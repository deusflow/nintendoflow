package main

import (
	"context"
	"database/sql"
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

	"github.com/deuswork/nintendoflow/pkg/ai"
	"github.com/deuswork/nintendoflow/pkg/config"
	"github.com/deuswork/nintendoflow/pkg/db"
	"github.com/deuswork/nintendoflow/pkg/dedup"
	"github.com/deuswork/nintendoflow/pkg/fetcher"
	"github.com/deuswork/nintendoflow/pkg/scorer"
	"github.com/deuswork/nintendoflow/pkg/telegram"
)

const (
	maxAICallsPerRun           = 4
	aiCallDelay                = 20 * time.Second
	defaultPlaceholdersBaseURL = "https://deusflow.github.io/nintendoflow/assets/placeholders"
	aggregatorFreshnessHours   = 12
	mediaFreshnessHours        = 24
	insiderFreshnessHours      = 36
	maxAgePenaltyPercent       = 60
)

type candidate struct {
	item                fetcher.Item
	score               int
	rankScore           int
	techScore           int
	weirdnessScore      int
	mustPublish         bool
	eventTag            string
	recentSimilarPosted bool
	urlHash             string
	titleHash           string
}

const feedPriorityRankingScale = 2

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
		if err := db.Cleanup(ctx, database); err != nil {
			slog.Warn("cleanup failed", "error", err)
		} else {
			slog.Info("db cleanup: removed articles older than 30 days")
		}
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

	// -- 4.5. Check command-line mode ------------------------------------
	mode := "news"
	for _, arg := range os.Args[1:] {
		if arg == "highlight" || arg == "--mode=highlight" {
			mode = "highlight"
		}
	}
	if mode == "highlight" {
		runHighlightMode(ctx, database, manager, cfg)
		logFinalStats(0, 0, false, true, false, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	// -- 5. Fetch RSS (parallel, 30s timeout) ------------------------------
	fetchCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()

	items := fetcher.FetchAll(fetchCtx, feeds)
	fetchedCount = len(items)
	slog.Info("fetched articles", "count", len(items))
	logStage("fetch", stageStart, runStart)
	stageStart = time.Now()

	// -- 6. Local filtering only (freshness + DB dedup hashes + score) ----
	candidates := make([]candidate, 0, len(items))
	dedupHours := maxInt(cfg.RecentTitlesHours, maxInt(aggregatorFreshnessHours, maxInt(mediaFreshnessHours, insiderFreshnessHours)))

	knownURLs, err := db.FetchRecentURLHashes(ctx, database, dedupHours)
	if err != nil {
		slog.Warn("fetch recent url hashes failed", "error", err)
		knownURLs = make(map[string]struct{})
	}
	knownTitles, err := db.FetchRecentTitleHashes(ctx, database, dedupHours)
	if err != nil {
		slog.Warn("fetch recent title hashes failed", "error", err)
		knownTitles = make(map[string]struct{})
	}
	recentDedupTexts, err := db.FetchRecentDedupTexts(ctx, database, dedupHours)
	if err != nil {
		slog.Warn("fetch recent dedup texts failed", "error", err)
		recentDedupTexts = nil
	}
	for i := range recentDedupTexts {
		recentDedupTexts[i] = dedup.FingerprintText(recentDedupTexts[i])
	}

	for _, item := range items {
		sourceFreshnessHours := freshnessHoursForSourceType(item.SourceType, cfg.RecentTitlesHours)
		sourceCutoff := time.Now().Add(-time.Duration(sourceFreshnessHours) * time.Hour)
		if item.PublishedAt == nil || item.PublishedAt.Before(sourceCutoff) {
			continue
		}

		urlHash := dedup.HashURL(item.Link)
		titleHash := dedup.HashTitle(item.Title)
		semanticSig := dedup.SemanticSignature(item.Title)

		if _, exists := knownURLs[urlHash]; exists {
			continue
		}
		if _, exists := knownTitles[titleHash]; exists {
			slog.Debug("candidate duplicate title hash (semantic)", "signature", semanticSig, "title", item.Title)
			continue
		}

		candidateText := dedup.BuildSimilarityText(item.Title, item.Description)
		if candidateText != "" {
			threshold := dedup.ThresholdForSourceType(item.SourceType)
			if dedup.IsNearDuplicate(candidateText, recentDedupTexts, threshold) {
				slog.Debug("candidate filtered out", "reason", "near_duplicate", "source_type", item.SourceType, "title", item.Title)
				continue
			}
		}

		effectiveMinScore := cfg.MinScore
		if item.SourcePriority > 0 {
			// Lower priority -> higher min score requirement. Higher priority -> lower min score requirement.
			// e.g. Priority 50 adds +25 to requirement. Priority 120 reduces requirement by 10.
			effectiveMinScore += (100 - item.SourcePriority) / 2
		}

		// Score + Nintendo relevance gate
		result, ok, reason := scorer.ShouldPost(item.Title, item.Description, topics, effectiveMinScore, item.RequireAnchor)
		if !ok {
			slog.Debug("candidate filtered out", "reason", reason, "title", item.Title)
			continue
		}

		var recentSimilar bool
		if result.EventTag != "" {
			recentSimilar, _ = db.WasEventPostedRecently(ctx, database, result.EventTag, 6)
		}

		rankScore := candidateRankingScore(result.Score, item.SourcePriority, item.PublishedAt, item.SourceType, cfg.RecentTitlesHours)
		if recentSimilar {
			rankScore -= 50
		}

		candidates = append(candidates, candidate{
			item:                item,
			score:               result.Score,
			rankScore:           rankScore,
			techScore:           result.TechScore,
			weirdnessScore:      result.WeirdnessScore,
			mustPublish:         result.MustPublish,
			eventTag:            result.EventTag,
			recentSimilarPosted: recentSimilar,
			urlHash:             urlHash,
			titleHash:           titleHash,
		})
		if result.MustPublish {
			slog.Info("MUST-PUBLISH event detected", "title", item.Title, "score", result.Score)
		}
		slog.Debug("candidate accepted for AI", "title", item.Title, "score", result.Score, "signature", semanticSig)
		knownURLs[urlHash] = struct{}{}
		knownTitles[titleHash] = struct{}{}
		if candidateText != "" {
			recentDedupTexts = append(recentDedupTexts, candidateText)
		}
	}
	filteredCount = len(candidates)
	logStage("local_filter", stageStart, runStart)
	stageStart = time.Now()

	if len(candidates) < 1 {
		slog.Info("no candidates this run")
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	sortCandidates(candidates)

	topN := 5
	if len(candidates) < topN {
		topN = len(candidates)
	}
	topCandidates := candidates[:topN]
	checkedTop, dateChecked, dateDropped, dateUnknown := validateTopCandidatesFreshness(ctx, topCandidates, cfg.RecentTitlesHours)
	sortCandidates(checkedTop)
	slog.Info("source-date freshness check complete",
		"checked", dateChecked,
		"dropped_stale", dateDropped,
		"unknown_source_date", dateUnknown,
		"remaining", len(checkedTop),
	)
	logStage("source_date_check", stageStart, runStart)
	stageStart = time.Now()

	if len(checkedTop) < 1 {
		slog.Info("no candidates this run")
		logFinalStats(fetchedCount, 0, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}
	topCandidates = checkedTop

	selectionPrompt := buildSelectorPrompt(topCandidates)

	var selectedIdx int
	aiSelectorUsed = true
	rawSelection, err := manager.Generate(ctx, selectionPrompt)
	selectedIdx = 0
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

	// Step 6.5: fetch preferred media only for the single winning article.
	selected.item.VideoURL = ""
	selected.item.ImageURL = ""
	imageCtx, imageCancel := context.WithTimeout(ctx, 10*time.Second)
	videoURL, imageURL, imageErr := fetcher.FetchPreferredMedia(imageCtx, selected.item.Link)
	imageCancel()
	if imageErr != nil {
		slog.Warn("media fetch failed, fallback to no media", "url", selected.item.Link, "error", imageErr)
	} else if videoURL != "" {
		selected.item.VideoURL = videoURL
		slog.Info("youtube video extracted", "url", selected.item.Link, "video_url", videoURL)
	} else if imageURL == "" {
		slog.Info("no preferred media found, fallback to text-only", "url", selected.item.Link)
	} else {
		selected.item.ImageURL = imageURL
		slog.Info("og:image extracted", "url", selected.item.Link)
	}
	logStage("image_fetch", stageStart, runStart)
	stageStart = time.Now()

	// Step 6.6: fetch article content for the winning article.
	var articleBody string
	contentCtx, contentCancel := context.WithTimeout(ctx, 10*time.Second)
	content, contentErr := fetcher.FetchArticleContent(contentCtx, selected.item.Link)
	contentCancel()
	if contentErr != nil {
		slog.Warn("article content fetch failed, using RSS description", "url", selected.item.Link, "error", contentErr)
		articleBody = selected.item.Description
	} else if content != "" {
		articleBody = content
		slog.Info("article content fetched", "url", selected.item.Link, "chars", len(content))
	} else {
		articleBody = selected.item.Description
		slog.Info("article content empty, using RSS description", "url", selected.item.Link)
	}
	logStage("content_fetch", stageStart, runStart)
	stageStart = time.Now()

	rewritePrompt := ai.BuildPrompt(ai.NewsInput{
		Title:  selected.item.Title,
		Body:   articleBody,
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
	articleType, cleanBody := ai.ParseTypedPost(rewritten)
	if cleanBody == "" {
		cleanBody = rewritten
	}
	cleanBody = sanitizeGeneratedBody(cleanBody)

	hypeCount := calculateHype(selected.item, items)
	var hypeText string
	if hypeCount > 3 {
		hypeText = fmt.Sprintf("\n\n🔥 <i>Цю подію обговорюють у %d інших джерелах</i>", hypeCount)
	} else if hypeCount == 1 && isExclusiveWorthy(selected, articleType) {
		hypeText = "\n\n💎 <i>Ексклюзив (знайдено лише тут)</i>"
	} else if hypeCount > 1 {
		hypeText = fmt.Sprintf("\n\n🔥 <i>Знайдено у %d джерелах</i>", hypeCount)
	}
	cleanBody += hypeText

	logStage("ai_rewrite", stageStart, runStart)
	stageStart = time.Now()

	if selected.item.VideoURL == "" && selected.item.ImageURL == "" {
		selected.item.ImageURL = fallbackImageForType(articleType)
		slog.Info("fallback image selected",
			"article_type", articleType,
			"image_url", selected.item.ImageURL,
		)
	}

	article := db.Article{
		SourceURL:   selected.item.Link,
		URLHash:     selected.urlHash,
		TitleHash:   selected.titleHash,
		ContentHash: selected.item.ContentHash,
		TitleRaw:    selected.item.Title,
		BodyUA:      cleanBody,
		VideoURL:    selected.item.VideoURL,
		ImageURL:    selected.item.ImageURL,
		SourceName:  selected.item.SourceName,
		SourceType:  selected.item.SourceType,
		ArticleType: articleType,
		Score:       selected.score,
		Status:      db.StatusPending,
		PublishedAt: selected.item.PublishedAt,
	}

	if cfg.TestModerationMode {
		articleID, err := db.InsertArticle(ctx, database, article)
		if err != nil {
			slog.Error("insert pending article failed", "error", err)
			logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
			return
		}
		article.ID = articleID

		if err := db.UpdateBodyUA(ctx, database, article.ID, cleanBody, rewriteProvider); err != nil {
			slog.Warn("update body_ua failed", "error", err)
		}

		testBot, err := tgbotapi.NewBotAPI(cfg.TestTelegramToken)
		if err != nil {
			slog.Error("test telegram bot init failed", "error", err)
			logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
			return
		}

		previewChatID := cfg.TestAdminChatID
		if strings.TrimSpace(previewChatID) == "" {
			previewChatID = cfg.TestChannelID
		}

		previewMessageID, err := telegram.SendModerationPreview(testBot, previewChatID, article)
		if err != nil {
			slog.Error("send moderation preview failed", "error", err, "article_id", article.ID)
			logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
			return
		}

		slog.Info("test moderation preview sent",
			"article_id", article.ID,
			"preview_chat_id", previewChatID,
			"preview_message_id", previewMessageID,
		)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
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

	if err := db.UpdateBodyUA(ctx, database, article.ID, cleanBody, rewriteProvider); err != nil {
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
	b.WriteString("Choose the best news candidate for a Ukrainian Nintendo Telegram channel.\n")
	b.WriteString("Here is the ranked list of candidates (already scored by internal logic):\n\n")

	for i, c := range candidates {
		desc := strings.ReplaceAll(c.item.Description, "\n", " ")
		desc = strings.TrimSpace(desc)
		runes := []rune(desc)
		if len(runes) > 240 {
			desc = string(runes[:240]) + "..."
		}
		
		recentFlag := ""
		if c.recentSimilarPosted {
			recentFlag = ", RECENT_SIMILAR_POSTED: True"
		}
		fmt.Fprintf(&b, "Candidate #%d [score: %d, type: %s%s]\n", i+1, c.score, c.item.SourceType, recentFlag)
		fmt.Fprintf(&b, "Title: %s\n", c.item.Title)
		if desc != "" {
			fmt.Fprintf(&b, "Body: %s\n", desc)
		}
		b.WriteString("\n")
	}

	b.WriteString(`Instructions:
1. Select the most interesting, fresh, and engaging candidate.
2. If a candidate has "RECENT_SIMILAR_POSTED: True", strictly penalize it UNLESS it contains genuinely new and massive information (e.g., specific highly anticipated game reveals).
3. Return ONLY the number of the best candidate (e.g., 1 or 2).
4. If all candidates are weak, repetitive, or lack substance, return "SKIP" instead of a number.`)
	return b.String()
}

var numberRe = regexp.MustCompile(`\d+`)

var fillerPhraseReplacer = strings.NewReplacer(
	"Час покаже", "",
	"час покаже", "",
	"Подивимось", "",
	"подивимось", "",
	"Побачимо", "",
	"побачимо", "",
)

var emptyQuestionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)чи\s+чекаєте\s+ви[^?.!]*[?.!]`),
	regexp.MustCompile(`(?i)чи\s+стане\s+це\s+хітом[^?.!]*[?.!]`),
}

var excessiveNewlinesRe = regexp.MustCompile(`\n{3,}`)

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

func sanitizeGeneratedBody(body string) string {
	clean := fillerPhraseReplacer.Replace(body)
	for _, re := range emptyQuestionPatterns {
		clean = re.ReplaceAllString(clean, "")
	}
	clean = strings.ReplaceAll(clean, " .", ".")
	clean = excessiveNewlinesRe.ReplaceAllString(clean, "\n\n")
	return strings.TrimSpace(clean)
}

func isExclusiveWorthy(c candidate, articleType string) bool {
	if c.score < 170 {
		return false
	}
	sourceType := strings.ToLower(strings.TrimSpace(c.item.SourceType))
	if sourceType == "aggregator" {
		return false
	}
	t := ai.NormalizeArticleType(articleType)
	if t == ai.ArticleTypeRumor || t == ai.ArticleTypeOfftop {
		return false
	}
	return true
}

func fallbackImageForType(articleType string) string {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("PLACEHOLDER_BASE_URL")), "/")
	if base == "" {
		base = defaultPlaceholdersBaseURL
	}

	fileName := "newstwo-fallback-16x9.webp"
	switch ai.NormalizeArticleType(articleType) {
	case ai.ArticleTypeInsight:
		fileName = "news-fallback-16x9.webp"
	case ai.ArticleTypeRumor:
		fileName = "card-fallback-16x9.webp"
	case ai.ArticleTypeOfftop:
		fileName = "offtop-fallback-16x9.webp"
	case ai.ArticleTypeNews:
		fileName = "newstwo-fallback-16x9.webp"
	}

	return base + "/" + fileName
}

func runHighlightMode(ctx context.Context, database *sql.DB, manager *ai.Manager, cfg *config.Config) {
	slog.Info("starting in highlight mode")

	// 1. Fetch already posted games
	previousGames, err := db.FetchRecentlyHighlightedGames(ctx, database)
	if err != nil {
		slog.Error("failed to fetch previously highlighted games", "error", err)
		previousGames = []string{}
	}

	// 2. Build prompt for Gemini
	exclusionList := strings.Join(previousGames, ", ")
	if exclusionList == "" {
		exclusionList = "None"
	}

	prompt := fmt.Sprintf(`Choose a legendary Nintendo game with Metacritic 85+ (Switch, Wii U, 3DS, Wii, DS, GameCube, N64, SNES, or NES) that is NOT in this exclusion list: [%s].

Write an engaging, emotional storytelling post in Ukrainian about this game, acting as a passionate gaming historian. 
Tell it as a compelling story rather than a dry list of facts:
1. The human drama/creative struggle during creation (risks, prototypes, genius of the creators like Miyamoto, Aonuma, etc.).
2. The gameplay magic, art, and music that touched players' hearts.
3. Why this specific game is a timeless masterpiece and how it earned its status and high scores.

Style and formatting rules:
- Factual and accurate history only (no made-up rumors or fake facts).
- High emotional connection and passion in writing (make the reader want to play or replay it immediately).
- Catchy title (with game name, platform, release year, and Metacritic score).
- Do NOT use dry bullet points if they ruin the flow; instead use clean, readable paragraphs with bold key phrases.
- Max 1-2 thematic emojis.
- Keep it under 1400 characters to fit Telegram limits.
- The very first line of the output MUST strictly be in this format: 'GAME: [Exact English Game Name]'. Example:
GAME: The Legend of Zelda: Breath of the Wild
`, exclusionList)

	rewritten, err := manager.Generate(ctx, prompt)
	if err != nil {
		slog.Error("AI highlight generation failed", "error", err)
		return
	}

	// 3. Parse AI response
	lines := strings.Split(rewritten, "\n")
	gameTitle := "Legendary Nintendo Game"
	cleanBodyLines := []string{}
	for _, line := range lines {
		if strings.HasPrefix(line, "GAME:") {
			gameTitle = strings.TrimSpace(strings.TrimPrefix(line, "GAME:"))
			continue
		}
		cleanBodyLines = append(cleanBodyLines, line)
	}
	cleanBody := strings.TrimSpace(strings.Join(cleanBodyLines, "\n"))

	// 4. Create and insert article
	now := time.Now()
	article := db.Article{
		SourceURL:   "https://www.nintendo.com/masterpiece/" + urlSafeName(gameTitle),
		URLHash:     dedup.HashURL("https://www.nintendo.com/masterpiece/" + urlSafeName(gameTitle)),
		TitleHash:   dedup.HashTitle(gameTitle + " Highlight"),
		ContentHash: dedup.HashTitle(cleanBody),
		TitleRaw:    gameTitle,
		BodyUA:      cleanBody,
		SourceName:  "Nintendo Masterpiece",
		SourceType:  "highlight",
		ArticleType: "insight",
		Score:       99,
		Status:      db.StatusPending,
		PublishedAt: &now,
	}

	articleID, err := db.InsertArticle(ctx, database, article)
	if err != nil {
		slog.Error("insert highlight article failed", "error", err)
		return
	}
	article.ID = articleID

	// 5. Send moderation preview
	testBot, err := tgbotapi.NewBotAPI(cfg.TestTelegramToken)
	if err != nil {
		slog.Error("telegram bot init failed", "error", err)
		return
	}

	previewChatID := cfg.TestAdminChatID
	if strings.TrimSpace(previewChatID) == "" {
		previewChatID = cfg.TestChannelID
	}

	previewMessageID, err := telegram.SendModerationPreview(testBot, previewChatID, article)
	if err != nil {
		slog.Error("send highlight moderation preview failed", "error", err, "article_id", article.ID)
		return
	}

	slog.Info("highlight moderation preview sent", "article_id", article.ID, "preview_message_id", previewMessageID)
}

func urlSafeName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func logStage(name string, stageStart, runStart time.Time) {
	slog.Info("stage complete",
		"stage", name,
		"stage_duration_ms", time.Since(stageStart).Milliseconds(),
		"since_start_ms", time.Since(runStart).Milliseconds(),
	)
}

func candidateRankingScore(contentScore, sourcePriority int, publishedAt *time.Time, sourceType string, defaultHours int) int {
	if sourcePriority <= 0 {
		sourcePriority = 100
	}
	base := contentScore * (100 + sourcePriority/feedPriorityRankingScale) / 100
	if publishedAt == nil {
		return base
	}

	windowHours := freshnessHoursForSourceType(sourceType, defaultHours)
	if windowHours <= 0 {
		return base
	}

	ageHours := time.Since(*publishedAt).Hours()
	if ageHours <= 0 {
		return base
	}

	ratio := ageHours / float64(windowHours)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	penaltyPercent := int(ratio * maxAgePenaltyPercent)
	if penaltyPercent <= 0 {
		return base
	}

	adjusted := base * (100 - penaltyPercent) / 100
	if adjusted < 1 {
		return 1
	}
	return adjusted
}

func sortCandidates(candidates []candidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rankScore == candidates[j].rankScore {
			if candidates[i].score != candidates[j].score {
				return candidates[i].score > candidates[j].score
			}
			if candidates[i].item.SourcePriority != candidates[j].item.SourcePriority {
				return candidates[i].item.SourcePriority > candidates[j].item.SourcePriority
			}
			return candidates[i].item.PublishedAt.After(*candidates[j].item.PublishedAt)
		}
		return candidates[i].rankScore > candidates[j].rankScore
	})
}

func calculateHype(selectedItem fetcher.Item, allItems []fetcher.Item) int {
	hypeCount := 1
	hypeSources := make(map[string]struct{})
	hypeSources[selectedItem.SourceName] = struct{}{}

	selectedSig := dedup.SemanticSignature(selectedItem.Title)
	selectedFingerprint := dedup.BuildSimilarityText(selectedItem.Title, selectedItem.Description)

	for _, it := range allItems {
		if it.Link == selectedItem.Link {
			continue
		}
		if _, exists := hypeSources[it.SourceName]; exists {
			continue
		}

		match := false
		if dedup.SemanticSignature(it.Title) == selectedSig {
			match = true
		} else if selectedFingerprint != "" {
			itFingerprint := dedup.BuildSimilarityText(it.Title, it.Description)
			if itFingerprint != "" && dedup.Similarity(selectedFingerprint, itFingerprint) >= 0.60 {
				match = true
			}
		}

		if match {
			hypeCount++
			hypeSources[it.SourceName] = struct{}{}
		}
	}

	return hypeCount
}

func validateTopCandidatesFreshness(ctx context.Context, topCandidates []candidate, defaultHours int) ([]candidate, int, int, int) {
	validated := make([]candidate, 0, len(topCandidates))
	checked := 0
	dropped := 0
	unknown := 0

	for _, c := range topCandidates {
		checked++
		dateCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		sourcePublishedAt, err := fetcher.FetchSourcePublishedAt(dateCtx, c.item.Link)
		cancel()
		if err != nil {
			unknown++
			slog.Warn("source-date check failed; keeping candidate with feed date",
				"title", c.item.Title,
				"source", c.item.SourceName,
				"url", c.item.Link,
				"error", err,
			)
			validated = append(validated, c)
			continue
		}

		if sourcePublishedAt == nil {
			unknown++
			slog.Debug("source-date missing; keeping candidate with feed date",
				"title", c.item.Title,
				"source", c.item.SourceName,
				"url", c.item.Link,
			)
			validated = append(validated, c)
			continue
		}

		ageHours := int(time.Since(*sourcePublishedAt).Hours())
		sourceFreshnessHours := freshnessHoursForSourceType(c.item.SourceType, defaultHours)
		sourceCutoff := time.Now().Add(-time.Duration(sourceFreshnessHours) * time.Hour)
		if sourcePublishedAt.Before(sourceCutoff) {
			dropped++
			slog.Info("candidate dropped by source-date freshness",
				"title", c.item.Title,
				"source", c.item.SourceName,
				"url", c.item.Link,
				"source_published_at", sourcePublishedAt.UTC().Format(time.RFC3339),
				"age_hours", ageHours,
				"freshness_hours", sourceFreshnessHours,
			)
			continue
		}

		c.item.SourcePublishedAt = sourcePublishedAt
		c.item.PublishedAt = sourcePublishedAt
		c.rankScore = candidateRankingScore(c.score, c.item.SourcePriority, c.item.PublishedAt, c.item.SourceType, defaultHours)
		validated = append(validated, c)
	}

	return validated, checked, dropped, unknown
}

func freshnessHoursForSourceType(sourceType string, fallback int) int {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "aggregator":
		return aggregatorFreshnessHours
	case "media":
		return mediaFreshnessHours
	case "insider":
		return insiderFreshnessHours
	default:
		if fallback > 0 {
			return fallback
		}
		return mediaFreshnessHours
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
