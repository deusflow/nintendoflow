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
	ModerationActionPubTG   = "pub_tg"
	ModerationActionPubTH   = "pub_th"
	ModerationActionEditTG  = "edit_tg"
	ModerationActionEditTH  = "edit_th"
	ModerationActionReject  = "reject"
	ModerationActionCancel  = "cancel"
	ModerationActionNoop    = "noop"
)

func SendModerationPreview(bot *tgbotapi.BotAPI, chatID string, article db.Article) (int, error) {
	chat, channel, err := resolveChat(chatID)
	if err != nil {
		return 0, err
	}

	// 1. Send TG Preview
	tgText := buildTGPreviewText(article)
	tgMarkup := moderationTGKeyboard(article)

	previewImage := strings.TrimSpace(article.ImageURL)
	if previewImage == "" && strings.TrimSpace(article.VideoURL) == "" {
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
			Caption:   fmt.Sprintf("<b>Preview Telegram</b>\nPhoto attached for: %s", escapeHTML(article.TitleRaw)),
			ParseMode: "HTML",
		}
		// Send photo without markup to avoid caption limits
		_, sendErr := bot.Send(photo)
		if sendErr != nil {
			slog.Warn("telegram preview media send failed",
				"mode", "preview",
				"step", "send_photo",
				"article_id", article.ID,
				"error", sendErr,
			)
		}
	}

	if strings.TrimSpace(article.VideoURL) != "" {
		tgText = fmt.Sprintf("%s\n\n🔗 %s", tgText, strings.TrimSpace(article.VideoURL))
	}

	tgMsg, err := newTextMessage(chatID, tgText)
	if err != nil {
		return 0, err
	}
	tgMsg.ParseMode = "HTML"
	tgMsg.DisableWebPagePreview = true
	tgMsg.ReplyMarkup = tgMarkup

	tgSent, err := bot.Send(tgMsg)
	if err != nil {
		return 0, fmt.Errorf("telegram send TG preview: %w", err)
	}

	// 2. Send Threads Preview
	thText := buildTHPreviewText(article)
	thMarkup := moderationTHKeyboard(article)
	thMsg, err := newTextMessage(chatID, thText)
	if err == nil {
		thMsg.ParseMode = "HTML"
		thMsg.DisableWebPagePreview = true
		thMsg.ReplyMarkup = thMarkup
		_, _ = bot.Send(thMsg)
	}

	return tgSent.MessageID, nil
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

func EditModerationWaitingMessage(bot *tgbotapi.BotAPI, chatID int64, messageID int, article db.Article, target string) error {
	markup := moderationWaitingKeyboard(article.ID)
	return editModerationMessage(bot, chatID, messageID, BuildModerationEditWaitingText(article, target), &markup)
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

func EditModerationPreview(bot *tgbotapi.BotAPI, chatID int64, messageID int, article db.Article, target string) error {
	var markup tgbotapi.InlineKeyboardMarkup
	var text string
	if target == "th" {
		markup = moderationTHKeyboard(article)
		text = buildTHPreviewText(article)
	} else {
		markup = moderationTGKeyboard(article)
		text = buildTGPreviewText(article)
	}

	if err := editModerationMessage(bot, chatID, messageID, text, &markup); err != nil {
		return fmt.Errorf("telegram edit moderation preview: %w", err)
	}
	return nil
}

func BuildModerationEditWaitingText(article db.Article, target string) string {
	tgtName := "Telegram"
	if target == "th" {
		tgtName = "Threads"
	}
	return fmt.Sprintf("<b>Edit %s mode enabled ✍️</b>\n\nSend your next text message to replace the %s article body for:\n<b>%s</b>", tgtName, tgtName, escapeHTML(article.TitleRaw))
}

func ParseModerationCallbackData(data string) (string, int, error) {
	parts := strings.Split(data, ":")
	if len(parts) != 3 || parts[0] != "mod" {
		return "", 0, fmt.Errorf("invalid callback data")
	}
	action := parts[1]
	switch action {
	case ModerationActionPubTG, ModerationActionPubTH, ModerationActionEditTG, ModerationActionEditTH, ModerationActionReject, ModerationActionCancel, ModerationActionNoop:
	default:
		return "", 0, fmt.Errorf("unsupported callback action")
	}
	id, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", 0, fmt.Errorf("invalid article id in callback")
	}
	return action, id, nil
}

func moderationTGKeyboard(article db.Article) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	btnPub := tgbotapi.NewInlineKeyboardButtonData("TG: Publish", fmt.Sprintf("mod:%s:%d", ModerationActionPubTG, article.ID))
	if article.PostedTG {
		btnPub = tgbotapi.NewInlineKeyboardButtonData("TG: ✅", fmt.Sprintf("mod:%s:%d", ModerationActionNoop, article.ID))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(btnPub))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Edit TG", fmt.Sprintf("mod:%s:%d", ModerationActionEditTG, article.ID)),
		tgbotapi.NewInlineKeyboardButtonData("Reject All", fmt.Sprintf("mod:%s:%d", ModerationActionReject, article.ID)),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func moderationTHKeyboard(article db.Article) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	btnPub := tgbotapi.NewInlineKeyboardButtonData("Threads: Publish", fmt.Sprintf("mod:%s:%d", ModerationActionPubTH, article.ID))
	if article.PostedThreads {
		btnPub = tgbotapi.NewInlineKeyboardButtonData("Threads: ✅", fmt.Sprintf("mod:%s:%d", ModerationActionNoop, article.ID))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(btnPub))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Edit Threads", fmt.Sprintf("mod:%s:%d", ModerationActionEditTH, article.ID)),
		tgbotapi.NewInlineKeyboardButtonData("Reject All", fmt.Sprintf("mod:%s:%d", ModerationActionReject, article.ID)),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func moderationWaitingKeyboard(articleID int) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Cancel", fmt.Sprintf("mod:%s:%d", ModerationActionCancel, articleID)),
		),
	)
}

func buildTGPreviewText(article db.Article) string {
	body := stripSourceFooter(article.BodyUA)
	if body == "" {
		body = "(empty body)"
	}
	bodyRunes := []rune(body)
	if len(bodyRunes) > 1800 {
		body = string(bodyRunes[:1800]) + "..."
	}

	typeLabel := articleTypeLabel(article.ArticleType)

	return fmt.Sprintf(
		"<b>Preview Telegram</b>\n\n<b>Title:</b> %s\n<b>Source:</b> %s · %s · <b>%d pts</b>\n\n%s\n\n<a href=\"%s\">Original link</a>",
		escapeHTML(article.TitleRaw),
		escapeHTML(article.SourceName),
		typeLabel,
		article.Score,
		body,
		escapeHTML(article.SourceURL),
	)
}

func buildTHPreviewText(article db.Article) string {
	body := article.BodyThreads
	if body == "" {
		body = "(empty body threads)"
	}
	bodyRunes := []rune(body)
	if len(bodyRunes) > 1800 {
		body = string(bodyRunes[:1800]) + "..."
	}

	typeLabel := articleTypeLabel(article.ArticleType)

	return fmt.Sprintf(
		"<b>Preview Threads</b>\n\n<b>Title:</b> %s\n<b>Source:</b> %s · %s · <b>%d pts</b>\n\n%s\n\n<a href=\"%s\">Original link</a>",
		escapeHTML(article.TitleRaw),
		escapeHTML(article.SourceName),
		typeLabel,
		article.Score,
		body,
		escapeHTML(article.SourceURL),
	)
}

func articleTypeLabel(t string) string {
	switch t {
	case "insight":
		return "🔍 інсайт"
	case "rumor":
		return "🤫 чутки"
	case "offtop":
		return "💬 офтоп"
	default:
		return "📰 новина"
	}
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
