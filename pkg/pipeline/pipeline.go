package pipeline

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
	"github.com/deuswork/nintendoflow/pkg/ai/embedding"
	"github.com/deuswork/nintendoflow/pkg/config"
	"github.com/deuswork/nintendoflow/pkg/db"
	"github.com/deuswork/nintendoflow/pkg/dedup"
	"github.com/deuswork/nintendoflow/pkg/fetcher"
	"github.com/deuswork/nintendoflow/pkg/scorer"
	"github.com/deuswork/nintendoflow/pkg/telegram"
)

const (
	defaultPlaceholdersBaseURL = "https://deusflow.github.io/nintendoflow/assets/placeholders"
	aggregatorFreshnessHours   = 24
	mediaFreshnessHours        = 36
	insiderFreshnessHours      = 48
	maxAgePenaltyPercent       = 60
	defaultDedupHours          = 30 * 24 // 30 days window for deduplication
)

type candidate struct {
	item                fetcher.Item
	score               int
	rankScore           int
	techScore           int
	impactScore         int
	weirdnessScore      int
	mustPublish         bool
	eventTag            string
	category            scorer.Category
	franchiseTag        string
	hypeCount           int
	rescuedByTriage     bool
	scrapedBody         string
	isPaywalled         bool
	paywallReason       string
	recentSimilarPosted bool
	urlHash             string
	titleHash           string
}

const feedPriorityRankingScale = 2

func Run(ctx context.Context, cfg *config.Config, database *sql.DB, manager *ai.Manager, feeds []config.Feed, topics map[string]config.Topic) {
	runStart := time.Now()
	stageStart := runStart

	var (
		fetchedCount   int
		filteredCount  int
		aiSelectorUsed bool
		aiRewriteUsed  bool
		posted         bool
	)

	// -- 5. Fetch RSS (parallel, lazy redirect resolution keeps FetchAll fast) --
	fetchCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()

	items, feedStatuses := fetcher.FetchAllDetailed(fetchCtx, feeds)
	fetchedCount = len(items)
	slog.Info("fetched articles", "count", len(items))
	if !cfg.DryRun && database != nil {
		monitorFeedHealth(ctx, database, cfg, feedStatuses)
	}
	logStage("fetch", stageStart, runStart)
	stageStart = time.Now()

	// -- 6. Local filtering + Semantic history loading -----------------------
	candidates := make([]candidate, 0, len(items))
	triagePool := make([]triageCandidate, 0, 25)
	dedupHours := maxInt(cfg.RecentTitlesHours, defaultDedupHours)

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
	recentPublishedTitles, err := db.FetchRecentPublishedTitles(ctx, database, dedupHours)
	if err != nil {
		slog.Warn("fetch recent published titles failed", "error", err)
	}
	recentStories, err := db.FetchRecentStories(ctx, database, dedupHours)
	if err != nil {
		slog.Warn("fetch recent stories for semantic dedup failed", "error", err)
	}
	recentCategories, err := db.FetchRecentCategoryHistory(ctx, database, 4)
	if err != nil {
		slog.Warn("fetch recent category history failed", "error", err)
	}
	recentFranchises, err := db.FetchRecentFranchiseCounts(ctx, database, 72)
	if err != nil {
		slog.Warn("fetch recent franchise counts failed", "error", err)
	}

	seenTitleNorms := make(map[string]struct{}, len(recentStories))
	for _, s := range recentStories {
		if s.TitleNorm != "" {
			seenTitleNorms[s.TitleNorm] = struct{}{}
		}
	}

	for _, item := range items {
		sourceFreshnessHours := freshnessHoursForSourceType(item.SourceType, cfg.RecentTitlesHours)
		sourceCutoff := time.Now().Add(-time.Duration(sourceFreshnessHours) * time.Hour)
		if item.PublishedAt == nil || item.PublishedAt.Before(sourceCutoff) {
			continue
		}

		// Drop obvious routine noise (NSO profile icons, Nintendo Music track adds, charts, guides, reviews)
		if noisy, noiseReason := scorer.IsRoutineNoiseTitle(item.Title); noisy {
			slog.Debug("candidate dropped as routine noise", "reason", noiseReason, "title", item.Title)
			continue
		}

		cleanTitle := dedup.StripPublisherSuffix(item.Title)
		urlHash := dedup.HashURL(item.Link)
		titleHash := dedup.HashTitle(cleanTitle)
		titleNorm := dedup.NormalizeTitleForDedup(cleanTitle)
		semanticSig := dedup.SemanticSignature(cleanTitle)

		if _, exists := knownURLs[urlHash]; exists {
			continue
		}
		if _, exists := knownTitles[titleHash]; exists {
			slog.Debug("candidate duplicate title hash (semantic)", "signature", semanticSig, "title", item.Title)
			continue
		}
		if titleNorm != "" {
			if _, exists := seenTitleNorms[titleNorm]; exists {
				slog.Debug("candidate duplicate title_norm", "title_norm", titleNorm, "title", item.Title)
				continue
			}
		}

		candidateText := dedup.BuildSimilarityText(cleanTitle, item.Description)
		if candidateText != "" {
			threshold := dedup.ThresholdForSourceType(item.SourceType)
			if dedup.IsNearDuplicate(candidateText, recentDedupTexts, threshold) {
				slog.Debug("candidate filtered out", "reason", "near_duplicate", "source_type", item.SourceType, "title", item.Title)
				continue
			}
		}

		effectiveMinScore := cfg.MinScore
		if item.SourcePriority > 0 {
			effectiveMinScore += (100 - item.SourcePriority) / 2
		}

		result, ok, reason := scorer.ShouldPost(item.Title, item.Description, topics, effectiveMinScore, item.RequireAnchor)
		if !ok {
			if len([]rune(strings.TrimSpace(item.Title))) >= 24 &&
				(reason == "below_min_score" || item.SourcePriority >= 100 || result.TechScore >= 60) {
				triagePriority := result.Score + result.TechScore + item.SourcePriority
				if result.Category == scorer.CategoryTech || result.Category == scorer.CategoryCompany || result.Category == scorer.CategoryLegal {
					triagePriority += 40
				}
				triagePool = append(triagePool, triageCandidate{
					Item:      item,
					URLHash:   urlHash,
					TitleHash: titleHash,
					RawScore:  result.Score,
					TechScore: result.TechScore,
					Priority:  triagePriority,
				})
			}
			slog.Debug("candidate filtered out", "reason", reason, "title", item.Title)
			continue
		}

		var recentSimilar bool
		if result.EventTag != "" {
			recentSimilar, _ = db.WasEventPostedRecently(ctx, database, result.EventTag, 6)
		}

		rankScore := candidateRankingScore(result.Score, item.SourcePriority, item.PublishedAt, item.SourceType, cfg.RecentTitlesHours) + result.ImpactScore
		if recentSimilar {
			rankScore -= 50
		}

		candidates = append(candidates, candidate{
			item:                item,
			score:               result.Score,
			rankScore:           rankScore,
			techScore:           result.TechScore,
			impactScore:         result.ImpactScore,
			weirdnessScore:      result.WeirdnessScore,
			mustPublish:         result.MustPublish,
			eventTag:            result.EventTag,
			category:            result.Category,
			franchiseTag:        result.FranchiseTag,
			recentSimilarPosted: recentSimilar,
			urlHash:             urlHash,
			titleHash:           titleHash,
		})
		if result.MustPublish {
			slog.Info("MUST-PUBLISH event detected", "title", item.Title, "score", result.Score)
		}
		slog.Debug("candidate accepted for AI", "title", item.Title, "score", result.Score, "category", result.Category, "signature", semanticSig)
		knownURLs[urlHash] = struct{}{}
		knownTitles[titleHash] = struct{}{}
		if titleNorm != "" {
			seenTitleNorms[titleNorm] = struct{}{}
		}
		if candidateText != "" {
			recentDedupTexts = append(recentDedupTexts, candidateText)
		}
	}

	// -- 6b. Cheap AI Triage Rescue over missed headlines --------------------
	if len(triagePool) > 0 && manager != nil && manager.CallsUsed() < manager.CallsBudget() {
		prioritizedTriage := prioritizeTriagePool(triagePool, 25)
		rescuedIndices := runTriage(ctx, prioritizedTriage, manager)
		for _, idx := range rescuedIndices {
			tc := prioritizedTriage[idx]
			res := scorer.Evaluate(tc.Item.Title, tc.Item.Description, topics)
			score := maxInt(res.Score, cfg.MinScore+35)
			rankScore := candidateRankingScore(score, tc.Item.SourcePriority, tc.Item.PublishedAt, tc.Item.SourceType, cfg.RecentTitlesHours) + maxInt(res.ImpactScore, 28)
			candidates = append(candidates, candidate{
				item:            tc.Item,
				score:           score,
				rankScore:       rankScore,
				techScore:       res.TechScore,
				impactScore:     res.ImpactScore,
				weirdnessScore:  res.WeirdnessScore,
				category:        res.Category,
				franchiseTag:    res.FranchiseTag,
				rescuedByTriage: true,
				urlHash:         tc.URLHash,
				titleHash:       tc.TitleHash,
			})
		}
	}

	filteredCount = len(candidates)
	logStage("local_filter", stageStart, runStart)
	stageStart = time.Now()

	if len(candidates) < 1 {
		slog.Info("no candidates this run")
		if !cfg.DryRun && database != nil {
			monitorEmptyRuns(ctx, database, cfg, false)
		}
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	// Cross-source hype boost & category/franchise anti-fatigue before top-pool cut.
	candidates = applyCrossSourceBoost(candidates, items)
	candidates = applyCategoryAndFranchiseAntiFatigue(candidates, recentCategories, recentFranchises)
	sortCandidates(candidates)
	candidates = capCandidatesPerSource(candidates, 3)

	maxCandidatesToCheck := 12
	if len(candidates) < maxCandidatesToCheck {
		maxCandidatesToCheck = len(candidates)
	}
	candidatesPool := candidates[:maxCandidatesToCheck]

	// Resolve lazy Google News URLs on top pool and drop any URLs matching already-known articles.
	resolvedPool := make([]candidate, 0, len(candidatesPool))
	for _, c := range candidatesPool {
		if c.item.NeedsRedirectResolve && fetcher.IsGoogleNewsURL(c.item.Link) {
			resCtx, resCancel := context.WithTimeout(ctx, 8*time.Second)
			resolved, rErr := fetcher.ResolveRedirectWithContext(resCtx, c.item.Link)
			resCancel()
			if rErr == nil && resolved != "" && resolved != c.item.Link {
				c.item.Link = resolved
				newHash := dedup.HashURL(resolved)
				if _, dup := knownURLs[newHash]; dup {
					slog.Debug("resolved google news URL already known, skipping", "url", resolved)
					continue
				}
				c.urlHash = newHash
				knownURLs[newHash] = struct{}{}
			}
		}
		resolvedPool = append(resolvedPool, c)
	}

	checkedCandidates, dateChecked, dateDropped, quarantined, quarantineReasons := validateTopCandidatesFreshness(ctx, resolvedPool, cfg.RecentTitlesHours, cfg, database)
	quarantineCount := len(quarantined)

	// Re-apply anti-fatigue/hype after source-date adjustment so ranking remains consistent.
	checkedCandidates = applyCategoryAndFranchiseAntiFatigue(checkedCandidates, recentCategories, recentFranchises)
	sortCandidates(checkedCandidates)

	slog.Info("source-date freshness check complete",
		"checked", dateChecked,
		"dropped_stale", dateDropped,
		"quarantined", quarantineCount,
		"remaining", len(checkedCandidates),
	)
	logStage("source_date_check", stageStart, runStart)
	stageStart = time.Now()

	if quarantineCount >= 5 {
		handleQuarantineAlert(cfg, quarantined)
	}

	topN := 6
	if len(checkedCandidates) > topN {
		checkedCandidates = checkedCandidates[:topN]
	}

	if len(checkedCandidates) < 1 {
		if cfg.DryRun {
			fmt.Println("\n==================== DRY RUN REPORT ====================")
			fmt.Printf("1. QUARANTINED ARTICLES (%d total):\n", len(quarantined))
			if len(quarantined) == 0 {
				fmt.Println("   (none)")
			} else {
				for i, q := range quarantined {
					reason := quarantineReasons[q.item.Link]
					fmt.Printf("   [%d] %s\n       Source: %s | URL: %s\n       Reason: %s\n", i+1, q.item.Title, q.item.SourceName, q.item.Link, reason)
				}
			}
			fmt.Println("\n2. NO VALID CANDIDATES REMAINED AFTER FRESHNESS/QUARANTINE CHECKS.")
			fmt.Println("========================================================")
			fmt.Println()
		}
		slog.Info("no candidates this run")
		if !cfg.DryRun && database != nil {
			monitorEmptyRuns(ctx, database, cfg, false)
		}
		logFinalStats(fetchedCount, 0, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	// Pre-AI parallel article body scrape & paywall/stub filter on top pool.
	topCandidates := scrapeTopCandidates(ctx, checkedCandidates, topN)
	logStage("pre_ai_scrape", stageStart, runStart)
	stageStart = time.Now()

	selectionPrompt := buildSelectorPrompt(topCandidates, recentPublishedTitles)

	selectedIdx := 0
	aiSelectorUsed = true
	rawSelection, err := manager.Generate(ctx, selectionPrompt)
	if err != nil {
		slog.Warn("AI selector failed, using top-scored fallback", "error", err)
	} else {
		slog.Info("AI selector used", "provider", manager.LastProvider())
		if strings.Contains(strings.ToUpper(rawSelection), "SKIP") {
			slog.Info("AI selector decided to skip all candidates (duplicates or low quality)", "response", rawSelection)
			if cfg.DryRun {
				printDryRunSkipReport(quarantined, quarantineReasons, topCandidates)
			}
			if !cfg.DryRun && database != nil {
				monitorEmptyRuns(ctx, database, cfg, false)
			}
			logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
			return
		}
		idx, ok := parseSelectedIndex(rawSelection, len(topCandidates))
		if !ok {
			slog.Warn("AI selector returned invalid number, using top-scored fallback", "response", rawSelection)
		} else {
			selectedIdx = idx
		}
	}
	logStage("ai_selector", stageStart, runStart)
	stageStart = time.Now()

	// Build fallback candidate order starting with AI selector pick, then top alternatives.
	orderedIndices := make([]int, 0, len(topCandidates))
	orderedIndices = append(orderedIndices, selectedIdx)
	for i := range topCandidates {
		if i != selectedIdx {
			orderedIndices = append(orderedIndices, i)
		}
	}

	embedder := buildPipelineEmbedder()
	var (
		chosenIdx        int
		selectedArticle  db.Article
		chosenProvider   string
		chosenCleanBody  string
		accepted         bool
	)

	attempts := 0
	const maxCandidateAttempts = 2
	for _, cIdx := range orderedIndices {
		if attempts >= maxCandidateAttempts {
			break
		}
		if manager.CallsUsed() >= manager.CallsBudget() {
			slog.Warn("AI call budget exhausted before candidate rewrite")
			break
		}
		attempts++
		selected := topCandidates[cIdx]

		// Fetch media only for candidate under active rewrite attempt.
		rssVideoURL := selected.item.VideoURL
		rssImageURL := selected.item.ImageURL
		selected.item.VideoURL = ""
		selected.item.ImageURL = ""
		imageCtx, imageCancel := context.WithTimeout(ctx, 10*time.Second)
		videoURL, imageURL, imageErr := fetcher.FetchPreferredMedia(imageCtx, selected.item.Link)
		imageCancel()
		if videoURL == "" && rssVideoURL != "" {
			videoURL = rssVideoURL
		}
		if imageURL == "" && rssImageURL != "" {
			imageURL = rssImageURL
		}
		if imageErr != nil && videoURL == "" && imageURL == "" {
			slog.Warn("media fetch failed, fallback to no media", "url", selected.item.Link, "error", imageErr)
		}

		// Use already-scraped body from pre-AI scrape or fetch on demand.
		articleBody := selected.scrapedBody
		if articleBody == "" {
			contentCtx, contentCancel := context.WithTimeout(ctx, 10*time.Second)
			content, contentErr := fetcher.FetchArticleContent(contentCtx, selected.item.Link)
			contentCancel()
			if contentErr != nil {
				slog.Warn("article content fetch failed, using RSS description", "url", selected.item.Link, "error", contentErr)
				articleBody = selected.item.Description
			} else if content != "" {
				articleBody = content
			} else {
				articleBody = selected.item.Description
			}
		}

		contentHash := dedup.GenerateContentHashWithNumbers(selected.item.Title, articleBody)
		if contentHash != "" {
			if hashDup, _ := db.HasContentHash(ctx, database, contentHash, dedupHours); hashDup {
				slog.Info("candidate skipped by DB content_hash dedup, trying next", "title", selected.item.Title)
				continue
			}
		}

		pubStr := ""
		if selected.item.PublishedAt != nil {
			pubStr = selected.item.PublishedAt.UTC().Format("2006-01-02")
		}
		sourceForNumbers := selected.item.Title + "\n" + articleBody + "\n" + pubStr + "\n" + time.Now().Format("2006-01-02")

		var rewritten, rewriteProvider string
		if !cfg.DryRun && database != nil {
			if cachedJSON, cachedProv, hit, _ := db.GetCachedAITranslation(ctx, database, selected.urlHash, 72); hit {
				rewritten = cachedJSON
				rewriteProvider = cachedProv
				slog.Info("AI translation cache hit", "url", selected.item.Link, "provider", cachedProv)
			}
		}

		if rewritten == "" {
			rewritePrompt := ai.BuildPrompt(ai.NewsInput{
				Title:       selected.item.Title,
				Body:        articleBody,
				Source:      selected.item.SourceName,
				PublishedAt: selected.item.PublishedAt,
				CurrentTime: time.Now(),
			})
			aiRewriteUsed = true
			var genErr error
			rewritten, genErr = manager.GenerateValidated(ctx, rewritePrompt, func(raw string) error {
				parsed, err := ai.ParseJSONPost(raw)
				if err != nil {
					return fmt.Errorf("invalid post json: %w", err)
				}
				if parsed.Skip {
					return nil
				}
				if err := ai.VerifyUkrainian(parsed.TelegramHTML); err != nil {
					return fmt.Errorf("ukrainian validation failed: %w", err)
				}
				if err := ai.VerifyNumbers(sourceForNumbers, parsed.TelegramHTML); err != nil {
					return fmt.Errorf("number grounding failed: %w", err)
				}
				return nil
			})
			if errors.Is(genErr, ai.ErrSkipped) {
				slog.Info("AI skipped candidate, trying fallback", "title", selected.item.Title)
				continue
			}
			if genErr != nil {
				slog.Error("AI rewrite/validation failed for candidate, trying fallback", "title", selected.item.Title, "error", genErr)
				continue
			}
			rewriteProvider = manager.LastProvider()
			if !cfg.DryRun && database != nil {
				_ = db.SaveCachedAITranslation(ctx, database, selected.urlHash, rewritten, rewriteProvider)
			}
		}

		postData, parseErr := ai.ParseJSONPost(rewritten)
		if parseErr != nil {
			slog.Error("unexpected JSON parse error after validation", "error", parseErr)
			continue
		}
		if postData.Skip {
			slog.Info("AI explicitly skipped candidate via JSON, trying fallback", "title", selected.item.Title, "reason", postData.Reason)
			continue
		}

		finalCat := string(selected.category)
		if postData.Category != "" {
			finalCat = postData.Category
		}
		if gateOK, gateReason := scorer.PassesEditorialGate(selected.item.Title, scorer.Category(finalCat), postData.AudienceScore, postData.ConcreteAnchor); !gateOK {
			slog.Info("candidate failed post-AI editorial gate, trying fallback", "title", selected.item.Title, "reason", gateReason)
			continue
		}

		var emb []float32
		if embedder != nil {
			embCtx, embCancel := context.WithTimeout(ctx, 10*time.Second)
			vec, embErr := embedder.Embed(embCtx, selected.item.Title+"\n"+postData.ConcreteAnchor+"\n"+postData.StoryClusterKey)
			embCancel()
			if embErr == nil && len(vec) > 0 {
				emb = vec
			}
		}

		titleNorm := dedup.NormalizeTitleForDedup(selected.item.Title)
		if dup, dupReason := dedup.CheckSemanticDuplicate(selected.item.Title, titleNorm, contentHash, postData.StoryClusterKey, emb, recentStories); dup {
			slog.Info("candidate caught by semantic duplicate check, trying fallback", "title", selected.item.Title, "reason", dupReason)
			continue
		}

		cleanBody := sanitizeGeneratedBody(postData.TelegramHTML)
		threadsBody := dedup.StripForbiddenIntro(postData.ThreadsText)
		if selected.item.VideoURL == "" && selected.item.ImageURL == "" {
			selected.item.ImageURL = fallbackImageForType(postData.Type)
		}

		chosenIdx = cIdx
		chosenProvider = rewriteProvider
		chosenCleanBody = cleanBody
		selectedArticle = db.Article{
			SourceURL:       selected.item.Link,
			URLHash:         selected.urlHash,
			TitleHash:       selected.titleHash,
			ContentHash:     contentHash,
			TitleRaw:        selected.item.Title,
			BodyUA:          cleanBody,
			BodyThreads:     threadsBody,
			VideoURL:        selected.item.VideoURL,
			ImageURL:        selected.item.ImageURL,
			SourceName:      selected.item.SourceName,
			SourceType:      selected.item.SourceType,
			ArticleType:     postData.Type,
			Category:        finalCat,
			FranchiseTag:    selected.franchiseTag,
			TitleNorm:       titleNorm,
			StoryClusterKey: postData.StoryClusterKey,
			ConcreteAnchor:  postData.ConcreteAnchor,
			AudienceScore:   postData.AudienceScore,
			Embedding:       emb,
			Score:           selected.score,
			Status:          db.StatusPending,
			EventTag:        selected.eventTag,
			PublishedAt:     selected.item.PublishedAt,
		}
		accepted = true
		break
	}

	logStage("ai_rewrite", stageStart, runStart)
	if !accepted {
		slog.Info("no candidate cleared AI selection/verification/semantic-dedup checks")
		if !cfg.DryRun && database != nil {
			monitorEmptyRuns(ctx, database, cfg, false)
		}
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	if cfg.DryRun {
		fmt.Println("\n==================== DRY RUN REPORT ====================")
		fmt.Printf("1. QUARANTINED ARTICLES (%d total):\n", len(quarantined))
		if len(quarantined) == 0 {
			fmt.Println("   (none)")
		} else {
			for i, q := range quarantined {
				reason := quarantineReasons[q.item.Link]
				if reason == "" {
					reason = "source date missing or unreachable"
				}
				fmt.Printf("   [%d] %s\n       Source: %s | URL: %s\n       Reason: %s\n", i+1, q.item.Title, q.item.SourceName, q.item.Link, reason)
			}
		}

		fmt.Printf("\n2. CANDIDATES CONSIDERED BY SELECTOR (%d total):\n", len(topCandidates))
		for i, tc := range topCandidates {
			pubStr := "unknown"
			if tc.item.PublishedAt != nil {
				pubStr = tc.item.PublishedAt.UTC().Format(time.RFC3339)
			}
			fmt.Printf("   [%d] %s\n       Source: %s | Cat: %s | Hype: %d | Score: %d (rank %d) | Published: %s\n",
				i+1, tc.item.Title, tc.item.SourceName, tc.category, tc.hypeCount, tc.score, tc.rankScore, pubStr)
		}

		fmt.Printf("\n3. SELECTED CANDIDATE (Index %d):\n", chosenIdx+1)
		fmt.Printf("   Title:    %s\n", selectedArticle.TitleRaw)
		fmt.Printf("   Source:   %s\n", selectedArticle.SourceName)
		fmt.Printf("   Category: %s | Type: %s | Cluster: %s\n", selectedArticle.Category, selectedArticle.ArticleType, selectedArticle.StoryClusterKey)

		fmt.Println("\n4. WOULD PUBLISH TELEGRAM POST:")
		fmt.Println("--------------------------------------------------------")
		fmt.Println(chosenCleanBody)
		fmt.Println("--------------------------------------------------------")
		fmt.Println("========================================================")
		fmt.Println()

		slog.Info("DRY_RUN - would post selected article", "title", selectedArticle.TitleRaw, "provider", chosenProvider)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	articleID, err := db.InsertArticle(ctx, database, selectedArticle)
	if err != nil {
		slog.Error("insert pending article failed", "error", err)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}
	selectedArticle.ID = articleID

	if err := db.UpdateBodies(ctx, database, selectedArticle.ID, selectedArticle.BodyUA, selectedArticle.BodyThreads, chosenProvider); err != nil {
		slog.Warn("update bodies failed", "error", err)
	}

	botToken := cfg.TestTelegramToken
	if botToken == "" {
		botToken = cfg.TelegramBotToken
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		slog.Error("telegram bot init failed", "error", err)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	previewChatID := cfg.TestAdminChatID
	if strings.TrimSpace(previewChatID) == "" {
		previewChatID = cfg.TestChannelID
	}
	if strings.TrimSpace(previewChatID) == "" {
		previewChatID = cfg.TelegramChannelID
	}

	previewMessageID, err := telegram.SendModerationPreview(bot, previewChatID, selectedArticle)
	if err != nil {
		slog.Error("send moderation preview failed", "error", err, "article_id", selectedArticle.ID)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	if cachedVid := telegram.GetCachedPreviewVideoURL(selectedArticle.ID); cachedVid != "" {
		if err := db.UpdateVideoURL(ctx, database, selectedArticle.ID, cachedVid); err != nil {
			slog.Warn("failed to cache telegram video file_id in db", "article_id", selectedArticle.ID, "error", err)
		}
	}

	posted = true
	if database != nil {
		monitorEmptyRuns(ctx, database, cfg, true)
	}
	slog.Info("moderation preview sent",
		"article_id", selectedArticle.ID,
		"preview_chat_id", previewChatID,
		"preview_message_id", previewMessageID,
	)

	logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
}

func buildPipelineEmbedder() embedding.Embedder {
	var keys []string
	for _, envName := range []string{"GEMINI_API_KEY", "GEMINI_API_KEY_2", "GEMINI_API_KEY_3"} {
		if k := strings.TrimSpace(os.Getenv(envName)); k != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	emb, err := embedding.NewGeminiEmbedder(keys, embedding.DefaultEmbeddingModel)
	if err != nil {
		slog.Debug("gemini embedder disabled", "error", err)
		return nil
	}
	return emb
}

func printDryRunSkipReport(quarantined []candidate, quarantineReasons map[string]string, topCandidates []candidate) {
	fmt.Println("\n==================== DRY RUN REPORT ====================")
	fmt.Printf("1. QUARANTINED ARTICLES (%d total):\n", len(quarantined))
	if len(quarantined) == 0 {
		fmt.Println("   (none)")
	} else {
		for i, q := range quarantined {
			reason := quarantineReasons[q.item.Link]
			if reason == "" {
				reason = "source date missing or unreachable"
			}
			fmt.Printf("   [%d] %s\n       Source: %s | URL: %s\n       Reason: %s\n", i+1, q.item.Title, q.item.SourceName, q.item.Link, reason)
		}
	}

	fmt.Printf("\n2. CANDIDATES CONSIDERED BY SELECTOR (%d total):\n", len(topCandidates))
	for i, tc := range topCandidates {
		pubStr := "unknown"
		if tc.item.PublishedAt != nil {
			pubStr = tc.item.PublishedAt.UTC().Format(time.RFC3339)
		}
		fmt.Printf("   [%d] %s\n       Source: %s | Cat: %s | Score: %d | Published: %s\n", i+1, tc.item.Title, tc.item.SourceName, tc.category, tc.score, pubStr)
	}

	fmt.Println("\n3. SELECTED CANDIDATE:")
	fmt.Println("   (AI selector returned SKIP: all candidates are duplicates of recent published events or lack substance)")
	fmt.Println("========================================================")
	fmt.Println()
}

func buildSelectorPrompt(candidates []candidate, publishedTitles []string) string {
	now := time.Now()
	nowStr := now.Format("2006-01-02")
	var b strings.Builder
	b.WriteString("Choose the best news candidate for a Ukrainian Nintendo Telegram channel.\n")
	fmt.Fprintf(&b, "Сьогоднішня дата: %s\n", nowStr)

	if len(publishedTitles) > 0 {
		b.WriteString("\n=== ОПУБЛІКОВАНІ ПОСТИ ЗА ОСТАННІ 30 ДНІВ (ЗАБОРОНЕНО ДУБЛЮВАТИ ПОДІЇ) ===\n")
		for _, pt := range publishedTitles {
			t := strings.TrimSpace(pt)
			if t != "" {
				fmt.Fprintf(&b, "• %s\n", t)
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("Here is the ranked list of candidates (already scored by internal logic):\n\n")

	for i, c := range candidates {
		sourceText := c.scrapedBody
		if sourceText == "" {
			sourceText = c.item.Description
		}
		desc := strings.ReplaceAll(sourceText, "\n", " ")
		desc = strings.TrimSpace(desc)
		runes := []rune(desc)
		if len(runes) > 480 {
			desc = string(runes[:480]) + "..."
		}

		recentFlag := ""
		if c.recentSimilarPosted {
			recentFlag = ", RECENT_SIMILAR_POSTED: True"
		}
		hypeFlag := ""
		if c.hypeCount > 1 {
			hypeFlag = fmt.Sprintf(", sources: %d", c.hypeCount)
		}
		catLabel := string(c.category)
		if catLabel == "" {
			catLabel = "games"
		}
		dateStr := "невідома"
		if c.item.PublishedAt != nil && !c.item.PublishedAt.IsZero() {
			dateStr = c.item.PublishedAt.UTC().Format("2006-01-02 15:04 UTC")
		}
		fmt.Fprintf(&b, "Candidate #%d [rank: %d, score: %d, cat: %s, type: %s%s, date: %s%s]\n",
			i+1, c.rankScore, c.score, catLabel, c.item.SourceType, hypeFlag, dateStr, recentFlag)
		fmt.Fprintf(&b, "Title: %s\n", c.item.Title)
		if desc != "" {
			fmt.Fprintf(&b, "Body: %s\n", desc)
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf(`Editorial Guidelines & Selection Instructions:
1. КАТЕГОРИЧНО НЕ ВИБИРАЙ НОВИНУ ПРО ТЕ Ж САМЕ ПОДІЮ (АНТИ-ДУБЛЬ):
   - Уважно звіряйся зі списком опублікованих і модераційних постів вище.
   - Якщо кандидат розповідає про ту саму подію, нагороду, судовий процес, вирок, оновлення чи анонс, який уже висвітлювався (наприклад, Міямото і D.I.C.E./AIAS Hall of Fame або позов проти SwitchPirates) — це ДУБЛЬ, навіть якщо інше видання переписало новину через 4 години або тиждень. Не вибирай новину про те ж саме подію!
2. СВІТ NINTENDO ПОНАД УСЕ (БАЛАНС ТЕМАТИК):
   - ТОП-ПРІОРИТЕТ: апаратні відкриття і залізо Switch 2 (DLSS, DLSS 4, чипи Nvidia, датамайнінг SDK/прошивок, дослідження Digital Foundry), бізнес і рішення керівництва (Фурукава, Міямото, звіти, студії), судові позови/патенти, Nintendo Direct, музей/парки/кіно, великі системні реформи.
   - ІГРИ ЗАЛИШАЄМО, АЛЕ ДОЗОВАНО І ЯКІСНО: обирай ігрові новини тоді, коли це дійсно великий анонс, ексклюзив або подія з кількома джерелами (sources > 1). Категорично уникай рутинних іконок NSO, треків Nintendo Music, тижневих чартів продажів, гайдів, оглядів та повторів тієї ж франшизи (наприклад, Pikmin).
3. ВРАХОВУЙ ДАТИ ТА СВІЖІСТЬ: Сьогоднішня дата %s. Категорично відкидай застарілі рерайти минулих років та вкиди про вже випущені пристрої.
4. If a candidate has "RECENT_SIMILAR_POSTED: True", strictly penalize it UNLESS it contains genuinely massive new facts.
5. Return ONLY the number of the best candidate (e.g., 1 or 2).
6. If all candidates are duplicates, weak, routine filler, or lack substance, return "SKIP" instead of a number.`, nowStr))
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
	clean = strings.ReplaceAll(clean, "\n — ", "\n")
	clean = strings.ReplaceAll(clean, "  ", " ")
	clean = strings.ReplaceAll(clean, " .", ".")
	clean = excessiveNewlinesRe.ReplaceAllString(clean, "\n\n")
	return strings.TrimSpace(clean)
}

func formatHypeFooter(hypeCount int) string {
	if hypeCount > 3 {
		return fmt.Sprintf("\n\n🔥 <i>Цю подію обговорюють у %d інших джерелах</i>", hypeCount)
	}
	if hypeCount > 1 {
		return fmt.Sprintf("\n\n🔥 <i>Знайдено у %d джерелах</i>", hypeCount)
	}
	return ""
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
			if candidates[i].item.PublishedAt == nil {
				return false
			}
			if candidates[j].item.PublishedAt == nil {
				return true
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

	cleanSelected := dedup.StripPublisherSuffix(selectedItem.Title)
	selectedSig := dedup.SemanticSignature(cleanSelected)
	selectedFingerprint := dedup.BuildSimilarityText(cleanSelected, selectedItem.Description)

	for _, it := range allItems {
		if it.Link == selectedItem.Link {
			continue
		}
		if _, exists := hypeSources[it.SourceName]; exists {
			continue
		}

		cleanTitle := dedup.StripPublisherSuffix(it.Title)
		if dedup.HasConflictingDateMarkers(cleanSelected, cleanTitle) {
			continue
		}

		match := false
		if dedup.SemanticSignature(cleanTitle) == selectedSig {
			match = true
		} else if selectedFingerprint != "" {
			itFingerprint := dedup.BuildSimilarityText(cleanTitle, it.Description)
			if itFingerprint != "" && dedup.Similarity(selectedFingerprint, itFingerprint) >= 0.45 {
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

func validateTopCandidatesFreshness(ctx context.Context, topCandidates []candidate, defaultHours int, cfg *config.Config, database *sql.DB) ([]candidate, int, int, []candidate, map[string]string) {
	validated := make([]candidate, 0, len(topCandidates))
	quarantined := make([]candidate, 0)
	quarantineReasons := make(map[string]string)
	checked := 0
	dropped := 0

	for _, c := range topCandidates {
		checked++
		dateCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		sourcePublishedAt, err := fetcher.FetchSourcePublishedAt(dateCtx, c.item.Link)
		cancel()

		isTrusted := isTrustedDateSource(c, cfg)

		if err != nil {
			if isTrusted || canFallbackToFeedDateOnBlock(c, err) {
				slog.Info("source-date check failed for trusted/blocked high-signal source; keeping candidate with feed date",
					"title", c.item.Title,
					"source", c.item.SourceName,
					"url", c.item.Link,
					"error", err,
				)
				validated = append(validated, c)
				continue
			}

			reason := fmt.Sprintf("fetch error: %v", err)
			// Transient error: retry up to 3 runs before permanent quarantine
			prevAttempts, prevStatus, _ := db.GetQuarantineAttempts(ctx, database, c.item.Link)
			newAttempts, permanent, skip := evaluateQuarantine(prevAttempts, prevStatus, true)
			if skip {
				slog.Debug("candidate already permanently quarantined, skipping", "url", c.item.Link)
				continue
			}

			if permanent {
				slog.Warn("candidate quarantined permanently: source-date check failed after 3 attempts",
					"title", c.item.Title,
					"source", c.item.SourceName,
					"url", c.item.Link,
					"error", err,
					"attempt", newAttempts,
				)
			} else {
				slog.Warn("candidate fetch failed: transient error, will retry in next run",
					"title", c.item.Title,
					"source", c.item.SourceName,
					"url", c.item.Link,
					"error", err,
					"attempt", newAttempts,
					"max_attempts", 3,
				)
			}

			if database != nil && (cfg == nil || !cfg.DryRun) {
				quarantinedArticle := db.Article{
					SourceURL:   c.item.Link,
					URLHash:     c.urlHash,
					TitleHash:   c.titleHash,
					ContentHash: c.item.ContentHash,
					TitleRaw:    c.item.Title,
					SourceName:  c.item.SourceName,
					SourceType:  c.item.SourceType,
					Score:       c.score,
					PublishedAt: c.item.PublishedAt,
				}
				if err := db.RecordQuarantineAttempt(ctx, database, quarantinedArticle, newAttempts, permanent); err != nil {
					slog.Warn("failed to record quarantine attempt", "url", c.item.Link, "error", err)
				}
			}

			if permanent {
				quarantineReasons[c.item.Link] = reason
				quarantined = append(quarantined, c)
			}
			continue
		}

		if sourcePublishedAt == nil {
			if isTrusted {
				slog.Info("source-date missing for trusted source; keeping candidate with feed date",
					"title", c.item.Title,
					"source", c.item.SourceName,
					"url", c.item.Link,
				)
				validated = append(validated, c)
				continue
			}

			prevAttempts, prevStatus, _ := db.GetQuarantineAttempts(ctx, database, c.item.Link)
			newAttempts, permanent, skip := evaluateQuarantine(prevAttempts, prevStatus, false)
			if skip {
				slog.Debug("candidate already permanently quarantined, skipping", "url", c.item.Link)
				continue
			}

			reason := "missing publication date on page"
			slog.Warn("candidate quarantined immediately: source-date missing",
				"title", c.item.Title,
				"source", c.item.SourceName,
				"url", c.item.Link,
			)
			if database != nil && (cfg == nil || !cfg.DryRun) {
				quarantinedArticle := db.Article{
					SourceURL:   c.item.Link,
					URLHash:     c.urlHash,
					TitleHash:   c.titleHash,
					ContentHash: c.item.ContentHash,
					TitleRaw:    c.item.Title,
					SourceName:  c.item.SourceName,
					SourceType:  c.item.SourceType,
					Score:       c.score,
					Status:      db.StatusQuarantined,
					PublishedAt: c.item.PublishedAt,
				}
				if err := db.RecordQuarantineAttempt(ctx, database, quarantinedArticle, newAttempts, permanent); err != nil {
					slog.Warn("failed to record quarantined article in database", "url", c.item.Link, "error", err)
				}
			}
			quarantineReasons[c.item.Link] = reason
			quarantined = append(quarantined, c)
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

	return validated, checked, dropped, quarantined, quarantineReasons
}

// evaluateQuarantine determines whether an article should be quarantined immediately,
// retried, or skipped if already quarantined.
// - Transient fetch errors allow up to 3 attempts before permanent quarantine.
// - HTTP 200 with missing publication date is quarantined immediately (permanent = true).
// - Already permanently quarantined articles are skipped.
func evaluateQuarantine(prevAttempts int, prevStatus string, isFetchError bool) (newAttempts int, permanent bool, skip bool) {
	if prevStatus == db.StatusQuarantined {
		return prevAttempts, true, true
	}
	newAttempts = prevAttempts + 1
	if !isFetchError {
		// HTTP 200 with missing date goes to quarantine immediately
		return newAttempts, true, false
	}
	// Transient error: up to 3 attempts before permanent quarantine
	return newAttempts, newAttempts >= 3, false
}

func isTrustedDateSource(c candidate, cfg *config.Config) bool {
	if c.item.TrustFeedDate {
		return true
	}
	if cfg == nil || len(cfg.TrustedDateSources) == 0 {
		return false
	}
	srcName := strings.ToLower(strings.TrimSpace(c.item.SourceName))
	for _, t := range cfg.TrustedDateSources {
		tClean := strings.ToLower(strings.TrimSpace(t))
		if tClean != "" && strings.Contains(srcName, tClean) {
			return true
		}
	}
	return false
}

// canFallbackToFeedDateOnBlock allows Google News curated feeds and insider sources
// with anti-bot/WAF blocks (HTTP 403/429/Cloudflare) to keep their RSS pubDate instead
// of losing time-sensitive tech or corporate exclusives in 3-run retry quarantine.
func canFallbackToFeedDateOnBlock(c candidate, err error) bool {
	if err == nil || c.item.PublishedAt == nil {
		return false
	}
	if !c.item.NeedsRedirectResolve && !strings.EqualFold(c.item.SourceType, "insider") {
		return false
	}
	lowerErr := strings.ToLower(err.Error())
	isWAFBlock := strings.Contains(lowerErr, "403") ||
		strings.Contains(lowerErr, "429") ||
		strings.Contains(lowerErr, "forbidden") ||
		strings.Contains(lowerErr, "cloudflare") ||
		strings.Contains(lowerErr, "access denied")
	if !isWAFBlock {
		return false
	}
	return c.techScore >= 60 ||
		c.category == scorer.CategoryTech ||
		c.category == scorer.CategoryCompany ||
		c.category == scorer.CategoryLegal ||
		c.category == scorer.CategoryDirect
}

func handleQuarantineAlert(cfg *config.Config, quarantined []candidate) {
	quarantineCount := len(quarantined)
	sourceCounts := make(map[string]int)
	for _, q := range quarantined {
		sourceCounts[q.item.SourceName]++
	}

	slog.Warn("quarantine threshold reached",
		"quarantined_count", quarantineCount,
		"sources", sourceCounts,
	)

	if cfg == nil {
		return
	}

	if cfg.DryRun {
		slog.Info("DRY_RUN - would send quarantine alert to telegram",
			"quarantined_count", quarantineCount,
			"sources", sourceCounts,
		)
		return
	}

	botToken := cfg.TestTelegramToken
	if botToken == "" {
		botToken = cfg.TelegramBotToken
	}
	if botToken == "" {
		slog.Warn("cannot send quarantine alert: no telegram bot token configured")
		return
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		slog.Error("telegram bot init failed for quarantine alert", "error", err)
		return
	}

	targetChatID := cfg.AdminChatID
	if strings.TrimSpace(targetChatID) == "" {
		targetChatID = cfg.TestAdminChatID
	}
	if strings.TrimSpace(targetChatID) == "" {
		slog.Warn("cannot send quarantine alert: ADMIN_CHAT_ID is not configured (will not send to public channel)")
		return
	}

	if err := telegram.SendQuarantineAlert(bot, targetChatID, quarantineCount, sourceCounts); err != nil {
		slog.Error("quarantine alert: telegram send failed", "error", err, "target_chat_id", targetChatID)
	} else {
		slog.Info("quarantine alert sent to telegram", "target_chat_id", targetChatID, "count", quarantineCount)
	}
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
