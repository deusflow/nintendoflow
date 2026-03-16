package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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

	if update.CallbackQuery == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	if err := handleCallback(r.Context(), update.CallbackQuery); err != nil {
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
		if cb.Message != nil {
			if err := telegram.EditModerationMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, "Rejected ❌"); err != nil {
				return err
			}
		}
		answerCallback(bot, cb.ID, "Rejected")
	case "edit":
		if err := db.UpdateArticleStatus(ctx, database, articleID, db.StatusNeedsEdit); err != nil {
			return err
		}
		if cb.Message != nil {
			if err := telegram.EditModerationMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, "Needs Edit ✍️"); err != nil {
				return err
			}
		}
		answerCallback(bot, cb.ID, "Marked as needs edit")
	default:
		return fmt.Errorf("unsupported moderation action: %s", action)
	}

	return nil
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
