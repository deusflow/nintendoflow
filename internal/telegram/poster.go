package telegram

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Article contains the data needed to post to Telegram.
type Article struct {
	BodyUA   string
	ImageURL string
}

// PostArticle sends an article to the Telegram channel.
// If the article has an image, sendPhoto is used; otherwise sendMessage.
func PostArticle(bot *tgbotapi.BotAPI, channelID string, article Article) error {
	text := article.BodyUA
	if len([]rune(text)) > 1000 {
		text = string([]rune(text)[:997]) + "..."
	}

	if article.ImageURL != "" {
		photo := tgbotapi.PhotoConfig{
			BaseFile: tgbotapi.BaseFile{
				BaseChat: tgbotapi.BaseChat{
					ChannelUsername: channelID,
				},
				File: tgbotapi.FileURL(article.ImageURL),
			},
			Caption:   text,
			ParseMode: "HTML",
		}
		if _, err := bot.Send(photo); err != nil {
			// fallback to text-only
			return sendText(bot, channelID, text)
		}
		return nil
	}
	return sendText(bot, channelID, text)
}

func sendText(bot *tgbotapi.BotAPI, channelID string, text string) error {
	msg := tgbotapi.NewMessageToChannel(channelID, text)
	msg.ParseMode = "HTML"
	if _, err := bot.Send(msg); err != nil {
		return fmt.Errorf("telegram sendMessage: %w", err)
	}
	return nil
}
