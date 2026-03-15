package scorer

import (
	"testing"

	"github.com/deuswork/nintendoflow/internal/config"
)

func TestShouldPostAllowsNintendoComparisonOnStrictFeed(t *testing.T) {
	keywords := []config.Keyword{
		{Word: "switch 2", Role: "anchor", Weight: 35},
		{Word: "performance", Role: "comparison", Weight: 10},
		{Word: "steam deck", Role: "comparison", Weight: 8},
		{Word: "comparison", Role: "comparison", Weight: 10},
	}

	result, ok, reason := ShouldPost(
		"Switch 2 performance comparison with Steam Deck",
		"Early benchmark talk points to better handheld performance.",
		keywords,
		25,
		true,
	)
	if !ok {
		t.Fatalf("expected article to pass, got ok=false reason=%s result=%+v", reason, result)
	}
	if !result.HasAnchor {
		t.Fatalf("expected Nintendo anchor to be detected")
	}
	if !result.HasComparison {
		t.Fatalf("expected comparison terms to be detected")
	}
}

func TestShouldPostRejectsNonNintendoComparisonOnStrictFeed(t *testing.T) {
	keywords := []config.Keyword{
		{Word: "performance", Role: "comparison", Weight: 10},
		{Word: "comparison", Role: "comparison", Weight: 10},
		{Word: "ps5", Role: "comparison", Weight: 4},
		{Word: "xbox", Role: "comparison", Weight: 4},
	}

	result, ok, reason := ShouldPost(
		"PS5 vs Xbox performance comparison",
		"A fresh graphics benchmark compares frame rates and resolution.",
		keywords,
		25,
		true,
	)
	if ok {
		t.Fatalf("expected article to be rejected, got ok=true result=%+v", result)
	}
	if reason != "missing_nintendo_anchor" && reason != "below_min_score" {
		t.Fatalf("unexpected rejection reason: %s", reason)
	}
}

func TestShouldPostAllowsTrustedNintendoFeedWithoutExplicitAnchor(t *testing.T) {
	keywords := []config.Keyword{
		{Word: "performance", Role: "comparison", Weight: 10},
		{Word: "comparison", Role: "comparison", Weight: 10},
		{Word: "fps", Role: "comparison", Weight: 8},
	}

	result, ok, reason := ShouldPost(
		"Performance comparison shows cleaner 60 FPS mode",
		"Frame-rate analysis focuses on handheld play.",
		keywords,
		25,
		false,
	)
	if !ok {
		t.Fatalf("expected trusted-feed article to pass, got ok=false reason=%s result=%+v", reason, result)
	}
	if result.HasAnchor {
		t.Fatalf("did not expect explicit anchor for this test case")
	}
}
