package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/db"
	"github.com/deuswork/nintendoflow/pkg/telegram"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	secretToken := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET"))
	if secretToken != "" {
		received := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if received != secretToken {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	var update tgbotapi.Update
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if update.CallbackQuery == nil && (update.Message == nil || strings.TrimSpace(update.Message.Text) == "") {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	var err error
	switch {
	case update.CallbackQuery != nil:
		err = handleCallback(r.Context(), update.CallbackQuery)
	case update.Message != nil && strings.TrimSpace(update.Message.Text) != "":
		err = handleEditMessage(r.Context(), update.Message)
	}
	if err != nil {
		slog.Error("webhook callback handling failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func handleCallback(parent context.Context, cb *tgbotapi.CallbackQuery) error {
	if cb == nil {
		return fmt.Errorf("nil callback")
	}

	testToken := strings.TrimSpace(os.Getenv("TEST_TELEGRAM_TOKEN"))
	testChannelID := strings.TrimSpace(os.Getenv("TEST_CHANNEL_ID"))
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if testToken == "" || testChannelID == "" || dsn == "" {
		return fmt.Errorf("missing required env for webhook (TEST_TELEGRAM_TOKEN, TEST_CHANNEL_ID, DATABASE_URL)")
	}

	action, articleID, err := telegram.ParseModerationCallbackData(cb.Data)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()

	database, err := db.Connect(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if err := db.RunMigration(ctx, database); err != nil {
		return err
	}

	bot, err := tgbotapi.NewBotAPI(testToken)
	if err != nil {
		return err
	}

	switch action {
	case "publish":
		if err := publishPendingArticle(ctx, database, bot, testChannelID, articleID); err != nil {
			return err
		}
		if err := db.DeleteModerationEditSessionsByArticle(ctx, database, articleID); err != nil {
			return err
		}
		if cb.Message != nil {
			text := "Published ✅"
			if err := telegram.EditModerationMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, text); err != nil {
				return err
			}
		}
		answerCallback(bot, cb.ID, "Published")
	case "reject":
		if err := db.UpdateArticleStatus(ctx, database, articleID, db.StatusRejected); err != nil {
			return err
		}
		if err := db.DeleteModerationEditSessionsByArticle(ctx, database, articleID); err != nil {
			return err
		}
		if cb.Message != nil {
			if err := telegram.EditModerationMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, "Rejected ❌"); err != nil {
				return err
			}
		}
		answerCallback(bot, cb.ID, "Rejected")
	case "edit":
		if cb.Message == nil {
			return fmt.Errorf("edit callback missing source message")
		}
		article, err := db.GetArticleByID(ctx, database, articleID)
		if err != nil {
			return err
		}
		if err := db.UpdateArticleStatus(ctx, database, articleID, db.StatusNeedsEdit); err != nil {
			return err
		}
		if err := db.UpsertModerationEditSession(ctx, database, db.ModerationEditSession{
			ChatID:           cb.Message.Chat.ID,
			UserID:           int64(cb.From.ID),
			ArticleID:        articleID,
			PreviewMessageID: cb.Message.MessageID,
		}); err != nil {
			return err
		}
		if err := telegram.EditModerationMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, telegram.BuildModerationEditWaitingText(article)); err != nil {
			return err
		}
		answerCallback(bot, cb.ID, "Send the replacement text")
	default:
		return fmt.Errorf("unsupported moderation action: %s", action)
	}

	return nil
}

func handleEditMessage(parent context.Context, message *tgbotapi.Message) error {
	if message == nil || message.From == nil {
		return nil
	}

	testToken := strings.TrimSpace(os.Getenv("TEST_TELEGRAM_TOKEN"))
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if testToken == "" || dsn == "" {
		return fmt.Errorf("missing required env for webhook edit mode (TEST_TELEGRAM_TOKEN, DATABASE_URL)")
	}

	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()

	database, err := db.Connect(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()

	if err := db.RunMigration(ctx, database); err != nil {
		return err
	}

	session, err := db.GetModerationEditSession(ctx, database, message.Chat.ID, int64(message.From.ID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	updatedBody := strings.TrimSpace(message.Text)
	if updatedBody == "" {
		return nil
	}

	article, err := db.GetArticleByID(ctx, database, session.ArticleID)
	if err != nil {
		return err
	}

	bot, err := tgbotapi.NewBotAPI(testToken)
	if err != nil {
		return err
	}

	if article.Status == db.StatusPublished || article.Status == db.StatusRejected {
		if err := db.DeleteModerationEditSession(ctx, database, session.ChatID, session.UserID); err != nil {
			return err
		}
		return sendEditAck(bot, message.Chat.ID, message.MessageID, "Edit session expired: the article is already finalized.")
	}

	if err := db.UpdateBodyUAOnly(ctx, database, article.ID, updatedBody); err != nil {
		return err
	}
	if err := db.UpdateArticleStatus(ctx, database, article.ID, db.StatusPending); err != nil {
		return err
	}
	if err := db.DeleteModerationEditSession(ctx, database, session.ChatID, session.UserID); err != nil {
		return err
	}

	article.BodyUA = updatedBody
	article.Status = db.StatusPending
	if err := telegram.EditModerationPreview(bot, message.Chat.ID, session.PreviewMessageID, article); err != nil {
		previewChatID := strconv.FormatInt(message.Chat.ID, 10)
		if _, sendErr := telegram.SendModerationPreview(bot, previewChatID, article); sendErr != nil {
			return fmt.Errorf("restore moderation preview: %w (fallback send error: %v)", err, sendErr)
		}
	}

	return sendEditAck(bot, message.Chat.ID, message.MessageID, "Updated ✅ Review the refreshed preview and press Publish or Reject.")
}

func publishPendingArticle(ctx context.Context, database *sql.DB, bot *tgbotapi.BotAPI, channelID string, articleID int) error {
	article, err := db.GetArticleByID(ctx, database, articleID)
	if err != nil {
		return err
	}
	if err := telegram.PostArticle(bot, channelID, article); err != nil {
		return err
	}
	if err := db.MarkPosted(ctx, database, articleID); err != nil {
		return err
	}
	return nil
}

func answerCallback(bot *tgbotapi.BotAPI, callbackID, text string) {
	if callbackID == "" {
		return
	}
	cb := tgbotapi.NewCallback(callbackID, text)
	_, _ = bot.Request(cb)
}

func sendEditAck(bot *tgbotapi.BotAPI, chatID int64, replyTo int, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = replyTo
	_, err := bot.Send(msg)
	if err != nil {
		return fmt.Errorf("telegram send edit ack: %w", err)
	}
	return nil
}
