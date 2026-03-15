package main

import (
	"context"
	"errors"
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
	keywords, err := config.LoadKeywords(cfg.KeywordsPath)
	if err != nil {
		slog.Error("keywords load failed", "path", cfg.KeywordsPath, "error", err)
		os.Exit(1)
	}
	config.LogConfigLoaded(len(feeds), len(keywords))
	logStage("config", stageStart, runStart)
	stageStart = time.Now()

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
	logStage("db", stageStart, runStart)
	stageStart = time.Now()

	ctx := context.Background()

	// -- 4. Init single AI provider (strict sequential, max 2 calls/run) --
	geminiProvider, err := ai.NewGeminiProvider(ctx, cfg.GeminiAPIKey)
	if err != nil {
		slog.Error("gemini init failed", "error", err)
		os.Exit(1)
	}
	provider := ai.AIProvider(geminiProvider)
	slog.Info("AI provider selected", "provider", provider.Name())
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
	cutoff := time.Now().Add(-48 * time.Hour)
	candidates := make([]candidate, 0, len(items))

	for _, item := range items {
		if item.PublishedAt == nil || item.PublishedAt.Before(cutoff) {
			continue
		}

		urlHash := dedup.HashURL(item.Link)
		titleHash := dedup.HashTitle(item.Title)

		exists, err := db.HashExists(database, urlHash, titleHash)
		if err != nil {
			slog.Warn("hash dedup check failed", "error", err)
			continue
		}
		if exists {
			continue
		}

		// Score + Nintendo relevance gate
		result, ok, reason := scorer.ShouldPost(item.Title, item.Description, keywords, cfg.MinScore, item.RequireAnchor)
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
	}
	filteredCount = len(candidates)
	logStage("local_filter", stageStart, runStart)
	stageStart = time.Now()

	if len(candidates) < 1 {
		slog.Info("no candidates this run")
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, runStart)
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

	time.Sleep(20 * time.Second)
	aiSelectorUsed = true
	rawSelection, err := provider.Complete(ctx, selectionPrompt)
	selectedIdx := 0
	if err != nil {
		slog.Warn("AI selector failed, using top-scored fallback", "error", err)
	} else {
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

	time.Sleep(20 * time.Second)
	aiRewriteUsed = true
	rewritten, err := provider.Rewrite(ctx, selected.item.Title, selected.item.Description, selected.item.SourceName)
	if errors.Is(err, ai.ErrSkipped) {
		slog.Info("AI skipped selected candidate", "title", selected.item.Title)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, runStart)
		return
	}
	if err != nil {
		slog.Error("AI rewrite failed", "error", err)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, runStart)
		return
	}
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

	articleID, err := db.InsertArticle(database, article)
	if err != nil {
		slog.Error("insert selected article failed", "error", err)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, runStart)
		return
	}
	article.ID = articleID

	if err := db.UpdateBodyUA(database, article.ID, rewritten, provider.Name()); err != nil {
		slog.Warn("update body_ua failed", "error", err)
	}

	if cfg.DryRun {
		slog.Info("DRY_RUN - would post selected article", "title", article.TitleRaw, "provider", provider.Name())
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, runStart)
		return
	}

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		slog.Error("telegram bot init failed", "error", err)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, runStart)
		return
	}

	if err := telegram.PostArticle(bot, cfg.TelegramChannelID, article); err != nil {
		slog.Error("telegram post failed", "error", err, "article_id", article.ID)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, runStart)
		return
	}

	if err := db.MarkPosted(database, article.ID); err != nil {
		slog.Warn("mark posted failed", "error", err)
	}
	posted = true
	logStage("telegram_post", stageStart, runStart)

	logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, runStart)
}

func buildSelectorPrompt(candidates []candidate) string {
	var b strings.Builder
	b.WriteString("Here are 5 news headlines. Return only the number of the single most interesting one for a Nintendo-focused Ukrainian Telegram channel. Return just the number, nothing else.\n\n")
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

func logFinalStats(fetched, filtered int, aiSelectorUsed, aiRewriteUsed, posted bool, runStart time.Time) {
	slog.Info("run complete",
		"fetched", fetched,
		"filtered", filtered,
		"ai_selector_used", aiSelectorUsed,
		"ai_rewrite_used", aiRewriteUsed,
		"posted", posted,
		"total_duration_ms", time.Since(runStart).Milliseconds(),
	)
}
