package telegram

import (
	"fmt"
	"strings"

	"github.com/deuswork/nintendoflow/internal/db"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// PostArticle sends an article to the Telegram channel.
// If the article has an image, sendPhoto is used; otherwise sendMessage.
func PostArticle(bot *tgbotapi.BotAPI, channelID string, article db.Article) error {
	if article.ImageURL != "" {
		photo := tgbotapi.PhotoConfig{
			BaseFile: tgbotapi.BaseFile{
				BaseChat: tgbotapi.BaseChat{
					ChannelUsername: channelID,
				},
				File: tgbotapi.FileURL(article.ImageURL),
			},
			Caption:   buildCaption(&article, 1024),
			ParseMode: "HTML",
		}
		photo.ReplyMarkup = makeKeyboard(article.SourceURL)
		if _, err := bot.Send(photo); err != nil {
			// fallback to text-only
			return sendText(bot, channelID, article)
		}
		return nil
	}
	return sendText(bot, channelID, article)
}

func sendText(bot *tgbotapi.BotAPI, channelID string, article db.Article) error {
	msg := tgbotapi.NewMessageToChannel(channelID, buildCaption(&article, 4096))
	msg.ParseMode = "HTML"
	msg.ReplyMarkup = makeKeyboard(article.SourceURL)
	if _, err := bot.Send(msg); err != nil {
		return fmt.Errorf("telegram sendMessage: %w", err)
	}
	return nil
}

func makeKeyboard(sourceURL string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonURL("🔗 Читати повністю", sourceURL),
		),
	)
}

// buildCaption forms the final post text based on source type.
func buildCaption(article *db.Article, maxLen int) string {
	var prefix string
	switch article.SourceType {
	case "official":
		prefix = "📢 <b>ОФІЦІЙНО</b>\n\n"
	case "insider":
		prefix = "🕵️ <i>Інсайд</i>\n\n"
	case "aggregator":
		prefix = "📡 <i>Чутки</i>\n\n"
	default:
		prefix = ""
	}

	body := stripSourceFooter(article.BodyUA)
	full := prefix + body
	runes := []rune(full)
	if len(runes) <= maxLen {
		return full
	}

	bodyRunes := []rune(body)
	allowed := maxLen - len([]rune(prefix)) - 3
	if allowed > 0 && allowed < len(bodyRunes) {
		return prefix + string(bodyRunes[:allowed]) + "..."
	}
	return string(runes[:maxLen])
}

// stripSourceFooter removes plain-text source/link lines because source is now
// delivered via inline URL button under the post.
func stripSourceFooter(body string) string {
	lines := strings.Split(body, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmed, "🔗") || strings.Contains(lower, "джерело:") || strings.Contains(lower, "source:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}
