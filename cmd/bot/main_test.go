package main

import (
	"testing"
	"time"

	"github.com/deuswork/nintendoflow/pkg/fetcher"
)

func TestCandidateRankingScoreUsesFeedPriority(t *testing.T) {
	high := candidateRankingScore(100, 90)
	low := candidateRankingScore(100, 60)
	if high <= low {
		t.Fatalf("expected higher feed priority to produce higher ranking score, got high=%d low=%d", high, low)
	}

	defaultPriority := candidateRankingScore(100, 0)
	explicitDefault := candidateRankingScore(100, 100)
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
			rankScore: candidateRankingScore(100, 60),
		},
		{
			item:      fetcher.Item{Title: "higher-priority", SourcePriority: 90, PublishedAt: &now},
			score:     100,
			rankScore: candidateRankingScore(100, 90),
		},
	}

	sortCandidates(candidates)
	if candidates[0].item.Title != "higher-priority" {
		t.Fatalf("expected higher-priority source to rank first, got %s", candidates[0].item.Title)
	}
}
