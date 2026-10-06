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
	defaultDedupHours          = 30 * 24 // 30 days window for deduplication
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

	maxCandidatesToCheck := 10
	if len(candidates) < maxCandidatesToCheck {
		maxCandidatesToCheck = len(candidates)
	}
	candidatesPool := candidates[:maxCandidatesToCheck]
	checkedCandidates, dateChecked, dateDropped, quarantined, quarantineReasons := validateTopCandidatesFreshness(ctx, candidatesPool, cfg.RecentTitlesHours, cfg, database)
	sortCandidates(checkedCandidates)
	quarantineCount := len(quarantined)

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

	topN := 5
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
		logFinalStats(fetchedCount, 0, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}
	topCandidates := checkedCandidates

	selectionPrompt := buildSelectorPrompt(topCandidates, recentPublishedTitles)

	var selectedIdx int
	aiSelectorUsed = true
	rawSelection, err := manager.Generate(ctx, selectionPrompt)
	selectedIdx = 0
	if err != nil {
		slog.Warn("AI selector failed, using top-scored fallback", "error", err)
	} else {
		slog.Info("AI selector used", "provider", manager.LastProvider())
		if strings.Contains(strings.ToUpper(rawSelection), "SKIP") {
			slog.Info("AI selector decided to skip all candidates (duplicates or low quality)", "response", rawSelection)
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
					fmt.Printf("   [%d] %s\n       Source: %s | Score: %d | Published: %s\n", i+1, tc.item.Title, tc.item.SourceName, tc.score, pubStr)
				}

				fmt.Println("\n3. SELECTED CANDIDATE:")
				fmt.Println("   (AI selector returned SKIP: all candidates are duplicates of recent published events or lack substance)")
				fmt.Println("========================================================")
				fmt.Println()
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
		Title:       selected.item.Title,
		Body:        articleBody,
		Source:      selected.item.SourceName,
		PublishedAt: selected.item.PublishedAt,
		CurrentTime: time.Now(),
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
	
	postData, parseErr := ai.ParseJSONPost(rewritten)
	if parseErr != nil {
		slog.Error("Failed to parse AI JSON, falling back to raw text", "error", parseErr, "raw", rewritten)
		postData.TelegramHTML = sanitizeGeneratedBody(rewritten)
		postData.Type = ai.ArticleTypeNews
	}
	
	if postData.Skip {
		slog.Info("AI explicitly skipped via JSON",
			"title", selected.item.Title,
			"reason", postData.Reason,
		)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	articleType := postData.Type
	cleanBody := sanitizeGeneratedBody(postData.TelegramHTML)
	threadsBody := dedup.StripForbiddenIntro(postData.ThreadsText)

	hypeCount := calculateHype(selected.item, items)
	cleanBody += formatHypeFooter(hypeCount)

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
		BodyThreads: threadsBody,
		VideoURL:    selected.item.VideoURL,
		ImageURL:    selected.item.ImageURL,
		SourceName:  selected.item.SourceName,
		SourceType:  selected.item.SourceType,
		ArticleType: articleType,
		Score:       selected.score,
		Status:      db.StatusPending,
		PublishedAt: selected.item.PublishedAt,
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
			fmt.Printf("   [%d] %s\n       Source: %s | Score: %d | Published: %s\n", i+1, tc.item.Title, tc.item.SourceName, tc.score, pubStr)
		}

		fmt.Printf("\n3. SELECTED CANDIDATE (Index %d):\n", selectedIdx+1)
		fmt.Printf("   Title:  %s\n", article.TitleRaw)
		fmt.Printf("   Source: %s\n", article.SourceName)
		fmt.Printf("   Type:   %s\n", article.ArticleType)

		fmt.Println("\n4. WOULD PUBLISH TELEGRAM POST:")
		fmt.Println("--------------------------------------------------------")
		fmt.Println(cleanBody)
		fmt.Println("--------------------------------------------------------")
		fmt.Println("========================================================")
		fmt.Println()

		slog.Info("DRY_RUN - would post selected article", "title", article.TitleRaw, "provider", rewriteProvider)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	articleID, err := db.InsertArticle(ctx, database, article)
	if err != nil {
		slog.Error("insert pending article failed", "error", err)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}
	article.ID = articleID

	if err := db.UpdateBodies(ctx, database, article.ID, cleanBody, threadsBody, rewriteProvider); err != nil {
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

	previewMessageID, err := telegram.SendModerationPreview(bot, previewChatID, article)
	if err != nil {
		slog.Error("send moderation preview failed", "error", err, "article_id", article.ID)
		logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
		return
	}

	slog.Info("moderation preview sent",
		"article_id", article.ID,
		"preview_chat_id", previewChatID,
		"preview_message_id", previewMessageID,
	)

	logFinalStats(fetchedCount, filteredCount, aiSelectorUsed, aiRewriteUsed, posted, manager.CallsUsed(), manager.RetriesUsed(), manager.CallsBudget(), runStart)
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
		dateStr := "невідома"
		if c.item.PublishedAt != nil && !c.item.PublishedAt.IsZero() {
			dateStr = c.item.PublishedAt.UTC().Format("2006-01-02 15:04 UTC")
		}
		fmt.Fprintf(&b, "Candidate #%d [score: %d, type: %s, date: %s%s]\n", i+1, c.score, c.item.SourceType, dateStr, recentFlag)
		fmt.Fprintf(&b, "Title: %s\n", c.item.Title)
		if desc != "" {
			fmt.Fprintf(&b, "Body: %s\n", desc)
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf(`Editorial Guidelines & Selection Instructions:
1. КАТЕГОРИЧНО НЕ ВИБИРАЙ НОВИНУ ПРО ТЕ Ж САМЕ ПОДІЮ (АНТИ-ДУБЛЬ):
   - Уважно звіряйся зі списком опублікованих постів за останні 30 днів вище.
   - Якщо кандидат розповідає про ту саму подію, судовий процес, вирок, оновлення чи анонс, який уже висвітлювався в каналі (наприклад, позов проти піратства / модератора Reddit SwitchPirates) — це ДУБЛЬ, навіть якщо інше видання переписало новину через 2 дні чи тиждень. Не вибирай новину про те ж саме подію!
   - Якщо кандидат дублює опубліковану подію — категорично відхиляй його.
2. NINTENDO-FIRST PRIORITY: The channel is primarily about NINTENDO AS A COMPANY AND ECOSYSTEM, not just an endless feed of routine third-party game release dates.
   - TOP PRIORITY: Nintendo company moves, official Nintendo Direct announcements, executive statements (Furukawa, Miyamoto, Aonuma, Koizumi), console sales milestones, financial earnings reports, Nintendo Switch Online updates, backward compatibility, firmware/OS updates, Nintendo Museum, legal battles/IP protection (e.g. Palworld/Pocketpair lawsuit, emulator bans), and major first-party franchise announcements (Mario, Zelda, Pokemon, Metroid, Smash).
   - MEDIUM PRIORITY: Exclusive third-party partnerships, major Switch 2 technical breakthroughs (DLSS, performance reveals).
   - LOW PRIORITY: Generic multiplatform game release date announcements, minor indie ports, routine game trailers.
   - If a candidate covering Nintendo corporate, hardware, executive statement, or ecosystem is present alongside routine indie/3rd-party game release dates, ALWAYS prefer the Nintendo corporate/hardware/ecosystem candidate!
3. ВРАХОВУЙ ДАТИ ТА СВІЖІСТЬ: Сьогоднішня дата %s. Оцінюй дату кожного кандидата (вказана в полі date). Категорично відкидай або штрафуй застарілі рерайти минулих років та матеріали, які подають як майбутнє або невідоме те, що вже давно вийшло за списком пристроїв.
4. If a candidate has "RECENT_SIMILAR_POSTED: True", strictly penalize it UNLESS it contains genuinely new and massive information.
5. Return ONLY the number of the best candidate (e.g., 1 or 2).
6. If all candidates are duplicates, weak, repetitive, or lack substance, return "SKIP" instead of a number.`, nowStr))
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
			if isTrusted {
				slog.Info("source-date check failed for trusted source; keeping candidate with feed date",
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
