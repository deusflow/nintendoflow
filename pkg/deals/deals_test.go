package deals

import (
	"strings"
	"testing"
)

func TestDealScorePrioritizesEurope(t *testing.T) {
	euDeal := Deal{
		Title:    "Zelda EU",
		OldPrice: 60.0,
		NewPrice: 30.0,
		Currency: "€",
		Region:   "EU",
		Cut:      50,
	}

	usDeal := Deal{
		Title:    "Zelda US",
		OldPrice: 60.0,
		NewPrice: 30.0,
		Currency: "$",
		Region:   "US",
		Cut:      50,
	}

	euScore := dealScore(euDeal)
	usScore := dealScore(usDeal)

	if euScore <= usScore {
		t.Fatalf("expected EU deal score (%f) to be strictly greater than US deal score (%f)", euScore, usScore)
	}
}

func TestCleanRedditQuote(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "[eShop/US] Super Mario Odyssey - $39.99 (33% off)",
			expected: "Super Mario Odyssey (33% off)",
		},
		{
			input:    "[US] Persona 5 Royal is 50% off right now!",
			expected: "Persona 5 Royal is 50% off right now!",
		},
		{
			input:    "💬 [eShop / NA] Great game for this price.",
			expected: "Great game for this price",
		},
		{
			input:    "Worth every penny at 29.99.",
			expected: "Worth every penny at 29.99",
		},
	}

	for _, tc := range tests {
		got := CleanRedditQuote(tc.input)
		if !strings.Contains(got, tc.expected) && got != tc.expected {
			t.Errorf("CleanRedditQuote(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
		if strings.Contains(got, "$") {
			t.Errorf("CleanRedditQuote should strip dollar prices, got: %q", got)
		}
		if strings.Contains(strings.ToLower(got), "[eshop") {
			t.Errorf("CleanRedditQuote should strip bracket tags, got: %q", got)
		}
	}
}
