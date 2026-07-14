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
	Skip         bool   `json:"skip"`
	Type         string `json:"type"`
	TelegramHTML string `json:"telegram_html"`
	ThreadsText  string `json:"threads_text"`
}

// ParseJSONPost extracts the JSON block from the text and parses it.
// It handles potential markdown formatting blocks like ```json ... ```.
func ParseJSONPost(text string) (GeneratedPost, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end < start {
		// Attempt fallback parsing if JSON is completely missing but "SKIP" is there
		if strings.Contains(strings.ToUpper(text), "SKIP") {
			return GeneratedPost{Skip: true}, nil
		}
		return GeneratedPost{}, json.Unmarshal([]byte(text), &GeneratedPost{}) // return parsing error
	}
	
	jsonStr := text[start : end+1]
	var post GeneratedPost
	err := json.Unmarshal([]byte(jsonStr), &post)
	if err != nil {
		return GeneratedPost{}, err
	}
	
	post.Type = NormalizeArticleType(post.Type)
	return post, nil
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
