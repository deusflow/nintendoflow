package fetcher

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
)

const (
	articleContentMaxChars = 1500
	articleContentMinChars = 50
)

// FetchArticleContent downloads the page at rawURL and extracts the main
// textual content. It tries <article> first, falls back to <main>, then to all
// <p> tags, and finally to the meta description.
// The returned text is trimmed to articleContentMaxChars runes.
func FetchArticleContent(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	cfg := NewScraperConfig(15 * time.Second)
	req = PrepareScraperRequest(req, cfg)

	client := NewScraperClient(cfg)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}

	// Strategy 1: <article> tag — most news sites wrap main content here.
	if text := extractTextFromSelector(doc, "article"); text != "" {
		return trimToMaxRunes(text, articleContentMaxChars), nil
	}

	// Strategy 2: <main> tag.
	if text := extractTextFromSelector(doc, "main"); text != "" {
		return trimToMaxRunes(text, articleContentMaxChars), nil
	}

	// Strategy 3: all <p> tags in <body>.
	if text := extractAllParagraphs(doc); text != "" {
		return trimToMaxRunes(text, articleContentMaxChars), nil
	}

	// Strategy 4: meta description.
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
