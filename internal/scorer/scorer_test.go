package scorer

import (
	"testing"

	"github.com/deuswork/nintendoflow/internal/config"
)

func TestShouldPostAllowsNintendoComparisonOnStrictFeed(t *testing.T) {
	topics := map[string]config.Topic{
		"hardware_anchor": {
			Enabled:  true,
			Priority: 100,
			Keywords: []config.Keyword{
				{Word: "switch 2", Role: "anchor", Weight: 35},
			},
		},
		"comparison": {
			Enabled:  true,
			Priority: 100,
			Keywords: []config.Keyword{
				{Word: "performance", Role: "comparison", Weight: 10},
				{Word: "steam deck", Role: "comparison", Weight: 8},
				{Word: "comparison", Role: "comparison", Weight: 10},
			},
		},
	}

	result, ok, reason := ShouldPost(
		"Switch 2 performance comparison with Steam Deck",
		"Early benchmark talk points to better handheld performance.",
		topics,
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
	topics := map[string]config.Topic{
		"comparison": {
			Enabled:  true,
			Priority: 100,
			Keywords: []config.Keyword{
				{Word: "performance", Role: "comparison", Weight: 10},
				{Word: "comparison", Role: "comparison", Weight: 10},
				{Word: "ps5", Role: "comparison", Weight: 4},
				{Word: "xbox", Role: "comparison", Weight: 4},
			},
		},
	}

	result, ok, reason := ShouldPost(
		"PS5 vs Xbox performance comparison",
		"A fresh graphics benchmark compares frame rates and resolution.",
		topics,
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
	topics := map[string]config.Topic{
		"comparison": {
			Enabled:  true,
			Priority: 100,
			Keywords: []config.Keyword{
				{Word: "performance", Role: "comparison", Weight: 10},
				{Word: "comparison", Role: "comparison", Weight: 10},
				{Word: "fps", Role: "comparison", Weight: 8},
			},
		},
	}

	result, ok, reason := ShouldPost(
		"Performance comparison shows cleaner 60 FPS mode",
		"Frame-rate analysis focuses on handheld play.",
		topics,
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

func TestEvaluateAppliesTopicPriorityMultiplier(t *testing.T) {
	topics := map[string]config.Topic{
		"hardware_tech": {
			Enabled:  true,
			Priority: 180,
			Keywords: []config.Keyword{
				{Word: "developer insight", Role: "tech", Weight: 75},
				{Word: "portable mode", Role: "tech", Weight: 70},
			},
		},
	}

	result := Evaluate(
		"Developer insight on portable mode",
		"Early analysis mentions portable mode again.",
		topics,
	)

	// 75*180/100 + 70*180/100 = 135 + 126 = 261.
	if result.Score != 261 {
		t.Fatalf("expected score=261 with priority multiplier, got %d", result.Score)
	}
	if result.TechScore != 261 {
		t.Fatalf("expected tech score=261 with priority multiplier, got %d", result.TechScore)
	}
}

func TestShouldPostHighTechScoreBypassesAnchorOnStrictFeed(t *testing.T) {
	topics := map[string]config.Topic{
		"hardware_tech": {
			Enabled:  true,
			Priority: 180,
			Keywords: []config.Keyword{
				{Word: "developer insight", Role: "tech", Weight: 75},
				{Word: "portable mode", Role: "tech", Weight: 70},
				{Word: "hidden capabilities", Role: "tech", Weight: 85},
			},
		},
	}

	result, ok, reason := ShouldPost(
		"Developer insight reveals hidden capabilities in portable mode",
		"Technical discussion focuses on hidden capabilities only.",
		topics,
		4,
		true,
	)

	if !ok {
		t.Fatalf("expected strict feed to accept article via high tech bypass, got ok=false reason=%s result=%+v", reason, result)
	}
	if result.TechScore < strictFeedHighTechBypassScore {
		t.Fatalf("expected tech score to exceed bypass threshold, got %d", result.TechScore)
	}
	if reason != "accepted_via_high_tech" {
		t.Fatalf("expected accepted_via_high_tech, got %s", reason)
	}
	if result.HasAnchor {
		t.Fatalf("did not expect anchor to be detected")
	}
}

func TestShouldPostLowTechScoreStillNeedsAnchorOnStrictFeed(t *testing.T) {
	topics := map[string]config.Topic{
		"hardware_tech": {
			Enabled:  true,
			Priority: 180,
			Keywords: []config.Keyword{
				{Word: "portable mode", Role: "tech", Weight: 70},
			},
		},
	}

	result, ok, reason := ShouldPost(
		"Portable mode looks promising",
		"Technical discussion focuses on portable mode only.",
		topics,
		4,
		true,
	)

	if ok {
		t.Fatalf("expected strict feed to reject low-tech-only article, got ok=true result=%+v", result)
	}
	if result.TechScore >= strictFeedHighTechBypassScore {
		t.Fatalf("expected tech score below bypass threshold, got %d", result.TechScore)
	}
	if reason != "missing_nintendo_anchor" {
		t.Fatalf("expected missing_nintendo_anchor, got %s", reason)
	}
}
