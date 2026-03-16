package telegram

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/deuswork/nintendoflow/pkg/db"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	moderationActionPublish = "publish"
	moderationActionEdit    = "edit"
	moderationActionReject  = "reject"
	moderationActionCancel  = "cancel"
)

func SendModerationPreview(bot *tgbotapi.BotAPI, chatID string, article db.Article) (int, error) {
	text := buildModerationPreviewText(article)
	msg, err := newTextMessage(chatID, text)
	if err != nil {
		return 0, err
	}
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = moderationKeyboard(article.ID)

	sent, err := bot.Send(msg)
	if err != nil {
		return 0, fmt.Errorf("telegram send preview: %w", err)
	}
	return sent.MessageID, nil
}

func EditModerationMessage(bot *tgbotapi.BotAPI, chatID int64, messageID int, text string) error {
	return editModerationMessage(bot, chatID, messageID, text, nil)
}

func EditModerationWaitingMessage(bot *tgbotapi.BotAPI, chatID int64, messageID int, article db.Article) error {
	markup := moderationWaitingKeyboard(article.ID)
	return editModerationMessage(bot, chatID, messageID, BuildModerationEditWaitingText(article), &markup)
}

func editModerationMessage(bot *tgbotapi.BotAPI, chatID int64, messageID int, text string, markup *tgbotapi.InlineKeyboardMarkup) error {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "HTML"
	if markup != nil {
		edit.ReplyMarkup = markup
	}
	_, err := bot.Send(edit)
	if err != nil {
		return fmt.Errorf("telegram edit message: %w", err)
	}
	return nil
}

func EditModerationPreview(bot *tgbotapi.BotAPI, chatID int64, messageID int, article db.Article) error {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, buildModerationPreviewText(article))
	edit.ParseMode = "HTML"
	markup := moderationKeyboard(article.ID)
	edit.ReplyMarkup = &markup
	_, err := bot.Send(edit)
	if err != nil {
		return fmt.Errorf("telegram edit moderation preview: %w", err)
	}
	return nil
}

func BuildModerationEditWaitingText(article db.Article) string {
	return fmt.Sprintf("<b>Edit mode enabled ✍️</b>\n\nSend your next text message to replace the article body for:\n<b>%s</b>", escapeHTML(article.TitleRaw))
}

func ParseModerationCallbackData(data string) (string, int, error) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || parts[0] != "mod" {
		return "", 0, fmt.Errorf("invalid callback data")
	}
	action := parts[1]
	switch action {
	case moderationActionPublish, moderationActionEdit, moderationActionReject, moderationActionCancel:
	default:
		return "", 0, fmt.Errorf("unsupported callback action")
	}
	id, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", 0, fmt.Errorf("invalid article id in callback")
	}
	return action, id, nil
}

func moderationKeyboard(articleID int) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Publish", fmt.Sprintf("mod:%s:%d", moderationActionPublish, articleID)),
			tgbotapi.NewInlineKeyboardButtonData("Edit", fmt.Sprintf("mod:%s:%d", moderationActionEdit, articleID)),
			tgbotapi.NewInlineKeyboardButtonData("Reject", fmt.Sprintf("mod:%s:%d", moderationActionReject, articleID)),
		),
	)
}

func moderationWaitingKeyboard(articleID int) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Cancel", fmt.Sprintf("mod:%s:%d", moderationActionCancel, articleID)),
		),
	)
}

func buildModerationPreviewText(article db.Article) string {
	body := stripSourceFooter(article.BodyUA)
	if body == "" {
		body = "(empty body)"
	}
	bodyRunes := []rune(body)
	if len(bodyRunes) > 700 {
		body = string(bodyRunes[:700]) + "..."
	}
	return fmt.Sprintf("<b>Preview</b>\n\n<b>Title:</b> %s\n<b>Source:</b> %s\n<b>Score:</b> %d\n\n%s\n\n<a href=\"%s\">Original link</a>",
		escapeHTML(article.TitleRaw),
		escapeHTML(article.SourceName),
		article.Score,
		escapeHTML(body),
		escapeHTML(article.SourceURL),
	)
}

func newTextMessage(chatID string, text string) (tgbotapi.MessageConfig, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return tgbotapi.MessageConfig{}, fmt.Errorf("empty chat id")
	}
	if strings.HasPrefix(chatID, "@") {
		return tgbotapi.NewMessageToChannel(chatID, text), nil
	}
	numericID, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return tgbotapi.MessageConfig{}, fmt.Errorf("invalid numeric chat id: %w", err)
	}
	return tgbotapi.NewMessage(numericID, text), nil
}

func escapeHTML(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
	)
	return replacer.Replace(s)
}
