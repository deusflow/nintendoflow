package telegram

import (
	"fmt"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/deals"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// PostDealsDigest formats and sends a single digest post with up to 5 deals.
func PostDealsDigest(bot *tgbotapi.BotAPI, chatID string, finalDeals []deals.Deal) error {
	if len(finalDeals) == 0 {
		return nil
	}

	dateStr := time.Now().Format("02.01.2006")
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("🎮 <b>Знижки Nintendo eShop — %s</b>\n\n", dateStr))

	for _, d := range finalDeals {
		oldPriceStr := formatPrice(d.OldPrice)
		newPriceStr := formatPrice(d.NewPrice)

		sb.WriteString(fmt.Sprintf(
			"🔥 <b>%s</b> — <s>%s%s</s> → %s%s (-%d%%)\n",
			escapeHTML(d.Title), d.Currency, oldPriceStr, d.Currency, newPriceStr, d.Cut,
		))
		sb.WriteString(fmt.Sprintf("⭐ Metacritic: %d | Nintendo Switch\n", d.Metacritic))
		sb.WriteString(fmt.Sprintf("<i>%s</i>\n\n", escapeHTML(d.RedditQuote)))
	}

	msg := tgbotapi.NewMessageToChannel(chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true

	_, err := bot.Send(msg)
	return err
}

func formatPrice(price float64) string {
	if price == float64(int(price)) {
		return fmt.Sprintf("%.0f", price)
	}
	return fmt.Sprintf("%.2f", price)
}


