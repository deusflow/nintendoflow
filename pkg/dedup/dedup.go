package dedup

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

var tokenRe = regexp.MustCompile(`[a-z0-9]+`)

var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "but": {},
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"to": {}, "of": {}, "in": {}, "on": {}, "for": {}, "with": {}, "by": {}, "from": {}, "at": {},
	"as": {}, "into": {}, "about": {}, "after": {}, "before": {}, "over": {}, "under": {}, "between": {},
	"this": {}, "that": {}, "these": {}, "those": {}, "it": {}, "its": {}, "their": {},
	"now": {}, "live": {}, "new": {}, "latest": {}, "update": {}, "updates": {},
	"report": {}, "reports": {}, "rumor": {}, "rumors": {}, "details": {},
}

var fillerPhrases = []string{
	"now live",
	"full patch notes",
	"heres what changed",
	"here is what changed",
}

// HashURL returns a sha256 hex hash of the URL (Layer 1 dedup).
func HashURL(url string) string {
	h := sha256.Sum256([]byte(url))
	return hex.EncodeToString(h[:])
}

// HashTitle returns a normalized sha256 hash for title dedup in DB.
func HashTitle(title string) string {
	fingerprint := FingerprintText(title)
	h := sha256.Sum256([]byte(fingerprint))
	return hex.EncodeToString(h[:])
}

// IsDuplicate returns true if title is too similar (Jaccard > 0.65)
// to any title in the recent list (Layer 2 dedup).
func IsDuplicate(title string, recent []string) bool {
	return IsNearDuplicate(title, recent, 0.65)
}

// IsNearDuplicate returns true when any recent text reaches threshold.
func IsNearDuplicate(text string, recent []string, threshold float64) bool {
	for _, r := range recent {
		if Similarity(text, r) >= threshold {
			return true
		}
	}
	return false
}

// Similarity is a Jaccard similarity on normalized token sets.
func Similarity(a, b string) float64 {
	setA := tokenSet(a)
	setB := tokenSet(b)
	if len(setA) == 0 && len(setB) == 0 {
		return 1.0
	}
	intersection := 0
	for tok := range setA {
		if setB[tok] {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// FingerprintText builds a stable bag-of-words fingerprint.
func FingerprintText(s string) string {
	tokens := normalizeTokens(s)
	if len(tokens) == 0 {
		return ""
	}
	set := make(map[string]struct{}, len(tokens))
	for _, tok := range tokens {
		set[tok] = struct{}{}
	}
	uniq := make([]string, 0, len(set))
	for tok := range set {
		uniq = append(uniq, tok)
	}
	sort.Strings(uniq)
	return strings.Join(uniq, " ")
}

// BuildSimilarityText normalizes text used for near-duplicate checks.
func BuildSimilarityText(title, description string) string {
	// Duplicate title once to weight headline intent over noisy body tails.
	return strings.TrimSpace(FingerprintText(title + " " + title + " " + description))
}

// ThresholdForSourceType returns duplicate sensitivity by feed type.
// Lower threshold => stricter duplicate suppression.
func ThresholdForSourceType(sourceType string) float64 {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "aggregator":
		return 0.55
	case "official":
		return 0.78
	case "insider":
		return 0.68
	default:
		return 0.64
	}
}

func tokenSet(s string) map[string]bool {
	set := make(map[string]bool)
	for _, tok := range normalizeTokens(s) {
		set[tok] = true
	}
	return set
}

func normalizeTokens(s string) []string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("'", "", "`", "", "’", "").Replace(s)
	for _, phrase := range fillerPhrases {
		s = strings.ReplaceAll(s, phrase, " ")
	}
	raw := tokenRe.FindAllString(s, -1)
	if len(raw) == 0 {
		return nil
	}
	tokens := make([]string, 0, len(raw))
	for _, tok := range raw {
		if _, skip := stopwords[tok]; skip {
			continue
		}
		if len(tok) < 2 && !isNumericToken(tok) {
			continue
		}
		tokens = append(tokens, tok)
	}
	return tokens
}

func isNumericToken(tok string) bool {
	if tok == "" {
		return false
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] < '0' || tok[i] > '9' {
			return false
		}
	}
	return true
}
