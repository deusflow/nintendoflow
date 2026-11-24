package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/deuswork/nintendoflow/pkg/config"
	"github.com/deuswork/nintendoflow/pkg/db"
	"github.com/deuswork/nintendoflow/pkg/fetcher"
)

// monitorFeedHealth records per-feed failure/empty streaks in bot_state and alerts
// the admin after 3 consecutive failing runs.
func monitorFeedHealth(ctx context.Context, database *sql.DB, cfg *config.Config, statuses []fetcher.FeedStatus) {
	if database == nil || len(statuses) == 0 {
		return
	}
	hasRedditOAuth := strings.TrimSpace(os.Getenv("REDDIT_CLIENT_ID")) != ""

	for _, st := range statuses {
		if st.FetchMode == "reddit_json" && !hasRedditOAuth && strings.Contains(st.ErrMessage, "403") {
			continue
		}

		key := "feed_fails:" + st.FeedName
		curVal, _ := db.GetState(ctx, database, key)
		fails, _ := strconv.Atoi(curVal)

		if st.ErrMessage != "" || st.ItemCount == 0 {
			fails++
			if err := db.SetState(ctx, database, key, strconv.Itoa(fails)); err != nil {
				slog.Warn("failed to save feed health state", "feed", st.FeedName, "error", err)
			}
			if fails == 3 {
				errMsg := "0 items fetched"
				if st.ErrMessage != "" {
					errMsg = st.ErrMessage
				}
				msg := fmt.Sprintf("⚠️ <b>Сторож RSS-стрічок (NintendoFlow):</b>\nДжерело <b>%s</b> не віддає новин 3 запуски поспіль!\nURL: %s\nПричина: <code>%s</code>",
					st.FeedName, st.FeedURL, errMsg)
				sendAdminMonitorAlert(cfg, msg)
				slog.Warn("RSS feed failed 3 consecutive runs", "feed", st.FeedName, "error", errMsg)
			}
		} else if fails > 0 {
			_ = db.SetState(ctx, database, key, "0")
			slog.Info("RSS feed recovered", "feed", st.FeedName, "previous_fails", fails)
		}
	}
}

// monitorEmptyRuns tracks consecutive runs that produced zero posts and alerts after 3 in a row.
func monitorEmptyRuns(ctx context.Context, database *sql.DB, cfg *config.Config, posted bool) {
	if database == nil {
		return
	}
	const key = "consecutive_empty_runs"
	curVal, _ := db.GetState(ctx, database, key)
	emptyRuns, _ := strconv.Atoi(curVal)

	if !posted {
		emptyRuns++
		_ = db.SetState(ctx, database, key, strconv.Itoa(emptyRuns))
		if emptyRuns == 3 {
			msg := "⚠️ <b>Сторож запусків (NintendoFlow):</b>\nБот завершив 3 запуски поспіль без жодної нової новини!\nПеревірте RSS-джерела або критерії відбору."
			sendAdminMonitorAlert(cfg, msg)
			slog.Warn("3 consecutive empty runs without posts", "empty_runs", emptyRuns)
		}
		return
	}

	if emptyRuns > 0 {
		_ = db.SetState(ctx, database, key, "0")
	}
}

func sendAdminMonitorAlert(cfg *config.Config, htmlMsg string) {
	if cfg == nil || cfg.DryRun {
		return
	}
	botToken := cfg.TestTelegramToken
	if botToken == "" {
		botToken = cfg.TelegramBotToken
	}
	targetChatID := strings.TrimSpace(cfg.AdminChatID)
	if targetChatID == "" {
		targetChatID = strings.TrimSpace(cfg.TestAdminChatID)
	}
	if botToken == "" || targetChatID == "" {
		return
	}
	chatID, err := strconv.ParseInt(targetChatID, 10, 64)
	if err != nil {
		return
	}
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		return
	}
	msg := tgbotapi.NewMessage(chatID, htmlMsg)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true
	_, _ = bot.Send(msg)
}
