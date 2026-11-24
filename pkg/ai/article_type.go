package ai

import (
	"encoding/json"
	"strings"
)

const (
	ArticleTypeInsight = "insight"
	ArticleTypeRumor   = "rumor"
	ArticleTypeNews    = "news"
	ArticleTypeOfftop  = "offtop"
)

type GeneratedPost struct {
	Skip            bool   `json:"skip"`
	Reason          string `json:"reason,omitempty"`
	Type            string `json:"type"`
	Category        string `json:"category,omitempty"`
	StoryClusterKey string `json:"story_cluster_key,omitempty"`
	ConcreteAnchor  string `json:"concrete_anchor,omitempty"`
	AudienceScore   int    `json:"audience_score,omitempty"`
	TelegramHTML    string `json:"telegram_html"`
	ThreadsText     string `json:"threads_text"`
}

// ParseJSONPost extracts the JSON block from the text and parses it.
// It handles reasoning tags, markdown formatting blocks like ```json ... ```, and trailing prose.
func ParseJSONPost(text string) (GeneratedPost, error) {
	cleaned := SanitizeJSON(text)
	if !strings.HasPrefix(cleaned, "{") {
		// Attempt fallback parsing if JSON is completely missing but "SKIP" is there
		if strings.Contains(strings.ToUpper(text), "SKIP") {
			return GeneratedPost{Skip: true, Reason: "model output contains SKIP without JSON"}, nil
		}
		return GeneratedPost{}, json.Unmarshal([]byte(cleaned), &GeneratedPost{}) // return parsing error
	}

	var post GeneratedPost
	err := json.Unmarshal([]byte(cleaned), &post)
	if err != nil {
		return GeneratedPost{}, err
	}

	post.Type = NormalizeArticleType(post.Type)
	post.Category = NormalizeCategory(post.Category)
	post.StoryClusterKey = normalizeClusterKey(post.StoryClusterKey)
	post.ConcreteAnchor = strings.TrimSpace(post.ConcreteAnchor)
	return post, nil
}

// NormalizeCategory constrains AI-emitted categories to canonical taxonomy buckets.
func NormalizeCategory(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "company", "business", "corporate", "finance", "financial":
		return "company"
	case "tech", "hardware", "technology", "specs", "silicon":
		return "tech"
	case "legal", "lawsuit", "ip", "patent", "patents", "piracy":
		return "legal"
	case "ecosystem", "nso", "firmware", "system", "eshop", "service":
		return "ecosystem"
	case "culture", "movie", "film", "museum", "theme_park", "merch", "music":
		return "culture"
	case "direct", "event", "showcase", "announcement":
		return "direct"
	case "games", "game", "gaming", "release":
		return "games"
	default:
		return ""
	}
}

func normalizeClusterKey(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// ParseTypedPost extracts optional TYPE: marker and returns (type, body).
// If marker is missing or invalid, type defaults to news and text is returned as-is.
func ParseTypedPost(text string) (string, string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ArticleTypeNews, ""
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 {
		return ArticleTypeNews, trimmed
	}

	first := strings.TrimSpace(lines[0])
	lower := strings.ToLower(first)
	if !strings.HasPrefix(lower, "type:") && !strings.HasPrefix(lower, "тип:") {
		return ArticleTypeNews, trimmed
	}

	rawType := strings.TrimSpace(first[strings.Index(first, ":")+1:])
	articleType := NormalizeArticleType(rawType)
	body := strings.TrimSpace(strings.Join(lines[1:], "\n"))
	if body == "" {
		return articleType, trimmed
	}
	return articleType, body
}

func NormalizeArticleType(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.Trim(normalized, " .,!?:;\"'()[]{}")

	switch normalized {
	case "insight", "инсайт", "інсайт":
		return ArticleTypeInsight
	case "rumor", "rumour", "слух", "слухи", "чутка", "чутки":
		return ArticleTypeRumor
	case "offtop", "off-topic", "offtopic", "оффтоп", "офтоп":
		return ArticleTypeOfftop
	case "news", "fact", "новость", "новина", "факт":
		return ArticleTypeNews
	default:
		return ArticleTypeNews
	}
}
