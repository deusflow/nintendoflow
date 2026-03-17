package dedup

import "testing"

func TestHashTitleNormalizesEquivalentHeadlines(t *testing.T) {
	a := "Nintendo Switch firmware 21.0.0 update - now live"
	b := "  NINTENDO switch firmware 21 0 0 UPDATE (full patch notes)  "
	if HashTitle(a) != HashTitle(b) {
		t.Fatalf("expected equivalent normalized title hashes to match")
	}
}

func TestIsNearDuplicateOnTitleAndDescription(t *testing.T) {
	recent := []string{
		BuildSimilarityText(
			"Nintendo Switch firmware 21.0.0 is now live",
			"Patch notes mention stability improvements and eShop fixes.",
		),
	}

	candidate := BuildSimilarityText(
		"Nintendo launches Switch firmware 21.0.0 update",
		"New patch notes include eShop fixes and overall stability improvements.",
	)

	if !IsNearDuplicate(candidate, recent, 0.55) {
		t.Fatalf("expected candidate to be treated as near-duplicate")
	}
}

func TestThresholdForSourceType(t *testing.T) {
	aggregator := ThresholdForSourceType("aggregator")
	official := ThresholdForSourceType("official")
	insider := ThresholdForSourceType("insider")
	media := ThresholdForSourceType("media")

	if !(aggregator < media && media <= insider && insider < official) {
		t.Fatalf("unexpected threshold ordering: agg=%v media=%v insider=%v official=%v", aggregator, media, insider, official)
	}
}
