package scorer

import (
	"strings"

	"github.com/deuswork/nintendoflow/pkg/config"
)

type Result struct {
	Score         int
	TechScore     int
	HasAnchor     bool
	HasComparison bool
}

const strictFeedHighTechBypassScore = 240

// Evaluate computes relevance from topics. Disabled topics are skipped.
// Each keyword's effective weight = keyword.Weight * topic.Priority / 100.
func Evaluate(title, body string, topics map[string]config.Topic) Result {
	result := Result{}
	text := strings.ToLower(title + " " + body)

	for _, topic := range topics {
		if !topic.Enabled {
			continue
		}
		priority := topic.Priority
		if priority == 0 {
			priority = 100
		}
		for _, kw := range topic.Keywords {
			word := strings.TrimSpace(strings.ToLower(kw.Word))
			if word == "" {
				continue
			}
			if strings.Contains(text, word) {
				effective := kw.Weight * priority / 100
				result.Score += effective
				switch strings.ToLower(strings.TrimSpace(kw.Role)) {
				case "anchor":
					result.HasAnchor = true
				case "comparison":
					result.HasComparison = true
				case "tech":
					result.TechScore += effective
				}
			}
		}
	}
	return result
}

// ShouldPost applies the final policy gate:
//  1. article must reach minScore
//  2. if the feed requires an explicit Nintendo anchor, an anchor must be present
//     unless the article has a very high technical signal score.
func ShouldPost(title, body string, topics map[string]config.Topic, minScore int, requireAnchor bool) (Result, bool, string) {
	result := Evaluate(title, body, topics)
	if result.Score < minScore {
		return result, false, "below_min_score"
	}
	if requireAnchor && !result.HasAnchor {
		if result.TechScore >= strictFeedHighTechBypassScore {
			return result, true, "accepted_via_high_tech"
		}
		return result, false, "missing_nintendo_anchor"
	}
	return result, true, "accepted"
}
