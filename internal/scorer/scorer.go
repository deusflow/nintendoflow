package scorer

import (
	"strings"

	"github.com/deuswork/nintendoflow/internal/config"
)

// Score computes relevance from YAML-provided keyword weights.
func Score(title, body string, keywords []config.Keyword) int {
	score := 0
	text := strings.ToLower(title + " " + body)

	for _, kw := range keywords {
		word := strings.TrimSpace(strings.ToLower(kw.Word))
		if word == "" {
			continue
		}
		if strings.Contains(text, word) {
			score += kw.Weight
		}
	}
	return score
}
