package dedup

import (
	"testing"
)

func TestStripPublisherSuffixAndNormalizeTitle(t *testing.T) {
	a := NormalizeTitleForDedup("Nintendo Switch 2 Hides DLSS 4 Preset K In System Files - Wccftech")
	b := NormalizeTitleForDedup("Nintendo Switch 2 hides DLSS 4 Preset K in system files | VGC")
	if a != b {
		t.Fatalf("expected publisher-stripped titles to normalize identically, got %q vs %q", a, b)
	}

	// Ensure real title hyphen content is preserved when not a known outlet.
	c := NormalizeTitleForDedup("Switch 2 - Official System Update 2.1")
	if c != "switch 2 official system update 2 1" {
		t.Fatalf("unexpected title normalization: %q", c)
	}
}

func TestCheckSemanticDuplicateCatchesRephrasedAwardsAndEmbeddings(t *testing.T) {
	recent := []RecentStory{
		{
			ID:              1276,
			TitleRaw:        "Shigeru Miyamoto Selected To Join The Academy Of Interactive Arts & Sciences Hall Of Fame",
			TitleNorm:       NormalizeTitleForDedup("Shigeru Miyamoto Selected To Join The Academy Of Interactive Arts & Sciences Hall Of Fame"),
			StoryClusterKey: "miyamoto-aias-hall-fame-legend",
			Embedding:       []float32{0.9, 0.1, 0.0, 0.2},
			Status:          "pending",
		},
	}

	// 1. Rephrased same story caught via story_cluster_key overlap (>= 0.60 with >= 2 shared tokens)
	dupKey, reasonKey := CheckSemanticDuplicate(
		"Nintendo Icon Shigeru Miyamoto Receives AIAS Legend Honour at 2026 Awards - Nintendo Life",
		"",
		"different-hash",
		"miyamoto-aias-legend-award-honour",
		nil,
		recent,
	)
	if !dupKey {
		t.Fatalf("expected cluster key similarity to catch rephrased Miyamoto award story")
	}
	if reasonKey == "" {
		t.Fatal("expected non-empty duplicate reason")
	}

	// 2. Rephrased story with completely different wording caught via cosine similarity >= 0.85
	dupEmb, _ := CheckSemanticDuplicate(
		"Legendary Mario Creator Honoured By Interactive Academy",
		"",
		"other-hash",
		"mario-creator-honoured",
		[]float32{0.91, 0.09, 0.01, 0.19},
		recent,
	)
	if !dupEmb {
		t.Fatalf("expected embedding cosine similarity to catch semantic duplicate")
	}

	// 3. Unrelated Switch 2 DLSS 4 story passes cleanly
	dupOther, _ := CheckSemanticDuplicate(
		"Nintendo Switch 2 DLSS 4 Preset K Discovered in SDK",
		"",
		"dlss-hash",
		"switch2-dlss4-sdk-preset-k",
		[]float32{0.0, 0.0, 1.0, 0.0},
		recent,
	)
	if dupOther {
		t.Fatalf("expected distinct DLSS 4 story NOT to be marked duplicate")
	}
}
