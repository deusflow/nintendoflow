package handler

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/deuswork/nintendoflow/pkg/db"
	"github.com/deuswork/nintendoflow/pkg/telegram"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var (
	_dbOnce sync.Once
	_db     *sql.DB
	_dbErr  error

	_botOnce sync.Once // ← добавить
	_bot     *tgbotapi.BotAPI
	_botErr  error
)

func getDB(dsn string) (*sql.DB, error) {
	_dbOnce.Do(func() {
		_db, _dbErr = db.Connect(dsn)
		if _dbErr == nil {
			_db.SetMaxOpenConns(3)
			_db.SetMaxIdleConns(1)
			_db.SetConnMaxLifetime(30 * time.Second)
		}
	})
	return _db, _dbErr
}

func getBot(token string) (*tgbotapi.BotAPI, error) {
	_botOnce.Do(func() {
		_bot, _botErr = tgbotapi.NewBotAPI(token)
	})
	return _bot, _botErr
}

const editSessionTTL = 30 * time.Minute

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	secretToken := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET"))
	if secretToken == "" {
		slog.Error("webhook: TELEGRAM_WEBHOOK_SECRET is not set — refusing all requests")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	received := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if subtle.ConstantTimeCompare([]byte(received), []byte(secretToken)) != 1 {
		slog.Warn("webhook: forbidden — secret token mismatch")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Best-effort cleanup of expired edit sessions on every request.
	if dsn := strings.TrimSpace(os.Getenv("DATABASE_URL")); dsn != "" {
		if database, dbErr := getDB(dsn); dbErr == nil {
			cleanCtx, cleanCancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cleanCancel()
			if cleanErr := db.CleanupExpiredModerationEditSessions(cleanCtx, database, editSessionTTL); cleanErr != nil {
				slog.Warn("webhook: cleanup expired edit sessions failed (non-fatal)", "error", cleanErr)
			}
		}
	}

	var update tgbotapi.Update
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		slog.Error("webhook: failed to decode update", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Log the raw shape of the incoming update for diagnostics.
	switch {
	case update.CallbackQuery != nil:
		slog.Info("webhook: received callback_query",
			"callback_id", update.CallbackQuery.ID,
			"data", update.CallbackQuery.Data,
			"from_id", update.CallbackQuery.From.ID,
			"chat_id", func() int64 {
				if update.CallbackQuery.Message != nil {
					return update.CallbackQuery.Message.Chat.ID
				}
				return 0
			}(),
		)
	case update.Message != nil:
		slog.Info("webhook: received message",
			"message_id", update.Message.MessageID,
			"chat_id", update.Message.Chat.ID,
			"chat_type", update.Message.Chat.Type,
			"from_id", func() int64 {
				if update.Message.From != nil {
					return update.Message.From.ID
				}
				return 0
			}(),
			"text_len", len(update.Message.Text),
			"has_text", strings.TrimSpace(update.Message.Text) != "",
		)
	default:
		slog.Info("webhook: received unsupported update type, ignoring")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	if update.CallbackQuery == nil && (update.Message == nil || strings.TrimSpace(update.Message.Text) == "") {
		slog.Info("webhook: no actionable content in update, ignoring")
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
		slog.Error("webhook: handler returned error", "error", err)
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
		return fmt.Errorf("parse callback data %q: %w", cb.Data, err)
	}

	slog.Info("handleCallback: parsed action",
		"action", action,
		"article_id", articleID,
		"from_id", cb.From.ID,
		"chat_id", func() int64 {
			if cb.Message != nil {
				return cb.Message.Chat.ID
			}
			return 0
		}(),
	)

	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()

	database, err := getDB(dsn)
	if err != nil {
		return fmt.Errorf("db connect: %w", err)
	}

	bot, err := getBot(testToken)
	if err != nil {
		return fmt.Errorf("bot api init: %w", err)
	}

	switch action {
	case "publish":
		slog.Info("handleCallback: publishing article", "article_id", articleID)
		if err := publishPendingArticle(ctx, database, bot, testChannelID, articleID); err != nil {
			return fmt.Errorf("publish article %d: %w", articleID, err)
		}
		if err := db.DeleteModerationEditSessionsByArticle(ctx, database, articleID); err != nil {
			slog.Warn("handleCallback: cleanup edit sessions after publish", "error", err, "article_id", articleID)
		}
		if cb.Message != nil {
			if err := telegram.EditModerationMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, "Published ✅"); err != nil {
				slog.Warn("handleCallback: edit message after publish failed", "error", err)
			}
		}
		answerCallback(bot, cb.ID, "Published")
		slog.Info("handleCallback: article published", "article_id", articleID)

	case "reject":
		slog.Info("handleCallback: rejecting article", "article_id", articleID)
		if err := db.UpdateArticleStatus(ctx, database, articleID, db.StatusRejected); err != nil {
			return fmt.Errorf("reject article %d: %w", articleID, err)
		}
		if err := db.DeleteModerationEditSessionsByArticle(ctx, database, articleID); err != nil {
			slog.Warn("handleCallback: cleanup edit sessions after reject", "error", err, "article_id", articleID)
		}
		if cb.Message != nil {
			if err := telegram.EditModerationMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, "Rejected ❌"); err != nil {
				slog.Warn("handleCallback: edit message after reject failed", "error", err)
			}
		}
		answerCallback(bot, cb.ID, "Rejected")
		slog.Info("handleCallback: article rejected", "article_id", articleID)

	case "edit":
		if cb.Message == nil {
			return fmt.Errorf("edit callback missing source message, article_id=%d", articleID)
		}
		chatID := cb.Message.Chat.ID
		userID := int64(cb.From.ID)
		msgID := cb.Message.MessageID

		slog.Info("handleCallback: entering edit mode",
			"article_id", articleID,
			"chat_id", chatID,
			"user_id", userID,
			"preview_message_id", msgID,
		)

		article, err := db.GetArticleByID(ctx, database, articleID)
		if err != nil {
			return fmt.Errorf("get article %d: %w", articleID, err)
		}

		if err := db.UpdateArticleStatus(ctx, database, articleID, db.StatusNeedsEdit); err != nil {
			return fmt.Errorf("set needs_edit status article %d: %w", articleID, err)
		}

		if err := db.UpsertModerationEditSession(ctx, database, db.ModerationEditSession{
			ChatID:           chatID,
			UserID:           userID,
			ArticleID:        articleID,
			PreviewMessageID: msgID,
		}); err != nil {
			return fmt.Errorf("upsert edit session article=%d chat=%d user=%d: %w", articleID, chatID, userID, err)
		}
		slog.Info("handleCallback: edit session saved",
			"article_id", articleID,
			"chat_id", chatID,
			"user_id", userID,
			"preview_message_id", msgID,
		)

		if err := telegram.EditModerationWaitingMessage(bot, chatID, msgID, article); err != nil {
			slog.Warn("handleCallback: edit waiting message failed (non-fatal)", "error", err)
		}
		answerCallback(bot, cb.ID, "Send the replacement text")

	case "cancel":
		slog.Info("handleCallback: cancelling edit mode", "article_id", articleID)
		if err := cancelEditSession(ctx, database, bot, cb, articleID); err != nil {
			return fmt.Errorf("cancel edit session article %d: %w", articleID, err)
		}
		answerCallback(bot, cb.ID, "Edit cancelled")
		slog.Info("handleCallback: edit mode cancelled", "article_id", articleID)

	default:
		return fmt.Errorf("unsupported moderation action: %s", action)
	}

	return nil
}

func handleEditMessage(parent context.Context, message *tgbotapi.Message) error {
	if message == nil {
		slog.Warn("handleEditMessage: nil message, skipping")
		return nil
	}
	if message.From == nil {
		slog.Warn("handleEditMessage: message.From is nil (channel post?), skipping",
			"message_id", message.MessageID,
			"chat_id", message.Chat.ID,
		)
		return nil
	}

	chatID := message.Chat.ID
	userID := int64(message.From.ID)
	msgID := message.MessageID
	text := strings.TrimSpace(message.Text)
	text = sanitizeEditedBody(text)

	slog.Info("handleEditMessage: processing text message",
		"chat_id", chatID,
		"user_id", userID,
		"message_id", msgID,
		"text_len", len(text),
	)

	if text == "" {
		slog.Info("handleEditMessage: empty text, skipping")
		return nil
	}

	testToken := strings.TrimSpace(os.Getenv("TEST_TELEGRAM_TOKEN"))
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if testToken == "" || dsn == "" {
		return fmt.Errorf("missing required env for webhook edit mode (TEST_TELEGRAM_TOKEN=%v DATABASE_URL=%v)",
			testToken != "", dsn != "")
	}

	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()

	database, err := getDB(dsn)
	if err != nil {
		return fmt.Errorf("db connect: %w", err)
	}

	slog.Info("handleEditMessage: looking up edit session",
		"chat_id", chatID,
		"user_id", userID,
	)

	session, err := db.GetModerationEditSession(ctx, database, chatID, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			slog.Warn("handleEditMessage: no active edit session found for this admin — text message ignored",
				"chat_id", chatID,
				"user_id", userID,
				"hint", "Press Edit on a news preview first",
			)
			return nil
		}
		return fmt.Errorf("get edit session chat=%d user=%d: %w", chatID, userID, err)
	}

	slog.Info("handleEditMessage: active edit session found",
		"session_article_id", session.ArticleID,
		"session_preview_msg_id", session.PreviewMessageID,
		"session_updated_at", session.UpdatedAt,
		"session_chat_id", session.ChatID,
		"session_user_id", session.UserID,
	)

	bot, err := getBot(testToken)
	if err != nil {
		return fmt.Errorf("bot api init: %w", err)
	}

	// Timeout check — do this early, before loading article.
	if editSessionExpired(session, time.Now()) {
		slog.Warn("handleEditMessage: edit session expired",
			"article_id", session.ArticleID,
			"updated_at", session.UpdatedAt,
			"ttl", editSessionTTL,
		)
		if err := expireEditSession(ctx, database, bot, chatID, session); err != nil {
			slog.Warn("handleEditMessage: expire session restore failed (non-fatal)", "error", err)
		}
		return sendEditAck(bot, chatID, msgID, "Edit session expired ⏱️ Press Edit again if you still want to change the text.")
	}

	article, err := db.GetArticleByID(ctx, database, session.ArticleID)
	if err != nil {
		return fmt.Errorf("get article %d: %w", session.ArticleID, err)
	}

	slog.Info("handleEditMessage: article loaded",
		"article_id", article.ID,
		"status", article.Status,
		"title", article.TitleRaw,
	)

	if article.Status == db.StatusPublished || article.Status == db.StatusRejected {
		slog.Warn("handleEditMessage: article already finalized, clearing session",
			"article_id", article.ID,
			"status", article.Status,
		)
		if err := db.DeleteModerationEditSession(ctx, database, session.ChatID, session.UserID); err != nil {
			slog.Warn("handleEditMessage: delete stale session failed", "error", err)
		}
		return sendEditAck(bot, chatID, msgID,
			fmt.Sprintf("Cannot edit: article is already %s.", article.Status))
	}

	slog.Info("handleEditMessage: applying body update",
		"article_id", article.ID,
		"new_body_len", len(text),
	)

	if err := db.UpdateBodyUAOnly(ctx, database, article.ID, text); err != nil {
		return fmt.Errorf("update body_ua article %d: %w", article.ID, err)
	}
	slog.Info("handleEditMessage: body_ua updated", "article_id", article.ID)

	if err := db.UpdateArticleStatus(ctx, database, article.ID, db.StatusPending); err != nil {
		return fmt.Errorf("restore pending status article %d: %w", article.ID, err)
	}
	slog.Info("handleEditMessage: status set back to pending", "article_id", article.ID)

	if err := db.DeleteModerationEditSession(ctx, database, session.ChatID, session.UserID); err != nil {
		slog.Warn("handleEditMessage: delete session after edit failed", "error", err)
	}
	slog.Info("handleEditMessage: edit session cleared", "article_id", article.ID)

	article.BodyUA = text
	article.Status = db.StatusPending

	if err := telegram.EditModerationPreview(bot, chatID, session.PreviewMessageID, article); err != nil {
		slog.Warn("handleEditMessage: inline preview edit failed, sending new preview message", "error", err)
		previewChatID := strconv.FormatInt(chatID, 10)
		if _, sendErr := telegram.SendModerationPreview(bot, previewChatID, article); sendErr != nil {
			slog.Error("handleEditMessage: fallback send new preview also failed", "error", sendErr)
			return fmt.Errorf("restore moderation preview: %w (fallback send error: %v)", err, sendErr)
		}
		slog.Info("handleEditMessage: sent new preview as fallback", "article_id", article.ID)
	} else {
		slog.Info("handleEditMessage: preview message updated inline", "article_id", article.ID)
	}

	return sendEditAck(bot, chatID, msgID, "Updated ✅ Review the refreshed preview and press Publish or Reject.")
}

func cancelEditSession(ctx context.Context, database *sql.DB, bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery, articleID int) error {
	if cb == nil || cb.Message == nil || cb.From == nil {
		return fmt.Errorf("cancel callback missing message or sender")
	}

	article, err := db.GetArticleByID(ctx, database, articleID)
	if err != nil {
		return fmt.Errorf("get article %d: %w", articleID, err)
	}

	session, err := db.GetModerationEditSession(ctx, database, cb.Message.Chat.ID, int64(cb.From.ID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			slog.Info("cancelEditSession: no session in DB, restoring preview from article state",
				"article_id", articleID,
				"article_status", article.Status,
			)
			return restoreArticlePreview(ctx, database, bot, cb.Message.Chat.ID, cb.Message.MessageID, article, false)
		}
		return fmt.Errorf("get edit session: %w", err)
	}

	if session.ArticleID != articleID {
		slog.Warn("cancelEditSession: session belongs to different article",
			"session_article_id", session.ArticleID,
			"requested_article_id", articleID,
		)
		return sendEditAck(bot, cb.Message.Chat.ID, cb.Message.MessageID,
			"Another article is currently in edit mode. Finish or cancel that one first.")
	}

	slog.Info("cancelEditSession: restoring preview",
		"article_id", articleID,
		"preview_message_id", session.PreviewMessageID,
	)
	return restoreArticlePreview(ctx, database, bot, cb.Message.Chat.ID, session.PreviewMessageID, article, true)
}

func expireEditSession(ctx context.Context, database *sql.DB, bot *tgbotapi.BotAPI, chatID int64, session db.ModerationEditSession) error {
	article, err := db.GetArticleByID(ctx, database, session.ArticleID)
	if err != nil {
		return fmt.Errorf("get article %d: %w", session.ArticleID, err)
	}
	return restoreArticlePreview(ctx, database, bot, chatID, session.PreviewMessageID, article, true)
}

func restoreArticlePreview(ctx context.Context, database *sql.DB, bot *tgbotapi.BotAPI, chatID int64, messageID int, article db.Article, deleteSession bool) error {
	if deleteSession {
		if err := db.DeleteModerationEditSessionsByArticle(ctx, database, article.ID); err != nil {
			slog.Warn("restoreArticlePreview: delete session failed (non-fatal)", "error", err, "article_id", article.ID)
		}
	}

	if article.Status == db.StatusNeedsEdit {
		if err := db.UpdateArticleStatus(ctx, database, article.ID, db.StatusPending); err != nil {
			return fmt.Errorf("restore pending status article %d: %w", article.ID, err)
		}
		article.Status = db.StatusPending
		slog.Info("restoreArticlePreview: status restored to pending", "article_id", article.ID)
	}

	if article.Status == db.StatusPublished || article.Status == db.StatusRejected {
		slog.Info("restoreArticlePreview: article already finalized, showing state text",
			"article_id", article.ID, "status", article.Status)
		return telegram.EditModerationMessage(bot, chatID, messageID, finalModerationStateText(article.Status))
	}

	slog.Info("restoreArticlePreview: restoring preview message with buttons",
		"article_id", article.ID,
		"chat_id", chatID,
		"message_id", messageID,
	)

	if err := telegram.EditModerationPreview(bot, chatID, messageID, article); err != nil {
		slog.Warn("restoreArticlePreview: inline edit failed, sending new message", "error", err)
		previewChatID := strconv.FormatInt(chatID, 10)
		if _, sendErr := telegram.SendModerationPreview(bot, previewChatID, article); sendErr != nil {
			return fmt.Errorf("restore moderation preview: %w (fallback send error: %v)", err, sendErr)
		}
	}
	return nil
}

func editSessionExpired(session db.ModerationEditSession, now time.Time) bool {
	if session.UpdatedAt.IsZero() {
		return false
	}
	return now.After(session.UpdatedAt.Add(editSessionTTL))
}

func finalModerationStateText(status string) string {
	switch status {
	case db.StatusPublished:
		return "Published ✅"
	case db.StatusRejected:
		return "Rejected ❌"
	default:
		return "Moderation updated"
	}
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

func sanitizeEditedBody(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if lower == "preview" || strings.HasPrefix(lower, "title:") || strings.HasPrefix(lower, "source:") || strings.HasPrefix(lower, "score:") {
			continue
		}
		if strings.HasPrefix(lower, "<b>title:</b>") || strings.HasPrefix(lower, "<b>source:</b>") || strings.HasPrefix(lower, "<b>score:</b>") {
			continue
		}
		if strings.Contains(lower, "original link") {
			continue
		}
		kept = append(kept, line)
	}
	clean := strings.TrimSpace(strings.Join(kept, "\n"))
	if strings.HasPrefix(strings.ToLower(clean), "<b>preview</b>") {
		clean = strings.TrimSpace(strings.TrimPrefix(clean, "<b>Preview</b>"))
	}
	return clean
}
