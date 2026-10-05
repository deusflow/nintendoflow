package dedup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var tokenRe = regexp.MustCompile(`[a-z0-9]+`)

var monthMap = map[string]string{
	"january": "jan", "jan": "jan",
	"february": "feb", "feb": "feb",
	"march": "mar", "mar": "mar",
	"april": "apr", "apr": "apr",
	"may": "may",
	"june": "jun", "jun": "jun",
	"july": "jul", "jul": "jul",
	"august": "aug", "aug": "aug",
	"september": "sep", "sept": "sep", "sep": "sep",
	"october": "oct", "oct": "oct",
	"november": "nov", "nov": "nov",
	"december": "dec", "dec": "dec",
}

var (
	reMonthDay  = regexp.MustCompile(`(?i)\b(january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sep|sept|oct|nov|dec)\.?\s+(\d{1,2})(?:st|nd|rd|th)?\b`)
	reDayMonth  = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)?\s+(?:of\s+)?(january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sep|sept|oct|nov|dec)\b`)
	reSlashDate = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})(?:/\d{2,4})?\b`)
	reISODate   = regexp.MustCompile(`\b\d{4}-(\d{2})-(\d{2})\b`)
)

// ExtractDateMarkers extracts canonical date keys (e.g. "date:oct_4", "date:9/23") from titles.
func ExtractDateMarkers(title string) []string {
	seen := make(map[string]struct{})

	for _, m := range reMonthDay.FindAllStringSubmatch(title, -1) {
		mon := monthMap[strings.ToLower(m[1])]
		day, _ := strconv.Atoi(m[2])
		if mon != "" && day >= 1 && day <= 31 {
			seen[fmt.Sprintf("date:%s_%d", mon, day)] = struct{}{}
		}
	}

	for _, m := range reDayMonth.FindAllStringSubmatch(title, -1) {
		day, _ := strconv.Atoi(m[1])
		mon := monthMap[strings.ToLower(m[2])]
		if mon != "" && day >= 1 && day <= 31 {
			seen[fmt.Sprintf("date:%s_%d", mon, day)] = struct{}{}
		}
	}

	for _, m := range reSlashDate.FindAllStringSubmatch(title, -1) {
		n1, _ := strconv.Atoi(m[1])
		n2, _ := strconv.Atoi(m[2])
		if n1 >= 1 && n1 <= 12 && n2 >= 1 && n2 <= 31 {
			seen[fmt.Sprintf("date:%d/%d", n1, n2)] = struct{}{}
		}
	}

	for _, m := range reISODate.FindAllStringSubmatch(title, -1) {
		n1, _ := strconv.Atoi(m[1])
		n2, _ := strconv.Atoi(m[2])
		if n1 >= 1 && n1 <= 12 && n2 >= 1 && n2 <= 31 {
			seen[fmt.Sprintf("date:%d/%d", n1, n2)] = struct{}{}
		}
	}

	res := make([]string, 0, len(seen))
	for k := range seen {
		res = append(res, k)
	}
	sort.Strings(res)
	return res
}

// HasConflictingDateMarkers returns true if both titles specify dates and their dates are disjoint.
func HasConflictingDateMarkers(t1, t2 string) bool {
	d1 := ExtractDateMarkers(t1)
	d2 := ExtractDateMarkers(t2)
	if len(d1) == 0 || len(d2) == 0 {
		return false
	}
	for _, a := range d1 {
		for _, b := range d2 {
			if a == b {
				return false
			}
		}
	}
	return true
}

var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "but": {},
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"to": {}, "of": {}, "in": {}, "on": {}, "for": {}, "with": {}, "by": {}, "from": {}, "at": {},
	"as": {}, "into": {}, "about": {}, "after": {}, "before": {}, "over": {}, "under": {}, "between": {},
	"against": {}, "than": {}, "then": {},
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

// SemanticSignature generates a signature of significant keywords from a title.
// It prioritizes the longest and rarest substantive tokens instead of slicing alphabetically.
func SemanticSignature(title string) string {
	tokens := normalizeTokens(title)
	dateMarkers := ExtractDateMarkers(title)
	if len(tokens) == 0 && len(dateMarkers) == 0 {
		return "[]"
	}

	// Filter out highly generic words to focus on the unique news aspect.
	genericWords := map[string]bool{
		"nintendo": true, "switch": true, "game": true, "games": true,
		"play": true, "video": true, "console": true, "release": true,
	}

	set := make(map[string]struct{})
	for _, t := range tokens {
		if !genericWords[t] && !isNumericToken(t) {
			set[t] = struct{}{}
		}
	}
	if len(set) == 0 {
		for _, t := range tokens {
			if !isNumericToken(t) {
				set[t] = struct{}{}
			}
		}
	}
	if len(set) == 0 {
		for _, t := range tokens {
			set[t] = struct{}{}
		}
	}

	uniq := make([]string, 0, len(set))
	for tok := range set {
		uniq = append(uniq, tok)
	}

	// Sort by token length descending (longest and rarest tokens first), ties broken alphabetically
	sort.Slice(uniq, func(i, j int) bool {
		if len(uniq[i]) != len(uniq[j]) {
			return len(uniq[i]) > len(uniq[j])
		}
		return uniq[i] < uniq[j]
	})

	// Keep up to 7 most substantive keywords for the signature
	maxTokens := 7
	if len(uniq) > maxTokens {
		uniq = uniq[:maxTokens]
	}

	// Always include date markers so distinct dates in recurring series produce distinct signatures
	for _, dm := range dateMarkers {
		uniq = append(uniq, dm)
	}

	// Sort canonical signature alphabetically
	sort.Strings(uniq)

	return "[" + strings.Join(uniq, ", ") + "]"
}

// HashURL returns a sha256 hex hash of the URL (Layer 1 dedup).
func HashURL(url string) string {
	h := sha256.Sum256([]byte(url))
	return hex.EncodeToString(h[:])
}

// HashTitle returns a normalized sha256 hash for title dedup in DB.
func HashTitle(title string) string {
	sig := SemanticSignature(title)
	h := sha256.Sum256([]byte(sig))
	return hex.EncodeToString(h[:])
}



// IsNearDuplicate returns true when any recent text reaches threshold.
func IsNearDuplicate(text string, recent []string, threshold float64) bool {
	for _, r := range recent {
		if HasConflictingDateMarkers(text, r) {
			continue
		}
		if Similarity(text, r) >= threshold {
			return true
		}
	}
	return false
}

// Similarity is a Jaccard similarity on normalized token sets.
// If two texts have conflicting recurring series dates, they are not duplicates (similarity = 0).
func Similarity(a, b string) float64 {
	if HasConflictingDateMarkers(a, b) {
		return 0.0
	}
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
// Only uses raw titles to avoid language mismatch against Ukrainian body text.
func BuildSimilarityText(title, description string) string {
	return strings.TrimSpace(title)
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
	s = strings.NewReplacer("'", "", "`", "", "’", "", "$", "", "£", "", "€", "", "¥", "").Replace(s)
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
		c := tok[i]
		if (c < '0' || c > '9') && c != 'm' && c != 'k' && c != 'b' {
			return false
		}
	}
	return true
}

var forbiddenClichéIntros = []string{
	"йооой, оце вееесчь",
	"оце так новина",
	"йооой, оце вещь",
	"оце вееесчь",
	"оце вещь",
	"оце так",
	"ну що ж",
	"оце новина",
	"йооой",
}

// HasForbiddenClichéIntro checks if text starts with repetitive cliché intro phrases.
func HasForbiddenClichéIntro(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, phrase := range forbiddenClichéIntros {
		if strings.HasPrefix(lower, phrase) {
			return true
		}
	}
	return false
}

// StripForbiddenIntro strips repetitive cliché intros like "Йооой, оце вееесчь ✨ " from the text.
func StripForbiddenIntro(text string) string {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)
	for _, phrase := range forbiddenClichéIntros {
		if strings.HasPrefix(lower, phrase) {
			cutLen := len([]rune(phrase))
			runes := []rune(trimmed)
			if len(runes) > cutLen {
				rest := strings.TrimSpace(string(runes[cutLen:]))
				rest = strings.TrimLeft(rest, ",.!- \t\n\r✨🌸🎀")
				if len(rest) > 0 {
					runesRest := []rune(rest)
					runesRest[0] = []rune(strings.ToUpper(string(runesRest[0])))[0]
					return string(runesRest)
				}
			}
		}
	}
	return trimmed
}

// IsThreadsIntroDuplicate checks if newText starts with a forbidden cliché or shares the same initial words as a recent Threads post.
func IsThreadsIntroDuplicate(newText string, recentTexts []string) bool {
	if HasForbiddenClichéIntro(newText) {
		return true
	}

	newIntro := extractFirstWords(newText, 4)
	if newIntro == "" {
		return false
	}

	for _, recent := range recentTexts {
		recentIntro := extractFirstWords(recent, 4)
		if recentIntro != "" && strings.EqualFold(newIntro, recentIntro) {
			return true
		}
	}
	return false
}

func extractFirstWords(text string, count int) string {
	words := strings.Fields(strings.ToLower(strings.TrimSpace(text)))
	if len(words) == 0 {
		return ""
	}
	if len(words) > count {
		words = words[:count]
	}
	return strings.Join(words, " ")
}
