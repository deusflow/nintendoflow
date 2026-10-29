package scorer

import (
	"strings"

	"github.com/deuswork/nintendoflow/pkg/config"
)

type Result struct {
	Score          int
	TechScore      int
	HasAnchor      bool
	HasComparison  bool
	MustPublish    bool
	EventTag       string
	MatchedTopics  map[string]bool
	WeirdnessScore int
}

const strictFeedHighTechBypassScore = 240

// Evaluate computes relevance from topics. Disabled topics are skipped.
// Each keyword's effective weight = keyword.Weight * topic.Priority / 100.
func Evaluate(title, body string, topics map[string]config.Topic) Result {
	result := Result{MatchedTopics: make(map[string]bool)}
	text := strings.ToLower(title + " " + body)

	for topicName, topic := range topics {
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
			if containsKeyword(text, word) {
				effective := kw.Weight * priority / 100
				result.Score += effective
				result.MatchedTopics[topicName] = true
				switch strings.ToLower(strings.TrimSpace(kw.Role)) {
				case "anchor":
					result.HasAnchor = true
				case "comparison":
					result.HasComparison = true
				case "tech":
					result.TechScore += effective
				case "must_publish":
					result.MustPublish = true
					result.HasAnchor = true
					if kw.EventTag != "" {
						result.EventTag = kw.EventTag
					}
				}
			}
		}
	}

	// Calculate Weirdness Score (Idea 6)
	// If an article hits multiple completely different contexts (e.g. business + tech + anchor)
	if len(result.MatchedTopics) >= 3 {
		result.WeirdnessScore = len(result.MatchedTopics) * 10
	}

	return result
}

// ShouldPost applies the final policy gate:
//  1. article must reach minScore
//  2. if the feed requires an explicit Nintendo anchor, an anchor must be present
//     unless the article has a very high technical signal score.
func ShouldPost(title, body string, topics map[string]config.Topic, minScore int, requireAnchor bool) (Result, bool, string) {
	result := Evaluate(title, body, topics)
	if result.MustPublish {
		return result, true, "must_publish_event"
	}
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

func isWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// containsKeyword checks if word appears in text with proper word boundaries
// so short acronyms like "nes" or "nso" never match inside "business", "wednesday", or "johnson".
// Words of 4+ chars also match simple plural suffixes ("s", "es").
func containsKeyword(text, word string) bool {
	if word == "" || len(text) < len(word) {
		return false
	}
	offset := 0
	for offset <= len(text)-len(word) {
		idx := strings.Index(text[offset:], word)
		if idx == -1 {
			return false
		}
		pos := offset + idx
		end := pos + len(word)

		leftOK := pos == 0 || !isWordByte(text[pos-1])
		if leftOK {
			if end == len(text) || !isWordByte(text[end]) {
				return true
			}
			// Allow simple English plural (-s / -es) for keywords of 4+ characters
			if len(word) >= 4 {
				if text[end] == 's' && (end+1 == len(text) || !isWordByte(text[end+1])) {
					return true
				}
				if strings.HasPrefix(text[end:], "es") && (end+2 == len(text) || !isWordByte(text[end+2])) {
					return true
				}
			}
		}
		offset = pos + 1
	}
	return false
}

