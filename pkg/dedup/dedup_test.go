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

func TestRegularSeriesWithDifferentDatesNotDuplicates(t *testing.T) {
	cases := []struct {
		name string
		t1   string
		t2   string
	}{
		{
			name: "Nintendo pre-order updates October 4 vs October 11",
			t1:   "Nintendo pre-order updates – October 4",
			t2:   "Nintendo pre-order updates – October 11",
		},
		{
			name: "Japanese Nintendo eShop releases October 8 vs October 15",
			t1:   "Japanese Nintendo eShop releases for October 8",
			t2:   "Japanese Nintendo eShop releases for October 15",
		},
		{
			name: "Weekly Famitsu with slash dates",
			t1:   "Famitsu Sales: 9/23/24 – 9/29/24",
			t2:   "Famitsu Sales: 9/30/24 – 10/6/24",
		},
		{
			name: "Weekly Famitsu with full dates",
			t1:   "Famitsu weekly sales: October 3, 2026",
			t2:   "Famitsu weekly sales: October 10, 2026",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 1. Semantic signatures must not match
			sig1 := SemanticSignature(tc.t1)
			sig2 := SemanticSignature(tc.t2)
			if sig1 == sig2 {
				t.Fatalf("expected different semantic signatures for %q and %q, both got %s", tc.t1, tc.t2, sig1)
			}

			// 2. HashTitle must not match
			if HashTitle(tc.t1) == HashTitle(tc.t2) {
				t.Fatalf("expected different HashTitle for %q and %q", tc.t1, tc.t2)
			}

			// 3. Similarity must be 0 due to conflicting dates
			sim := Similarity(tc.t1, tc.t2)
			if sim != 0.0 {
				t.Fatalf("expected similarity 0.0 for conflicting dates, got %f", sim)
			}

			// 4. Must NOT be considered near duplicate
			text1 := BuildSimilarityText(tc.t1, "")
			text2 := BuildSimilarityText(tc.t2, "")
			if IsNearDuplicate(text1, []string{text2}, 0.50) {
				t.Fatalf("expected titles with different dates not to be duplicates")
			}
		})
	}

	// Also verify that when BOTH the event AND the date match, it IS treated as a duplicate:
	t.Run("Same series same date is a duplicate", func(t *testing.T) {
		t1 := "Nintendo pre-order updates – October 4"
		t2 := "Nintendo pre-order updates – October 4 (Latest Edition)"

		text1 := BuildSimilarityText(t1, "")
		text2 := BuildSimilarityText(t2, "")
		if !IsNearDuplicate(text1, []string{text2}, 0.60) {
			t.Fatalf("expected identical event on the same date to be treated as duplicate")
		}
	})
}


