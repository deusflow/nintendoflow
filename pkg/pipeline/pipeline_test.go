package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/deuswork/nintendoflow/pkg/ai"
	"github.com/deuswork/nintendoflow/pkg/config"
	"github.com/deuswork/nintendoflow/pkg/fetcher"
)

func TestCandidateRankingScoreUsesFeedPriority(t *testing.T) {
	now := time.Now()
	high := candidateRankingScore(100, 90, &now, "media", 24)
	low := candidateRankingScore(100, 60, &now, "media", 24)
	if high <= low {
		t.Fatalf("expected higher feed priority to produce higher ranking score, got high=%d low=%d", high, low)
	}

	defaultPriority := candidateRankingScore(100, 0, &now, "media", 24)
	explicitDefault := candidateRankingScore(100, 100, &now, "media", 24)
	if defaultPriority != explicitDefault {
		t.Fatalf("expected zero feed priority to normalize to 100, got %d vs %d", defaultPriority, explicitDefault)
	}
}

func TestSortCandidatesPrefersHigherRankScoreThenSourcePriority(t *testing.T) {
	now := time.Now()
	candidates := []candidate{
		{
			item:      fetcher.Item{Title: "lower-priority", SourcePriority: 60, PublishedAt: &now},
			score:     100,
			rankScore: candidateRankingScore(100, 60, &now, "media", 24),
		},
		{
			item:      fetcher.Item{Title: "higher-priority", SourcePriority: 90, PublishedAt: &now},
			score:     100,
			rankScore: candidateRankingScore(100, 90, &now, "media", 24),
		},
	}

	sortCandidates(candidates)
	if candidates[0].item.Title != "higher-priority" {
		t.Fatalf("expected higher-priority source to rank first, got %s", candidates[0].item.Title)
	}
}

func TestSortCandidatesWithNilPublishedAtDoesNotPanic(t *testing.T) {
	now := time.Now()
	candidates := []candidate{
		{
			item:      fetcher.Item{Title: "with-nil-date", SourcePriority: 100, PublishedAt: nil},
			score:     100,
			rankScore: 100,
		},
		{
			item:      fetcher.Item{Title: "with-valid-date", SourcePriority: 100, PublishedAt: &now},
			score:     100,
			rankScore: 100,
		},
	}

	// Should not panic on nil pointer dereference
	sortCandidates(candidates)
	if candidates[0].item.Title != "with-valid-date" {
		t.Fatalf("expected candidate with valid date to rank ahead of nil date, got %s", candidates[0].item.Title)
	}
}

func TestBuildSelectorPromptIncludesDates(t *testing.T) {
	pubDate := time.Date(2026, 10, 3, 7, 30, 0, 0, time.UTC)
	candidates := []candidate{
		{
			item: fetcher.Item{
				Title:       "Test candidate",
				Description: "Sample body",
				SourceType:  "aggregator",
				PublishedAt: &pubDate,
			},
			score: 100,
		},
	}

	prompt := buildSelectorPrompt(candidates, nil)
	if !strings.Contains(prompt, "2026-10-03") && !strings.Contains(prompt, "03.10.2026") {
		t.Errorf("expected selector prompt to contain candidate publication date, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Сьогодні") && !strings.Contains(prompt, "Today") && !strings.Contains(prompt, "Поточна дата") {
		t.Errorf("expected selector prompt to mention current date/time, got:\n%s", prompt)
	}
}

func TestQuarantineForGoogleNewsWithoutDate(t *testing.T) {
	// Mock HTTP server that returns HTML without any date fields
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><head><title>No date article</title></head><body>No date here</body></html>"))
	}))
	defer ts.Close()

	feedDate := time.Now().Add(-1 * time.Hour)
	candidates := []candidate{
		{
			item: fetcher.Item{
				Title:          "FCC Controller Leak",
				Link:           ts.URL,
				SourceName:     "Google News - Nintendo Switch 2",
				SourceType:     "aggregator",
				SourcePriority: 80,
				PublishedAt:    &feedDate,
				TrustFeedDate:  false,
			},
			score: 100,
		},
	}

	cfg := &config.Config{
		TrustedDateSources: []string{"Nintendo Everything", "Nintendo Life"},
		RecentTitlesHours:  24,
	}

	validated, checked, dropped, quarantined, _ := validateTopCandidatesFreshness(context.Background(), candidates, 24, cfg, nil)

	if checked != 1 {
		t.Fatalf("expected 1 checked candidate, got %d", checked)
	}
	if dropped != 0 {
		t.Fatalf("expected 0 dropped (stale) candidate, got %d", dropped)
	}
	if len(validated) != 0 {
		t.Fatalf("expected 0 validated candidates (should be quarantined), got %d", len(validated))
	}
	if len(quarantined) != 1 {
		t.Fatalf("expected 1 quarantined candidate, got %d", len(quarantined))
	}
	if quarantined[0].item.Title != "FCC Controller Leak" {
		t.Fatalf("expected quarantined candidate title 'FCC Controller Leak', got %q", quarantined[0].item.Title)
	}
}

func TestTrustedSourceWithoutDatePassesWithFeedDate(t *testing.T) {
	// Mock HTTP server that returns HTML without any date fields
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><head><title>No date article</title></head><body>No date here</body></html>"))
	}))
	defer ts.Close()

	feedDate := time.Now().Add(-2 * time.Hour)
	candidates := []candidate{
		{
			item: fetcher.Item{
				Title:          "Zelda Museum Exhibition",
				Link:           ts.URL,
				SourceName:     "Nintendo Everything",
				SourceType:     "media",
				SourcePriority: 90,
				PublishedAt:    &feedDate,
				TrustFeedDate:  true,
			},
			score: 100,
		},
	}

	cfg := &config.Config{
		TrustedDateSources: []string{"Nintendo Everything", "Nintendo Life"},
		RecentTitlesHours:  24,
	}

	validated, checked, _, quarantined, _ := validateTopCandidatesFreshness(context.Background(), candidates, 24, cfg, nil)

	if checked != 1 {
		t.Fatalf("expected 1 checked candidate, got %d", checked)
	}
	if len(quarantined) != 0 {
		t.Fatalf("expected 0 quarantined candidates for trusted source, got %d", len(quarantined))
	}
	if len(validated) != 1 {
		t.Fatalf("expected 1 validated candidate, got %d", len(validated))
	}
	if validated[0].item.PublishedAt == nil || !validated[0].item.PublishedAt.Equal(feedDate) {
		t.Fatalf("expected validated candidate to retain feed date %v, got %v", feedDate, validated[0].item.PublishedAt)
	}
}

func TestQuarantineThresholdAccumulation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>No date</body></html>"))
	}))
	defer ts.Close()

	now := time.Now()
	var candidates []candidate
	for i := 1; i <= 6; i++ {
		candidates = append(candidates, candidate{
			item: fetcher.Item{
				Title:          fmt.Sprintf("Untrusted Candidate %d", i),
				Link:           fmt.Sprintf("%s/article-%d", ts.URL, i),
				SourceName:     "Google News - Aggregator",
				SourceType:     "aggregator",
				SourcePriority: 70,
				PublishedAt:    &now,
			},
			score: 100,
		})
	}

	cfg := &config.Config{
		TrustedDateSources: []string{"Nintendo Everything"},
		RecentTitlesHours:  24,
	}

	validated, checked, _, quarantined, _ := validateTopCandidatesFreshness(context.Background(), candidates, 24, cfg, nil)
	if checked != 6 {
		t.Fatalf("expected 6 checked, got %d", checked)
	}
	if len(validated) != 0 {
		t.Fatalf("expected 0 validated, got %d", len(validated))
	}
	if len(quarantined) != 6 {
		t.Fatalf("expected 6 quarantined, got %d", len(quarantined))
	}
}

func TestQuarantineNotCountedTwiceAcrossRuns(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>No date</body></html>"))
	}))
	defer ts.Close()

	now := time.Now()
	cfg := &config.Config{
		TrustedDateSources: []string{"Nintendo Everything"},
		RecentTitlesHours:  24,
	}

	// Simulated persistent storage of known URLs in DB
	knownURLs := make(map[string]struct{})

	// Run 1: 3 untrusted items appear for the first time
	run1Candidates := []candidate{
		{item: fetcher.Item{Title: "Candidate 1", Link: ts.URL + "/c1", SourceName: "Google News", PublishedAt: &now}, urlHash: "hash-c1"},
		{item: fetcher.Item{Title: "Candidate 2", Link: ts.URL + "/c2", SourceName: "Google News", PublishedAt: &now}, urlHash: "hash-c2"},
		{item: fetcher.Item{Title: "Candidate 3", Link: ts.URL + "/c3", SourceName: "Google News", PublishedAt: &now}, urlHash: "hash-c3"},
	}

	_, _, _, quarantinedRun1, _ := validateTopCandidatesFreshness(context.Background(), run1Candidates, 24, cfg, nil)
	if len(quarantinedRun1) != 3 {
		t.Fatalf("expected 3 quarantined in run 1, got %d", len(quarantinedRun1))
	}

	// Persist quarantined items to knownURLs (as db.InsertQuarantinedArticle does)
	for _, q := range quarantinedRun1 {
		knownURLs[q.urlHash] = struct{}{}
	}

	// Run 2: Feed returns the same 3 items + 2 new items
	incomingRun2 := []candidate{
		{item: fetcher.Item{Title: "Candidate 1", Link: ts.URL + "/c1", SourceName: "Google News", PublishedAt: &now}, urlHash: "hash-c1"},
		{item: fetcher.Item{Title: "Candidate 2", Link: ts.URL + "/c2", SourceName: "Google News", PublishedAt: &now}, urlHash: "hash-c2"},
		{item: fetcher.Item{Title: "Candidate 3", Link: ts.URL + "/c3", SourceName: "Google News", PublishedAt: &now}, urlHash: "hash-c3"},
		{item: fetcher.Item{Title: "Candidate 4", Link: ts.URL + "/c4", SourceName: "Google News", PublishedAt: &now}, urlHash: "hash-c4"},
		{item: fetcher.Item{Title: "Candidate 5", Link: ts.URL + "/c5", SourceName: "Google News", PublishedAt: &now}, urlHash: "hash-c5"},
	}

	// Local filter step skips items already in knownURLs
	var run2Candidates []candidate
	for _, c := range incomingRun2 {
		if _, exists := knownURLs[c.urlHash]; exists {
			continue // skipped as already known / quarantined in DB
		}
		run2Candidates = append(run2Candidates, c)
	}

	if len(run2Candidates) != 2 {
		t.Fatalf("expected only 2 new candidates to reach freshness check, got %d", len(run2Candidates))
	}

	_, _, _, quarantinedRun2, _ := validateTopCandidatesFreshness(context.Background(), run2Candidates, 24, cfg, nil)
	if len(quarantinedRun2) != 2 {
		t.Fatalf("expected 2 new quarantined in run 2, got %d", len(quarantinedRun2))
	}

	// Alert check: 2 < 5 -> alert must NOT be sent in run 2!
	alertSentRun2 := len(quarantinedRun2) >= 5
	if alertSentRun2 {
		t.Fatalf("quarantine alert should NOT be sent when only 2 new items are quarantined")
	}

	// Persist the 2 new ones
	for _, q := range quarantinedRun2 {
		knownURLs[q.urlHash] = struct{}{}
	}

	// Run 3: 5 completely new items appear
	var run3Candidates []candidate
	for i := 6; i <= 10; i++ {
		hash := fmt.Sprintf("hash-c%d", i)
		if _, exists := knownURLs[hash]; !exists {
			run3Candidates = append(run3Candidates, candidate{
				item:    fetcher.Item{Title: fmt.Sprintf("Candidate %d", i), Link: fmt.Sprintf("%s/c%d", ts.URL, i), SourceName: "Google News", PublishedAt: &now},
				urlHash: hash,
			})
		}
	}

	_, _, _, quarantinedRun3, _ := validateTopCandidatesFreshness(context.Background(), run3Candidates, 24, cfg, nil)
	if len(quarantinedRun3) != 5 {
		t.Fatalf("expected 5 quarantined in run 3, got %d", len(quarantinedRun3))
	}

	// Alert check: 5 >= 5 -> alert IS sent!
	alertSentRun3 := len(quarantinedRun3) >= 5
	if !alertSentRun3 {
		t.Fatalf("quarantine alert SHOULD be sent when 5 new items are quarantined")
	}
}

func TestSelectorPromptIncludesPublishedTitlesAndEventDedupInstruction(t *testing.T) {
	pubDate := time.Date(2026, 9, 26, 14, 0, 0, 0, time.UTC)
	candidates := []candidate{
		{
			item: fetcher.Item{
				Title:       "Nintendo Awarded $4.5M in Piracy Lawsuit Against SwitchPirates Moderator",
				Description: "Nintendo secured a default judgment against James Williams.",
				SourceType:  "media",
				PublishedAt: &pubDate,
			},
			score: 180,
		},
	}
	publishedTitles := []string{
		"Nintendo Wins $4.5 Million Judgment Against SwitchPirates Subreddit Moderator",
	}

	prompt := buildSelectorPrompt(candidates, publishedTitles)

	if !strings.Contains(prompt, "Nintendo Wins $4.5 Million Judgment Against SwitchPirates Subreddit Moderator") {
		t.Fatalf("expected selector prompt to include 30-day published titles, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "КАТЕГОРИЧНО НЕ ВИБИРАЙ НОВИНУ ПРО ТЕ Ж САМЕ ПОДІЮ") {
		t.Fatalf("expected selector prompt to include anti-duplicate instruction, got:\n%s", prompt)
	}
}

func TestPiracyLawsuitDuplicatePairConsideredDuplicate(t *testing.T) {
	// Post published on 2026-09-24
	publishedTitles := []string{
		"Nintendo Wins $4.5 Million Judgment Against SwitchPirates Subreddit Moderator",
	}

	date26 := time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC)
	// Duplicate candidate on 2026-09-26 covering the exact same event
	candidate1 := candidate{
		item: fetcher.Item{
			Title:       "Nintendo Awarded $4.5M in Piracy Lawsuit Against SwitchPirates Moderator",
			Description: "Nintendo was awarded $4.5 million in a piracy lawsuit against a Reddit moderator who ran pirate shops for Nintendo Switch games.",
			SourceType:  "media",
			PublishedAt: &date26,
		},
		score: 200,
	}

	// Fresh, non-duplicate candidate on 2026-09-26
	candidate2 := candidate{
		item: fetcher.Item{
			Title:       "Kirby and the World Beyond Announced for Nintendo Switch 2",
			Description: "HAL Laboratory and Nintendo announced the next mainline 3D adventure coming to Nintendo Switch 2.",
			SourceType:  "official",
			PublishedAt: &date26,
		},
		score: 180,
	}

	candidates := []candidate{candidate1, candidate2}
	prompt := buildSelectorPrompt(candidates, publishedTitles)

	// Verify prompt incorporates the published title and instructions
	if !strings.Contains(prompt, publishedTitles[0]) {
		t.Fatalf("expected prompt to contain published title from 24.09")
	}

	// Test 1: Parser handles SKIP cleanly
	skipIdx, okSkip := parseSelectedIndex("SKIP", len(candidates))
	if okSkip {
		t.Fatalf("expected parseSelectedIndex to return false for SKIP, got idx=%d", skipIdx)
	}

	// Test 2: If live Gemini API key is present in environment, test with the live model
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		provider, err := ai.NewGeminiProvider(ctx, apiKey, "gemini-3.5-flash")
		if err == nil {
			// When Candidate 2 (Kirby) is present, the model must NOT choose Candidate 1 (the duplicate)
			resp, err := provider.Complete(ctx, prompt)
			if err == nil {
				idx, ok := parseSelectedIndex(resp, len(candidates))
				if !ok || idx != 1 {
					t.Errorf("expected model to reject duplicate candidate 1 and choose candidate 2 (got response %q)", resp)
				}
			}

			// When ONLY Candidate 1 (the duplicate) is present, the model must return SKIP
			promptOnlyDup := buildSelectorPrompt([]candidate{candidate1}, publishedTitles)
			respOnlyDup, err := provider.Complete(ctx, promptOnlyDup)
			if err == nil {
				if !strings.Contains(strings.ToUpper(respOnlyDup), "SKIP") {
					t.Errorf("expected model to return SKIP when only duplicate candidate is present, got %q", respOnlyDup)
				}
			}
		}
	}
}




