package dedup

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// HashURL returns a sha256 hex hash of the URL (Layer 1 dedup).
func HashURL(url string) string {
	h := sha256.Sum256([]byte(url))
	return hex.EncodeToString(h[:])
}

// HashTitle returns a normalized sha256 hash for title dedup in DB.
func HashTitle(title string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(title)), " ")
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])
}

// IsDuplicate returns true if title is too similar (Jaccard > 0.65)
// to any title in the recent list (Layer 2 dedup).
func IsDuplicate(title string, recent []string) bool {
	for _, r := range recent {
		if jaccardSimilarity(title, r) > 0.65 {
			return true
		}
	}
	return false
}

func jaccardSimilarity(a, b string) float64 {
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

func tokenSet(s string) map[string]bool {
	set := make(map[string]bool)
	for _, tok := range strings.Fields(strings.ToLower(s)) {
		set[tok] = true
	}
	return set
}
