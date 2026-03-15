package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
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

const (
	maxAICallsPerRun = 2
	aiCallDelay      = 20 * time.Second
)

type candidate struct {
	item      fetcher.Item
	score     int
	urlHash   string
	titleHash string
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	runStart := time.Now()
	stageStart := runStart

	var (
		fetchedCount   int
		filteredCount  int
		aiSelectorUsed bool
		aiRewriteUsed  bool
		posted         bool
	)

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
	topics, err := config.LoadKeywords(cfg.KeywordsPath)
	if err != nil {
		slog.Error("keywords load failed", "path", cfg.KeywordsPath, "error", err)
		os.Exit(1)
	}
	totalKeywords := 0
	for _, t := range topics {
		totalKeywords += len(t.Keywords)
	}
	config.LogConfigLoaded(len(feeds), totalKeywords)
	logStage("config", stageStart, runStart)
	stageStart = time.Now()

	ctx := context.Background()

	// -- 2. DB connect (retry 3x) -----------------------------------------
	database, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = database.Close() }()

	if cfg.DryRun {
		slog.Info("DRY_RUN - skipping database writes", "skip_migration", true, "skip_cleanup", true)
	} else {
		if err := db.RunMigration(ctx, database); err != nil {
			slog.Error("migration failed", "error", err)
			os.Exit(1)
		}

		// -- 3. DB cleanup -----------------------------------------------------
		cleaner.Run(ctx, database)
	}
	logStage("db", stageStart, runStart)
	stageStart = time.Now()

	// -- 4. Init AI manager ------------------------------------------------
	aiConfigPath := os.Getenv("AI_CONFIG_PATH")
	if aiConfigPath == "" {
		aiConfigPath = "ai_config.json"
	}

	providers, err := ai.BuildProvidersFromConfig(ctx, aiConfigPath)
	if err != nil {
		slog.Error("AI router init failed", "config_path", aiConfigPath, "error", err)
		os.Exit(1)
	}
	providerNames := make([]string, 0, len(providers))
	for _, provider := range providers {
		providerNames = append(providerNames, provider.Name())
	}
	slog.Info("AI router ready", "config_path", aiConfigPath, "providers", strings.Join(providerNames, ","))

	manager := ai.NewManager(providers, maxAICallsPerRun, aiCallDelay)
	logStage("ai_provider_init", stageStart, runStart)
	stageStart = time.Now()

	// -- 5. Fetch RSS (parallel, 30s timeout) ------------------------------
	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	items := fetcher.FetchAll(fetchCtx, feeds)
	fetchedCount = len(items)
	slog.Info("fetched articles", "count", len(items))
	logStage("fetch", stageStart, runStart)
	stageStart = time.Now()

	// -- 6. Local filtering only (freshness + DB dedup hashes + score) ----
	cutoff := time.Now().Add(-time.Duration(cfg.RecentTitlesHours) * time.Hour)
	candidates := make([]candidate, 0, len(items))

	knownURLs, err := db.FetchRecentURLHashes(ctx, database, cfg.RecentTitlesHours)
	if err != nil {
		slog.Warn("fetch recent url hashes failed", "error", err)
		knownURLs = make(map[string]struct{})
	}
	knownTitles, err := db.FetchRecentTitleHashes(ctx, database, cfg.RecentTitlesHours)
	if err != nil {
		slog.Warn("fetch recent title hashes failed", "error", err)
		knownTitles = make(map[string]struct{})
	}

	for _, item := range items {
		if item.PublishedAt == nil || item.PublishedAt.Before(cutoff) {
			continue
		}

		urlHash := dedup.HashURL(item.Link)
		titleHash := dedup.HashTitle(item.Title)

		if _, exists := knownURLs[urlHash]; exists {
			continue
		}
		if _, exists := knownTitles[titleHash]; exists {
			continue
		}

		// Score + Nintendo relevance gate
		result, ok, reason := scorer.ShouldPost(item.Title, item.Description, topics, cfg.MinScore, item.RequireAnchor)
		if !ok {
			slog.Debug("candidate filtered out", "reason", reason, "title", item.Title)
			continue
		}

		candidates = append(candidates, candidate{
			item:      item,
			score:     result.Score,
			urlHash:   urlHash,
			titleHash: titleHash,
		})
		knownURLs[urlHash] = struct{}{}
		knownTitles[titleHash] = struct{}{}
	}
	filteredCount = len(candidates)
	logStage("local_filter", stageStart, runStart)
	stageStart = time.Now()

	if len(candidates) < 1 {
		slog.Info("no candidates this run")
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].item.PublishedAt.After(*candidates[j].item.PublishedAt)
		}
		return candidates[i].score > candidates[j].score
	})

	topN := 5
	if len(candidates) < topN {
		topN = len(candidates)
	}
	topCandidates := candidates[:topN]

	selectionPrompt := buildSelectorPrompt(topCandidates)

	aiSelectorUsed = true
	rawSelection, err := manager.Generate(ctx, selectionPrompt)
	selectedIdx := 0
	if err != nil {
		slog.Warn("AI selector failed, using top-scored fallback", "error", err)
	} else {
		slog.Info("AI selector used", "provider", manager.LastProvider())
		idx, ok := parseSelectedIndex(rawSelection, len(topCandidates))
		if !ok {
			slog.Warn("AI selector returned invalid number, using top-scored fallback", "response", rawSelection)
		} else {
			selectedIdx = idx
		}
	}
	logStage("ai_selector", stageStart, runStart)
	stageStart = time.Now()

	selected := topCandidates[selectedIdx]

	// Step 6.5: fetch og:image only for the single winning article.
	selected.item.ImageURL = ""
	imageCtx, imageCancel := context.WithTimeout(ctx, 10*time.Second)
	ogImage, imageErr := fetcher.FetchOGImage(imageCtx, selected.item.Link)
	imageCancel()
	if imageErr != nil {
		slog.Warn("og:image fetch failed, fallback to no image", "url", selected.item.Link, "error", imageErr)
	} else if ogImage == "" {
		slog.Info("og:image not found, fallback to no image", "url", selected.item.Link)
	} else {
		selected.item.ImageURL = ogImage
		slog.Info("og:image extracted", "url", selected.item.Link)
	}
	logStage("image_fetch", stageStart, runStart)
	stageStart = time.Now()

	rewritePrompt := ai.BuildPrompt(ai.NewsInput{
		Title:  selected.item.Title,
		Body:   selected.item.Description,
		Source: selected.item.SourceName,
	})
	aiRewriteUsed = true
	rewritten, err := manager.Generate(ctx, rewritePrompt)
	if errors.Is(err, ai.ErrSkipped) {
		slog.Info("AI skipped selected candidate", "title", selected.item.Title)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}
	if err != nil {
		slog.Error("AI rewrite failed", "error", err)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}
	rewriteProvider := manager.LastProvider()
	slog.Info("AI rewrite used", "provider", rewriteProvider)
	logStage("ai_rewrite", stageStart, runStart)
	stageStart = time.Now()

	article := db.Article{
		SourceURL:   selected.item.Link,
		URLHash:     selected.urlHash,
		TitleHash:   selected.titleHash,
		ContentHash: selected.item.ContentHash,
		TitleRaw:    selected.item.Title,
		BodyUA:      rewritten,
		ImageURL:    selected.item.ImageURL,
		SourceName:  selected.item.SourceName,
		SourceType:  selected.item.SourceType,
		Score:       selected.score,
		PublishedAt: selected.item.PublishedAt,
	}

	if cfg.DryRun {
		slog.Info("DRY_RUN - would post selected article", "title", article.TitleRaw, "provider", rewriteProvider)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	articleID, err := db.InsertArticle(ctx, database, article)
	if err != nil {
		slog.Error("insert selected article failed", "error", err)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}
	article.ID = articleID

	if err := db.UpdateBodyUA(ctx, database, article.ID, rewritten, rewriteProvider); err != nil {
		slog.Warn("update body_ua failed", "error", err)
	}

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		slog.Error("telegram bot init failed", "error", err)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	if err := telegram.PostArticle(bot, cfg.TelegramChannelID, article); err != nil {
		slog.Error("telegram post failed", "error", err, "article_id", article.ID)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	if err := db.MarkPosted(ctx, database, article.ID); err != nil {
		slog.Warn("mark posted failed", "error", err)
	}
	posted = true
	logStage("telegram_post", stageStart, runStart)

	logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
}

func buildSelectorPrompt(candidates []candidate) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Here are %d news headlines. Return only the number of the single most interesting one for a Nintendo-focused Ukrainian Telegram channel. Return just the number, nothing else.\n\n", len(candidates)))
	for i, c := range candidates {
		desc := strings.ReplaceAll(c.item.Description, "\n", " ")
		desc = strings.TrimSpace(desc)
		runes := []rune(desc)
		if len(runes) > 240 {
			desc = string(runes[:240]) + "..."
		}
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(") ")
		b.WriteString(c.item.Title)
		if desc != "" {
			b.WriteString("\n")
			b.WriteString(desc)
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

var numberRe = regexp.MustCompile(`\d+`)

func parseSelectedIndex(raw string, count int) (int, bool) {
	match := numberRe.FindString(raw)
	if match == "" {
		return 0, false
	}
	n, err := strconv.Atoi(match)
	if err != nil || n < 1 || n > count {
		return 0, false
	}
	return n - 1, true
}

func logStage(name string, stageStart, runStart time.Time) {
	slog.Info("stage complete",
		"stage", name,
		"stage_duration_ms", time.Since(stageStart).Milliseconds(),
		"since_start_ms", time.Since(runStart).Milliseconds(),
	)
}

func logFinalStats(fetched, filtered int, aiSelectorUsed, aiRewriteUsed, posted bool, aiCallsUsed, aiRetries, aiCallsBudget int, runStart time.Time) {
	slog.Info("run complete",
		"fetched", fetched,
		"filtered", filtered,
		"ai_selector_used", aiSelectorUsed,
		"ai_rewrite_used", aiRewriteUsed,
		"ai_calls_used", aiCallsUsed,
		"ai_retries", aiRetries,
		"ai_calls_budget", aiCallsBudget,
		"posted", posted,
		"total_duration_ms", time.Since(runStart).Milliseconds(),
	)
}
