package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/deuswork/nintendoflow/pkg/config"
	"github.com/deuswork/nintendoflow/pkg/db"
	"github.com/deuswork/nintendoflow/pkg/deals"
	"github.com/deuswork/nintendoflow/pkg/telegram"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	runStart := time.Now()

	slog.Info("deals digest starting")

	// -- 1. Config --
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// -- 2. DB connect (same as main bot) --
	database, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = database.Close() }()

	// Run migrations to ensure deal_history table exists
	if err := db.RunMigration(ctx, database); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// -- 3. Fetch & filter deals --
	finalDeals, err := deals.FetchAndFilter(
		ctx, database,
		cfg.ITADAPIKey,
		cfg.DiscountMinCut,
		cfg.DiscountMinMetacritic,
	)
	if err != nil {
		slog.Error("deals fetch failed", "error", err)
		// Requirement: if API unavailable — silently skip the week, log the error.
		return
	}

	if len(finalDeals) == 0 {
		slog.Info("no deals matching criteria, skipping this week")
		return
	}

	slog.Info("deals ready to post", "count", len(finalDeals))
	for _, d := range finalDeals {
		slog.Info("deal", "title", d.Title, "cut", d.Cut, "meta", d.Metacritic, "price", d.NewPrice)
	}

	// -- 4. Determine bot token and chat --
	var botToken, chatID string
	if cfg.TestModerationMode {
		botToken = cfg.TestTelegramToken
		chatID = strings.TrimSpace(cfg.TestAdminChatID)
		if chatID == "" {
			chatID = cfg.TestChannelID
		}
		slog.Info("running in TEST mode", "chatID", chatID)
	} else {
		botToken = cfg.TelegramBotToken
		chatID = cfg.TelegramChannelID
	}

	if cfg.DryRun {
		slog.Info("DRY_RUN — skipping telegram post")
		return
	}

	// -- 5. Send to Telegram --
	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		slog.Error("telegram bot init failed", "error", err)
		return
	}

	if cfg.TestModerationMode {
		// Create a dummy article to leverage the existing moderation pipeline
		htmlBody := telegram.FormatDealsDigestHTML(finalDeals)
		article := db.Article{
			TitleRaw:    "Nintendo eShop Deals",
			BodyUA:      htmlBody,
			SourceType:  "deals",
			ArticleType: "deals",
			SourceName:  "Nintendo eShop",
			Status:      db.StatusPending,
			PublishedAt: &runStart,
		}

		articleID, err := db.InsertArticle(ctx, database, article)
		if err != nil {
			slog.Error("insert deals pending article failed", "error", err)
			return
		}
		article.ID = articleID

		if err := db.UpdateBodyUA(ctx, database, articleID, htmlBody, "deals"); err != nil {
			slog.Error("update deals body_ua failed", "error", err)
			return
		}

		previewMessageID, err := telegram.SendModerationPreview(bot, chatID, article)
		if err != nil {
			slog.Error("send deals moderation preview failed", "error", err)
			return
		}
		slog.Info("deals moderation preview sent", "article_id", article.ID, "msg_id", previewMessageID)
	} else {
		if err := telegram.PostDealsDigest(bot, chatID, finalDeals); err != nil {
			slog.Error("telegram deals post failed", "error", err)
			return
		}
		slog.Info("deals digest posted successfully")
	}

	// -- 6. Mark deals as published in DB --
	for _, d := range finalDeals {
		if err := deals.MarkDealPublished(ctx, database, d); err != nil {
			slog.Warn("failed to mark deal as published", "deal", d.Title, "error", err)
		}
	}

	slog.Info("deals digest run complete", "duration_ms", time.Since(runStart).Milliseconds())
}
