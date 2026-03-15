package scorer

import (
	"strings"

	"github.com/deuswork/nintendoflow/internal/config"
)

type Result struct {
	Score         int
	HasAnchor     bool
	HasComparison bool
}

// Evaluate computes relevance from YAML-provided keyword weights and tracks
// whether the text contains Nintendo anchors and comparison/performance terms.
func Evaluate(title, body string, keywords []config.Keyword) Result {
	result := Result{}
	text := strings.ToLower(title + " " + body)

	for _, kw := range keywords {
		word := strings.TrimSpace(strings.ToLower(kw.Word))
		if word == "" {
			continue
		}
		if strings.Contains(text, word) {
			result.Score += kw.Weight
			switch strings.ToLower(strings.TrimSpace(kw.Role)) {
			case "anchor":
				result.HasAnchor = true
			case "comparison":
				result.HasComparison = true
			}
		}
	}
	return result
}

// ShouldPost applies the final policy gate:
//  1. article must reach minScore
//  2. if the feed requires an explicit Nintendo anchor, an anchor must be present
func ShouldPost(title, body string, keywords []config.Keyword, minScore int, requireAnchor bool) (Result, bool, string) {
	result := Evaluate(title, body, keywords)
	if result.Score < minScore {
		return result, false, "below_min_score"
	}
	if requireAnchor && !result.HasAnchor {
		return result, false, "missing_nintendo_anchor"
	}
	return result, true, "accepted"
}
