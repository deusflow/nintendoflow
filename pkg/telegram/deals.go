package telegram

import (
	"fmt"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/deals"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const telegramMaxMessageLen = 4096

// FormatDealsDigestHTML formats the deals into a single HTML string.
func FormatDealsDigestHTML(finalDeals []deals.Deal) string {
	if len(finalDeals) == 0 {
		return ""
	}

	dateStr := time.Now().Format("02.01.2006")
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🛒 <b>Знижки eShop — %s</b>\n\n", dateStr))

	for i, d := range finalDeals {
		oldPriceStr := formatPrice(d.OldPrice)
		newPriceStr := formatPrice(d.NewPrice)

		sb.WriteString(fmt.Sprintf("%d. <b>%s</b>\n", i+1, escapeHTML(d.Title)))
		sb.WriteString(fmt.Sprintf("💰 <s>%s%s</s> → <b>%s%s</b> (-%d%%)\n", d.Currency, oldPriceStr, d.Currency, newPriceStr, d.Cut))
		
		if d.Metacritic > 0 {
			sb.WriteString(fmt.Sprintf("⭐ Metacritic: %d\n", d.Metacritic))
		}
		
		quote := strings.TrimSpace(d.RedditQuote)
		if quote != "" {
			sb.WriteString(fmt.Sprintf("💬 <i>%s</i>\n", escapeHTML(quote)))
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

// PostDealsDigest formats and sends a digest post with up to 10 deals.
func PostDealsDigest(bot *tgbotapi.BotAPI, chatID string, finalDeals []deals.Deal) error {
	htmlText := FormatDealsDigestHTML(finalDeals)
	if htmlText == "" {
		return nil
	}

	// Telegram message length limit is 4096. We split if necessary.
	// Normally 10 deals won't exceed this, but just in case.
	if len(htmlText) <= telegramMaxMessageLen {
		msg := tgbotapi.NewMessageToChannel(chatID, htmlText)
		msg.ParseMode = tgbotapi.ModeHTML
		msg.DisableWebPagePreview = true
		_, err := bot.Send(msg)
		return err
	}

	// Fallback simple split if too long (rare)
	parts := splitString(htmlText, telegramMaxMessageLen)
	for _, p := range parts {
		msg := tgbotapi.NewMessageToChannel(chatID, p)
		msg.ParseMode = tgbotapi.ModeHTML
		msg.DisableWebPagePreview = true
		if _, err := bot.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

func splitString(s string, chunkSize int) []string {
	var chunks []string
	runes := []rune(s)
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}

func formatPrice(price float64) string {
	if price == float64(int(price)) {
		return fmt.Sprintf("%.0f", price)
	}
	return fmt.Sprintf("%.2f", price)
}
