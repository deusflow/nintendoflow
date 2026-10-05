package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

	prompt := buildSelectorPrompt(candidates)
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

	validated, checked, dropped, quarantined := validateTopCandidatesFreshness(context.Background(), candidates, 24, cfg)

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

	validated, checked, _, quarantined := validateTopCandidatesFreshness(context.Background(), candidates, 24, cfg)

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
				Link:           ts.URL,
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

	validated, checked, _, quarantined := validateTopCandidatesFreshness(context.Background(), candidates, 24, cfg)
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


