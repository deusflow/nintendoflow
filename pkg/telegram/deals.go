package telegram

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/deals"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const telegramMaxMessageLen = 4096

// PostDealsDigest formats and sends a digest post with up to 10 deals.
// If the message exceeds Telegram's 4096 char limit, it splits into multiple messages.
func PostDealsDigest(bot *tgbotapi.BotAPI, chatID string, finalDeals []deals.Deal) error {
	if len(finalDeals) == 0 {
		return nil
	}

	dateStr := time.Now().Format("02.01.2006")
	header := fmt.Sprintf("🎮 <b>Знижки Nintendo eShop — %s</b>\n\n", dateStr)

	// Build individual deal blocks
	var blocks []string
	for i, d := range finalDeals {
		oldPriceStr := formatPrice(d.OldPrice)
		newPriceStr := formatPrice(d.NewPrice)

		var sb strings.Builder
		fmt.Fprintf(&sb,
			"%d. 🔥 <b>%s</b> — <s>%s%s</s> → %s%s (-%d%%)\n",
			i+1, escapeHTML(d.Title), d.Currency, oldPriceStr, d.Currency, newPriceStr, d.Cut,
		)
		fmt.Fprintf(&sb, "⭐ Metacritic: %d | Nintendo Switch\n", d.Metacritic)
		fmt.Fprintf(&sb, "<i>%s</i>", escapeHTML(d.RedditQuote))

		blocks = append(blocks, sb.String())
	}

	// Assemble messages, splitting if they exceed 4096 chars
	var messages []string
	current := header

	for _, block := range blocks {
		candidate := current + block + "\n\n"
		if len(candidate) > telegramMaxMessageLen {
			// Current message is full, start a new one
			messages = append(messages, strings.TrimSpace(current))
			current = block + "\n\n"
		} else {
			current = candidate
		}
	}
	if strings.TrimSpace(current) != "" {
		messages = append(messages, strings.TrimSpace(current))
	}

	// Send all messages
	for i, text := range messages {
		msg := tgbotapi.NewMessageToChannel(chatID, text)
		msg.ParseMode = tgbotapi.ModeHTML
		msg.DisableWebPagePreview = true

		if _, err := bot.Send(msg); err != nil {
			return fmt.Errorf("send deals message %d/%d: %w", i+1, len(messages), err)
		}
		slog.Info("deals message sent", "part", i+1, "total", len(messages), "len", len(text))
	}

	return nil
}

func formatPrice(price float64) string {
	if price == float64(int(price)) {
		return fmt.Sprintf("%.0f", price)
	}
	return fmt.Sprintf("%.2f", price)
}
