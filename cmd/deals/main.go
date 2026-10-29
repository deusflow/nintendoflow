package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/deuswork/nintendoflow/pkg/ai"
	"github.com/deuswork/nintendoflow/pkg/config"
	"github.com/deuswork/nintendoflow/pkg/db"
	"github.com/deuswork/nintendoflow/pkg/deals"
	"github.com/deuswork/nintendoflow/pkg/dedup"
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

	aiConfigPath := os.Getenv("AI_CONFIG_PATH")
	if aiConfigPath == "" {
		aiConfigPath = "ai_config.json"
	}
	var aiManager *ai.Manager
	if providers, aiErr := ai.BuildProvidersFromConfig(ctx, aiConfigPath); aiErr != nil {
		slog.Warn("AI router unavailable for deals quotes, using Ukrainian fallback blurbs", "error", aiErr)
	} else {
		aiManager = ai.NewManager(providers, 2, 10*time.Second)
	}
	deals.TranslateDealsQuotesUA(ctx, aiManager, finalDeals)

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
		// Create a pending article to leverage the existing moderation pipeline.
		// Deals are encoded in EventTag and marked in deal_history only upon admin approval in webhook.
		htmlBody := telegram.FormatDealsDigestHTML(finalDeals)
		sourceURL := fmt.Sprintf("https://www.nintendo.com/deals/%s", runStart.UTC().Format("20060102-150405"))
		article := db.Article{
			SourceURL:   sourceURL,
			URLHash:     dedup.HashURL(sourceURL),
			TitleHash:   dedup.HashTitle("Nintendo eShop Deals " + runStart.UTC().Format("2006-01-02")),
			ContentHash: dedup.HashTitle(htmlBody),
			TitleRaw:    "Nintendo eShop Deals",
			BodyUA:      htmlBody,
			SourceType:  "deals",
			ArticleType: "deals",
			SourceName:  "Nintendo eShop",
			Status:      db.StatusPending,
			EventTag:    deals.EncodeDealsMetadata(finalDeals),
			PublishedAt: &runStart,
		}

		articleID, err := db.InsertArticle(ctx, database, article)
		if err != nil {
			slog.Error("insert deals pending article failed", "error", err)
			return
		}
		article.ID = articleID

		if err := db.UpdateBodies(ctx, database, articleID, htmlBody, "", "deals"); err != nil {
			slog.Error("update deals bodies failed", "error", err)
			return
		}

		previewMessageID, err := telegram.SendModerationPreview(bot, chatID, article)
		if err != nil {
			slog.Error("send deals moderation preview failed", "error", err)
			return
		}
		slog.Info("deals moderation preview sent (will mark in history upon approval)", "article_id", article.ID, "msg_id", previewMessageID)
	} else {
		if err := telegram.PostDealsDigest(bot, chatID, finalDeals); err != nil {
			slog.Error("telegram deals post failed", "error", err)
			return
		}
		slog.Info("deals digest posted successfully")

		// Mark deals as published in DB
		for _, d := range finalDeals {
			if err := deals.MarkDealPublished(ctx, database, d); err != nil {
				slog.Warn("failed to mark deal as published", "deal", d.Title, "error", err)
			}
		}
	}

	slog.Info("deals digest run complete", "duration_ms", time.Since(runStart).Milliseconds())
}
