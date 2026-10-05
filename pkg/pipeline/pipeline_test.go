package pipeline

import (
	"strings"
	"testing"
	"time"

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

