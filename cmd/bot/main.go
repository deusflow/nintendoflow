package main

import (
	"context"
	"errors"
	"fmt"
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

	// -- 1. Config ---------------------------------------------------------
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	feeds, err := config.LoadFeeds(cfg.FeedsPath)
	if err != nil {
		slog.Error("feeds load failed", "path", cfg.FeedsPath, "error", err)
		os.Exit(1)
	}
	keywords, err := config.LoadKeywords(cfg.KeywordsPath)
	if err != nil {
		slog.Error("keywords load failed", "path", cfg.KeywordsPath, "error", err)
		os.Exit(1)
	}
	config.LogConfigLoaded(len(feeds), len(keywords))

	// -- 2. DB connect (retry 3x) -----------------------------------------
	database, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = database.Close() }()

	if err := db.RunMigration(database); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// -- 3. DB cleanup -----------------------------------------------------
	cleaner.Run(database)

	ctx := context.Background()

	// -- 4. Init AI chain --------------------------------------------------
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

	// -- 5. Fetch RSS (parallel, 30s timeout) ------------------------------
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	items := fetcher.FetchAll(fetchCtx, feeds)
	slog.Info("fetched articles", "count", len(items))

	// -- 6. Dedup + score + insert ----------------------------------------
	recentTitles, err := db.RecentTitles(database, cfg.RecentTitlesHours)
	if err != nil {
		slog.Warn("could not load recent titles", "error", err)
	}

	var (
		fetchedCount        = len(items)
		dedupedCount        int
		scoredOutCount      int
		anchorRejectedCount int
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

		// Score + Nintendo relevance gate
		result, ok, reason := scorer.ShouldPost(item.Title, item.Description, keywords, cfg.MinScore, item.RequireAnchor)
		if !ok {
			scoredOutCount++
			if reason == "missing_nintendo_anchor" {
				anchorRejectedCount++
			}
			continue
		}

		article := db.Article{
			SourceURL:   item.Link,
			ContentHash: item.ContentHash,
			TitleRaw:    item.Title,
			ImageURL:    item.ImageURL,
			SourceName:  item.SourceName,
			SourceType:  item.SourceType,
			Score:       result.Score,
			PublishedAt: item.PublishedAt,
		}

		if _, err := db.InsertArticle(database, article); err != nil {
			slog.Warn("insert article failed", "url", item.Link, "error", err)
		}
		recentTitles = append(recentTitles, item.Title)
	}

	// -- 7. Top unposted articles -----------------------------------------
	unposted, err := db.TopUnposted(database, cfg.MaxPostsPerRun)
	if err != nil {
		slog.Error("top unposted query failed", "error", err)
		os.Exit(1)
	}

	if len(unposted) == 0 {
		slog.Info("no new articles to post - silence is golden")
		logStats(fetchedCount, dedupedCount, scoredOutCount, anchorRejectedCount, 0, 0, 0)
		printChannelDescriptionHint()
		return
	}

	// -- 8. AI rewrite + post ---------------------------------------------
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
		article.BodyUA = text

		if cfg.DryRun {
			slog.Info("DRY_RUN - would post", "provider", usedProvider, "text", text)
			if usedProvider == "gemini-2.5-flash" {
				geminiCount++
			} else {
				openRouterCount++
			}
			postedCount++
			continue
		}

		if err := telegram.PostArticle(bot, cfg.TelegramChannelID, article); err != nil {
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

	// -- 9. Summary log ----------------------------------------------------
	logStats(fetchedCount, dedupedCount, scoredOutCount, anchorRejectedCount, postedCount, geminiCount, openRouterCount)
	printChannelDescriptionHint()
}

func logStats(fetched, deduped, scoredOut, anchorRejected, posted, gemini, openRouter int) {
	slog.Info("run complete",
		"fetched", fetched,
		"deduped", deduped,
		"scored_out", scoredOut,
		"anchor_rejected", anchorRejected,
		"posted", posted,
		"gemini_used", gemini,
		"openrouter_used", openRouter,
	)
}

func printChannelDescriptionHint() {
	fmt.Println("╔══════════════════════════════════════════════╗")
	fmt.Println("║        РЕКОМЕНДОВАНИЙ ОПИС КАНАЛУ           ║")
	fmt.Println("╠══════════════════════════════════════════════╣")
	fmt.Println("║ Назва: Nintendo UA 🎮                        ║")
	fmt.Println("║                                              ║")
	fmt.Println("║ Опис (до 255 символів):                      ║")
	fmt.Println("║ Найсвіжіші новини Nintendo українською —     ║")
	fmt.Println("║ ігри, залізо, анонси та інсайди.             ║")
	fmt.Println("║ Без води, з характером. 5 разів на день 🕹️   ║")
	fmt.Println("║                                              ║")
	fmt.Println("║ Як встановити:                               ║")
	fmt.Println("║ Telegram → твій канал → Edit → Description  ║")
	fmt.Println("╚══════════════════════════════════════════════╝")
}
