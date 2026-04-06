package telegram

import (
	"fmt"
	"log/slog"
	"net/url"
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
	chat, channel, err := resolveChat(chatID)
	if err != nil {
		return 0, err
	}
	markup := moderationKeyboard(article.ID)

	previewImage := strings.TrimSpace(article.ImageURL)
	if previewImage == "" && strings.TrimSpace(article.VideoURL) == "" {
		// Use the same fallback images as PostArticle
		previewImage = getFallbackImageURL(article.ArticleType)
	} else if previewImage == "" {
		previewImage = youtubeThumbnailURL(article.VideoURL)
	}

	if previewImage != "" {
		photo := tgbotapi.PhotoConfig{
			BaseFile: tgbotapi.BaseFile{
				BaseChat: tgbotapi.BaseChat{ChatID: chat, ChannelUsername: channel},
				File:     tgbotapi.FileURL(previewImage),
			},
			Caption:   text,
			ParseMode: "HTML",
		}
		photo.ReplyMarkup = markup
		sent, sendErr := bot.Send(photo)
		if sendErr == nil {
			return sent.MessageID, nil
		}
		slog.Warn("telegram preview media send failed",
			"mode", "preview",
			"step", "send_photo",
			"article_id", article.ID,
			"image_url", previewImage,
			"video_url", strings.TrimSpace(article.VideoURL),
			"error", sendErr,
		)
	}

	if strings.TrimSpace(article.VideoURL) != "" {
		text = fmt.Sprintf("%s\n\n%s", text, strings.TrimSpace(article.VideoURL))
	}

	msg, err := newTextMessage(chatID, text)
	if err != nil {
		return 0, err
	}
	msg.ParseMode = "HTML"
	msg.DisableWebPagePreview = false
	msg.ReplyMarkup = markup

	sent, err := bot.Send(msg)
	if err != nil {
		return 0, fmt.Errorf("telegram send preview: %w", err)
	}
	return sent.MessageID, nil
}

func resolveChat(chatID string) (int64, string, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return 0, "", fmt.Errorf("empty chat id")
	}
	if strings.HasPrefix(chatID, "@") {
		return 0, chatID, nil
	}
	numericID, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("invalid numeric chat id: %w", err)
	}
	return numericID, "", nil
}

func youtubeThumbnailURL(videoURL string) string {
	u, err := url.Parse(strings.TrimSpace(videoURL))
	if err != nil || u == nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	var id string
	if host == "youtu.be" {
		id = strings.Trim(u.Path, "/")
	} else if strings.Contains(host, "youtube.com") {
		id = strings.TrimSpace(u.Query().Get("v"))
		if id == "" {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) >= 2 {
				switch parts[0] {
				case "embed", "shorts", "live":
					id = parts[1]
				}
			}
		}
	}
	id = strings.Trim(id, " ")
	if len(id) != 11 {
		return ""
	}
	return "https://i.ytimg.com/vi/" + id + "/hqdefault.jpg"
}

func EditModerationMessage(bot *tgbotapi.BotAPI, chatID int64, messageID int, text string) error {
	return editModerationMessage(bot, chatID, messageID, text, nil)
}

func EditModerationWaitingMessage(bot *tgbotapi.BotAPI, chatID int64, messageID int, article db.Article) error {
	markup := moderationWaitingKeyboard(article.ID)
	return editModerationMessage(bot, chatID, messageID, BuildModerationEditWaitingText(article), &markup)
}

func editModerationMessage(bot *tgbotapi.BotAPI, chatID int64, messageID int, text string, markup *tgbotapi.InlineKeyboardMarkup) error {
	captionEdit := tgbotapi.NewEditMessageCaption(chatID, messageID, text)
	captionEdit.ParseMode = "HTML"
	if markup != nil {
		captionEdit.ReplyMarkup = markup
	}
	if _, err := bot.Send(captionEdit); err == nil {
		return nil
	} else {
		slog.Debug("telegram edit caption failed, fallback to text",
			"chat_id", chatID,
			"message_id", messageID,
			"error", err,
		)
	}

	textEdit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	textEdit.ParseMode = "HTML"
	if markup != nil {
		textEdit.ReplyMarkup = markup
	}
	if _, err := bot.Send(textEdit); err != nil {
		return fmt.Errorf("telegram edit message: %w", err)
	}
	return nil
}

func EditModerationPreview(bot *tgbotapi.BotAPI, chatID int64, messageID int, article db.Article) error {
	markup := moderationKeyboard(article.ID)
	if err := editModerationMessage(bot, chatID, messageID, buildModerationPreviewText(article), &markup); err != nil {
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
	chat, channel, err := resolveChat(chatID)
	if err != nil {
		return tgbotapi.MessageConfig{}, err
	}
	if channel != "" {
		return tgbotapi.NewMessageToChannel(channel, text), nil
	}
	return tgbotapi.NewMessage(chat, text), nil
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
