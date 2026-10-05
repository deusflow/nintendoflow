package telegram

import (
	"fmt"
	"sort"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// SendQuarantineAlert sends a summary message to Telegram when 5 or more articles
// were quarantined during a pipeline run due to missing or unparseable source dates.
func SendQuarantineAlert(bot *tgbotapi.BotAPI, chatID string, count int, sourceCounts map[string]int) error {
	chat, channel, err := resolveChat(chatID)
	if err != nil {
		return err
	}

	sources := make([]string, 0, len(sourceCounts))
	for src := range sourceCounts {
		sources = append(sources, src)
	}
	sort.Strings(sources)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("⚠️ в карантине %d статей\n\nИсточники:\n", count))
	for _, src := range sources {
		cnt := sourceCounts[src]
		sb.WriteString(fmt.Sprintf("• %s: %d\n", src, cnt))
	}

	msg := tgbotapi.NewMessage(chat, sb.String())
	if channel != "" {
		msg.ChannelUsername = channel
	}
	_, err = bot.Send(msg)
	return err
}
