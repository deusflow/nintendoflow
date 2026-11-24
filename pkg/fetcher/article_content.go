package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

const (
	articleContentMaxChars = 4000
	articleContentMinChars = 50
)

// FetchArticleContent downloads the page at rawURL and extracts the main
// textual content. It strips boilerplate chrome, then tries semantic article
// containers before falling back to <main>, <body> paragraphs, and meta description.
// The returned text is trimmed to articleContentMaxChars runes.
func FetchArticleContent(ctx context.Context, rawURL string) (string, error) {
	_, raw, err := FetchPageHTML(ctx, rawURL)
	if err != nil {
		return "", err
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}

	doc.Find("script, style, noscript, nav, footer, aside, .related-posts, .comments, .newsletter, .advertisement").Remove()

	for _, sel := range []string{"[itemprop='articleBody']", "article", ".entry-content", ".article-body", ".post-content", "main"} {
		if text := extractTextFromSelector(doc, sel); text != "" {
			return trimToMaxRunes(text, articleContentMaxChars), nil
		}
	}

	// Fallback: all substantial <p> tags in <body>.
	if text := extractAllParagraphs(doc); text != "" {
		return trimToMaxRunes(text, articleContentMaxChars), nil
	}

	// Final fallback: meta description.
	if desc := extractMetaDescription(doc); desc != "" {
		return desc, nil
	}

	return "", nil
}

func extractTextFromSelector(doc *goquery.Document, selector string) string {
	var parts []string
	doc.Find(selector).First().Find("p").Each(func(_ int, s *goquery.Selection) {
		t := strings.TrimSpace(s.Text())
		if t != "" {
			parts = append(parts, t)
		}
	})
	text := strings.Join(parts, "\n\n")
	if utf8.RuneCountInString(text) < articleContentMinChars {
		return ""
	}
	return text
}

func extractAllParagraphs(doc *goquery.Document) string {
	var parts []string
	doc.Find("body p").Each(func(_ int, s *goquery.Selection) {
		t := strings.TrimSpace(s.Text())
		if len(t) > 20 { // skip very short fragments like nav items
			parts = append(parts, t)
		}
	})
	text := strings.Join(parts, "\n\n")
	if utf8.RuneCountInString(text) < articleContentMinChars {
		return ""
	}
	return text
}

func extractMetaDescription(doc *goquery.Document) string {
	desc, exists := doc.Find(`meta[name="description"]`).First().Attr("content")
	if exists && strings.TrimSpace(desc) != "" {
		return strings.TrimSpace(desc)
	}
	desc, exists = doc.Find(`meta[property="og:description"]`).First().Attr("content")
	if exists && strings.TrimSpace(desc) != "" {
		return strings.TrimSpace(desc)
	}
	return ""
}

func trimToMaxRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	// Trim at last space to avoid cutting words.
	trimmed := string(runes[:maxRunes])
	if i := strings.LastIndex(trimmed, " "); i > maxRunes*3/4 {
		trimmed = trimmed[:i]
	}
	return trimmed + "..."
}
