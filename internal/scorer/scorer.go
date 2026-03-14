package scorer

import "strings"

// MinScoreThreshold is the minimum score an article must have to be processed.
const MinScoreThreshold = 4

var rules = []struct {
	keywords []string
	score    int
}{
	{[]string{"nintendo direct", "direct mini", "partner direct"}, 10},
	{[]string{"switch 2", "nintendo switch 2"}, 10},
	{[]string{"announced", "reveal", "new game", "launch", "release date"}, 8},
	{[]string{"free-to-play", "f2p", "free to play", "free game"}, 7},
	{[]string{"update", "patch", "dlc", "expansion"}, 5},
	{[]string{"rumor", "leak", "insider", "report"}, 4},
	{[]string{"sale", "discount", "eshop sale"}, 2},
	// negative signals
	{[]string{"top 10", "best games of", "tier list"}, -5},
	{[]string{"our review", "we reviewed"}, -3},
	{[]string{"guide", "how to", "tips and tricks"}, -4},
}

// ScoreArticle returns a relevance score for an article.
func ScoreArticle(title, body, sourceType string) int {
	score := 0
	text := strings.ToLower(title + " " + body)
	for _, r := range rules {
		for _, kw := range r.keywords {
			if strings.Contains(text, kw) {
				score += r.score
				break
			}
		}
	}
	switch sourceType {
	case "official":
		score += 5
	case "insider":
		score += 2
	}
	return score
}
