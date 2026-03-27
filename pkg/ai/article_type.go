package ai

import "strings"

const (
	ArticleTypeInsight = "insight"
	ArticleTypeRumor   = "rumor"
	ArticleTypeNews    = "news"
	ArticleTypeOfftop  = "offtop"
)

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
