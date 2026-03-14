package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/deuswork/nintendoflow/internal/ai"
	"github.com/deuswork/nintendoflow/internal/cleaner"
	"github.com/deuswork/nintendoflow/internal/config"
	"github.com/deuswork/nintendoflow/internal/db"
	"github.com/deuswork/nintendoflow/internal/dedup"
	"github.com/deuswork/nintendoflow/internal/fetcher"
	"github.com/deuswork/nintendoflow/internal/scorer"
	"github.com/deuswork/nintendoflow/internal/telegram"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// ── 1. Config ─────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	// ── 2. DB connect (retry 3×) ──────────────────────────────────────────
	database, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect failed", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := db.RunMigration(database); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// ── 3. DB cleanup ─────────────────────────────────────────────────────
	cleaner.Run(database)

	ctx := context.Background()

	// ── 4. Init AI chain ──────────────────────────────────────────────────
	geminiProvider, err := ai.NewGeminiProvider(ctx, cfg.GeminiAPIKey)
	if err != nil {
		slog.Error("gemini init failed", "error", err)
		os.Exit(1)
	}

	var providers []ai.AIProvider
	providers = append(providers, geminiProvider)

	if cfg.OpenRouterAPIKey != "" {
		providers = append(providers, ai.NewOpenRouterProvider(cfg.OpenRouterAPIKey))
		slog.Info("OpenRouter fallback enabled")
	} else {
		slog.Info("OpenRouter fallback disabled (no OPENROUTER_API_KEY)")
	}

	chain := ai.NewChain(cfg.SleepBetweenAICalls, providers...)

	// ── 5. Fetch RSS (parallel, 30s timeout) ─────────────────────────────
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	items := fetcher.FetchAll(fetchCtx)
	slog.Info("fetched articles", "count", len(items))

	// ── 6. Dedup + score + insert ─────────────────────────────────────────
	recentTitles, err := db.RecentTitles(database)
	if err != nil {
		slog.Warn("could not load recent titles", "error", err)
	}

	var (
		fetchedCount   = len(items)
		dedupedCount   int
		scoredOutCount int
	)

	for _, item := range items {
		// Layer 1: URL dedup
		exists, err := db.URLExists(database, item.Link)
		if err != nil {
			slog.Warn("url exists check failed", "error", err)
			continue
		}
		if exists {
			dedupedCount++
			continue
		}

		// Layer 2: title similarity dedup
		if dedup.IsDuplicate(item.Title, recentTitles) {
			dedupedCount++
			continue
		}

		// Score
		s := scorer.ScoreArticle(item.Title, item.Description, item.SourceType)
		if s < cfg.MinScore {
			scoredOutCount++
			continue
		}

		article := db.Article{
			SourceURL:   item.Link,
			ContentHash: item.ContentHash,
			TitleRaw:    item.Title,
			ImageURL:    item.ImageURL,
			SourceName:  item.SourceName,
			SourceType:  item.SourceType,
			Score:       s,
			PublishedAt: item.PublishedAt,
		}

		if _, err := db.InsertArticle(database, article); err != nil {
			slog.Warn("insert article failed", "url", item.Link, "error", err)
		}
		recentTitles = append(recentTitles, item.Title)
	}

	// ── 7. Top unposted articles ──────────────────────────────────────────
	unposted, err := db.TopUnposted(database, cfg.MaxPostsPerRun)
	if err != nil {
		slog.Error("top unposted query failed", "error", err)
		os.Exit(1)
	}

	if len(unposted) == 0 {
		slog.Info("no new articles to post — silence is golden")
		logStats(fetchedCount, dedupedCount, scoredOutCount, 0, 0, 0)
		return
	}

	// ── 8. AI rewrite + post ──────────────────────────────────────────────
	bot, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		slog.Error("telegram bot init failed", "error", err)
		os.Exit(1)
	}

	var (
		postedCount     int
		geminiCount     int
		openRouterCount int
	)

	for _, article := range unposted {
		text, usedProvider, err := chain.Rewrite(ctx, article.TitleRaw, "", article.SourceName)

		if errors.Is(err, ai.ErrSkipped) {
			slog.Info("AI skipped article", "id", article.ID, "title", article.TitleRaw)
			if markErr := db.MarkPosted(database, article.ID); markErr != nil {
				slog.Warn("mark posted failed", "error", markErr)
			}
			continue
		}
		if errors.Is(err, ai.ErrAllProvidersFailed) {
			slog.Error("all AI providers failed", "article_id", article.ID)
			continue
		}
		if err != nil {
			slog.Error("AI rewrite error", "error", err, "article_id", article.ID)
			continue
		}

		if err := db.UpdateBodyUA(database, article.ID, text, usedProvider); err != nil {
			slog.Warn("update body_ua failed", "error", err)
		}

		if cfg.DryRun {
			slog.Info("DRY_RUN — would post", "provider", usedProvider, "text", text)
			if usedProvider == "gemini-2.5-flash" {
				geminiCount++
			} else {
				openRouterCount++
			}
			postedCount++
			continue
		}

		if err := telegram.PostArticle(bot, cfg.TelegramChannelID, telegram.Article{
			BodyUA:   text,
			ImageURL: article.ImageURL,
		}); err != nil {
			slog.Error("telegram post failed", "error", err, "article_id", article.ID)
			continue
		}

		if err := db.MarkPosted(database, article.ID); err != nil {
			slog.Warn("mark posted failed", "error", err)
		}

		if usedProvider == "gemini-2.5-flash" {
			geminiCount++
		} else {
			openRouterCount++
		}
		postedCount++

		time.Sleep(3 * time.Second)
	}

	// ── 9. Summary log ────────────────────────────────────────────────────
	logStats(fetchedCount, dedupedCount, scoredOutCount, postedCount, geminiCount, openRouterCount)
}

func logStats(fetched, deduped, scoredOut, posted, gemini, openRouter int) {
	slog.Info("run complete",
		"fetched", fetched,
		"deduped", deduped,
		"scored_out", scoredOut,
		"posted", posted,
		"gemini_used", gemini,
		"openrouter_used", openRouter,
	)
}
