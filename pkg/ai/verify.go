package ai

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	thinkBlockRe   = regexp.MustCompile(`(?is)<think>.*?</think>`)
	htmlTagRe      = regexp.MustCompile(`(?s)<[^>]*>`)
	urlRe          = regexp.MustCompile(`https?://[^\s<>"']+`)
	numTokenRe     = regexp.MustCompile(`\d+(?:[.,\s\x{00a0}]\d+)*%?`)
	russianOnlyRe  = regexp.MustCompile(`[ыэёъЫЭЁЪ]`)
	russianCalques = []string{
		"в том числе",
		"по данным",
		"как сообщает",
		"на данный момент",
		"в настоящее время",
		"тем не менее",
		"среди прочего",
	}
	stubPhrases = []string{
		"на жаль, я не можу",
		"на жаль, у мене немає доступу",
		"я не маю доступу",
		"стаття недоступна",
		"повний текст недоступний",
		"неможливо перевірити",
		"без повного тексту",
		"надайте повний текст",
		"i cannot access",
		"i am unable to",
		"i'm sorry, i cannot",
		"please provide the full",
		"lorem ipsum",
	}
)

var englishNumberWords = map[string]string{
	"ten": "10", "eleven": "11", "twelve": "12", "thirteen": "13",
	"fourteen": "14", "fifteen": "15", "sixteen": "16", "seventeen": "17",
	"eighteen": "18", "nineteen": "19", "twenty": "20", "thirty": "30",
	"forty": "40", "fifty": "50", "sixty": "60", "seventy": "70",
	"eighty": "80", "ninety": "90", "hundred": "100", "thousand": "1000",
}

// SanitizeJSON removes reasoning <think> tags, Markdown fences, and leading/trailing prose
// to extract the primary JSON object or array payload.
func SanitizeJSON(raw string) string {
	cleaned := thinkBlockRe.ReplaceAllString(raw, "")
	cleaned = strings.TrimSpace(cleaned)

	// Strip ```json ... ``` or ``` ... ``` fences anywhere around the payload.
	if idx := strings.Index(cleaned, "```"); idx != -1 {
		afterStart := cleaned[idx+3:]
		if nl := strings.IndexByte(afterStart, '\n'); nl != -1 && nl <= 15 {
			afterStart = afterStart[nl+1:]
		}
		if endIdx := strings.LastIndex(afterStart, "```"); endIdx != -1 {
			cleaned = strings.TrimSpace(afterStart[:endIdx])
		}
	}

	if obj := extractBalancedJSON(cleaned, '{', '}'); obj != "" {
		return obj
	}
	if arr := extractBalancedJSON(cleaned, '[', ']'); arr != "" {
		return arr
	}
	return cleaned
}

func extractBalancedJSON(s string, openCh, closeCh byte) string {
	start := strings.IndexByte(s, openCh)
	if start == -1 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == openCh {
			depth++
		} else if c == closeCh {
			depth--
			if depth == 0 {
				return strings.TrimSpace(s[start : i+1])
			}
		}
	}
	return ""
}

// IsStubOrApology reports whether AI output looks like a refusal, apology, or placeholder stub.
func IsStubOrApology(text string) bool {
	plain := stripTagsAndURLs(text)
	trimmed := strings.TrimSpace(plain)
	if len([]rune(trimmed)) < 40 {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, phrase := range stubPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// VerifyUkrainian validates that generated post text is genuine Ukrainian without Russian-specific
// characters, common Russian multi-word phrases, or AI apology stubs.
func VerifyUkrainian(text string) error {
	if IsStubOrApology(text) {
		return fmt.Errorf("generated text is empty or stub/apology")
	}
	plain := stripTagsAndURLs(text)
	if m := russianOnlyRe.FindString(plain); m != "" {
		return fmt.Errorf("russian-only character %q detected in Ukrainian output", m)
	}
	lower := strings.ToLower(plain)
	for _, phrase := range russianCalques {
		if strings.Contains(lower, phrase) {
			return fmt.Errorf("russian phrase %q detected in Ukrainian output", phrase)
		}
	}
	cyrillicCount := 0
	for _, r := range plain {
		if unicode.In(r, unicode.Cyrillic) {
			cyrillicCount++
		}
	}
	if cyrillicCount < 15 {
		return fmt.Errorf("insufficient Ukrainian text (only %d Cyrillic chars)", cyrillicCount)
	}
	return nil
}

// VerifyNumbers checks that multi-digit numbers, decimals, and percentages in generatedText
// are grounded in sourceText to catch hallucinated prices, dates, or figures.
// Single-digit whole numbers (1-9) without decimals or '%' are allowed stylistically.
func VerifyNumbers(sourceText, generatedText string) error {
	sourceTokens := extractNumericSignatures(stripTagsAndURLs(sourceText), true)
	genTokens := extractNumericSignatures(stripTagsAndURLs(generatedText), false)

	for _, tok := range genTokens {
		if tok.isStylisticSingleDigit {
			continue
		}
		if _, ok := sourceTokens[tok.canonical]; !ok {
			// Also allow plain integer matching base of a percentage/decimal when present in source.
			if tok.rawNoPct != "" {
				if _, okBase := sourceTokens[tok.rawNoPct]; okBase {
					continue
				}
			}
			return fmt.Errorf("unverified number %q in generated output not found in source", tok.original)
		}
	}
	return nil
}

type numericSig struct {
	original               string
	canonical              string
	rawNoPct               string
	isStylisticSingleDigit bool
}

func extractNumericSignatures(text string, includeWords bool) map[string]numericSig {
	out := make(map[string]numericSig)
	matches := numTokenRe.FindAllString(text, -1)
	for _, raw := range matches {
		sig, ok := normalizeNumericToken(raw)
		if !ok {
			continue
		}
		out[sig.canonical] = sig
		if sig.rawNoPct != "" {
			out[sig.rawNoPct] = numericSig{original: raw, canonical: sig.rawNoPct}
		}
	}
	if includeWords {
		words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
			return !unicode.IsLetter(r)
		})
		for _, w := range words {
			if val, ok := englishNumberWords[w]; ok {
				out[val] = numericSig{original: w, canonical: val}
			}
		}
	}
	return out
}

func normalizeNumericToken(raw string) (numericSig, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return numericSig{}, false
	}
	isPct := strings.HasSuffix(trimmed, "%")
	numPart := strings.TrimSuffix(trimmed, "%")
	// Remove thousand-separator spaces / non-breaking spaces.
	numPart = strings.ReplaceAll(numPart, " ", "")
	numPart = strings.ReplaceAll(numPart, "\u00a0", "")
	if numPart == "" {
		return numericSig{}, false
	}

	// Distinguish thousand separators ("50.000", "50,000") vs decimals ("4.5", "4,5").
	// If groups after the last '.' or ',' have length != 3 (or multiple separators), normalize accordingly.
	canonicalNum := normalizeSeparators(numPart)
	if canonicalNum == "" {
		return numericSig{}, false
	}

	// Single-digit whole numbers without '%' or '.' are treated as stylistic.
	isSingleDigit := !isPct && !strings.Contains(canonicalNum, ".")
	if isSingleDigit {
		if v, err := strconv.Atoi(canonicalNum); err == nil && v >= 0 && v <= 9 {
			return numericSig{
				original:               trimmed,
				canonical:              canonicalNum,
				isStylisticSingleDigit: true,
			}, true
		}
	}

	if isPct {
		return numericSig{
			original:  trimmed,
			canonical: canonicalNum + "%",
			rawNoPct:  canonicalNum,
		}, true
	}
	return numericSig{
		original:  trimmed,
		canonical: canonicalNum,
	}, true
}

func normalizeSeparators(s string) string {
	// If both ',' and '.' appear, treat the last one with <3 trailing digits as decimal.
	dotCount := strings.Count(s, ".")
	commaCount := strings.Count(s, ",")
	if dotCount == 0 && commaCount == 0 {
		return s
	}
	if dotCount+commaCount > 1 {
		// Strip all grouping commas/dots except a final decimal part with length != 3.
		lastDot := strings.LastIndexByte(s, '.')
		lastComma := strings.LastIndexByte(s, ',')
		lastSep := lastDot
		if lastComma > lastSep {
			lastSep = lastComma
		}
		tail := s[lastSep+1:]
		if len(tail) == 3 {
			s = strings.ReplaceAll(s, ".", "")
			return strings.ReplaceAll(s, ",", "")
		}
		head := strings.ReplaceAll(s[:lastSep], ".", "")
		head = strings.ReplaceAll(head, ",", "")
		return strings.TrimRight(head+"."+tail, "0.")
	}

	sep := "."
	if commaCount == 1 {
		sep = ","
	}
	parts := strings.SplitN(s, sep, 2)
	if len(parts) != 2 {
		return s
	}
	// If single separator followed by exactly 3 digits and head is non-zero (e.g., 50.000 or 1,200),
	// assume thousand grouping unless head is 0 (0.125).
	if len(parts[1]) == 3 && parts[0] != "0" {
		return parts[0] + parts[1]
	}
	norm := parts[0] + "." + parts[1]
	if f, err := strconv.ParseFloat(norm, 64); err == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return norm
}

func stripTagsAndURLs(s string) string {
	noTags := htmlTagRe.ReplaceAllString(s, " ")
	return urlRe.ReplaceAllString(noTags, " ")
}
