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

func TestStripForbiddenIntro(t *testing.T) {
	input := "Йооой, оце вееесчь ✨ Японія вже вибрала найкращі ігри для Switch 2."
	expected := "Японія вже вибрала найкращі ігри для Switch 2."
	got := StripForbiddenIntro(input)
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestPiracyLawsuitDuplicateTitlesWithoutLLM(t *testing.T) {
	// Real headlines from lawsuit over piracy ($ vs £):
	titleUSD := "Nintendo Wins $4.5 Million Piracy Lawsuit Against SwitchPirates Moderator"
	titleGBP := "Nintendo Wins £3.4 Million Piracy Lawsuit Against SwitchPirates Moderator"

	// 1. SemanticSignature check: both titles must have the identical semantic signature
	sigUSD := SemanticSignature(titleUSD)
	sigGBP := SemanticSignature(titleGBP)
	if sigUSD != sigGBP {
		t.Fatalf("expected identical SemanticSignature for lawsuit titles, got USD: %s vs GBP: %s", sigUSD, sigGBP)
	}

	// 2. HashTitle check: hashes must match
	if HashTitle(titleUSD) != HashTitle(titleGBP) {
		t.Fatalf("expected identical HashTitle for lawsuit titles")
	}

	// 3. Jaccard similarity / near-duplicate check on title alone
	textUSD := BuildSimilarityText(titleUSD, "")
	textGBP := BuildSimilarityText(titleGBP, "")
	sim := Similarity(textUSD, textGBP)
	if sim < 0.64 {
		t.Fatalf("expected similarity >= 0.64, got %f", sim)
	}
	if !IsNearDuplicate(textGBP, []string{textUSD}, 0.64) {
		t.Fatalf("expected titleGBP to be detected as near-duplicate of titleUSD without LLM")
	}
}

