package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var jsonLDDateFieldRe = regexp.MustCompile(`"(?:datePublished|dateCreated|uploadDate|dateModified)"\s*:\s*"([^"]+)"`)

// FetchSourcePublishedAt loads an article page and tries to extract its source
// publication date from common HTML meta fields, JSON-LD and <time> tags.
func FetchSourcePublishedAt(ctx context.Context, sourceURL string) (*time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; NintendoNewsBot/1.0)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}

	// Prefer explicit publication fields; fall back to broader date fields.
	publicationSelectors := []string{
		`meta[property="article:published_time"]`,
		`meta[property="og:published_time"]`,
		`meta[name="article:published_time"]`,
		`meta[name="parsely-pub-date"]`,
		`meta[itemprop="datePublished"]`,
		`meta[name="pubdate"]`,
		`time[itemprop="datePublished"]`,
		`time.published`,
	}
	if t := firstParsedTime(doc, publicationSelectors, "content", "datetime"); t != nil {
		return t, nil
	}

	if t := firstParsedJSONLDTime(raw); t != nil {
		return t, nil
	}

	fallbackSelectors := []string{
		`meta[property="article:modified_time"]`,
		`meta[property="og:updated_time"]`,
		`meta[itemprop="dateModified"]`,
		`time[datetime]`,
	}
	if t := firstParsedTime(doc, fallbackSelectors, "content", "datetime"); t != nil {
		return t, nil
	}

	return nil, nil
}

func firstParsedTime(doc *goquery.Document, selectors []string, attrs ...string) *time.Time {
	for _, selector := range selectors {
		sel := doc.Find(selector).First()
		for _, attr := range attrs {
			if raw, ok := sel.Attr(attr); ok {
				if t, ok := parseFlexibleTime(raw); ok {
					return &t
				}
			}
		}
		if raw := strings.TrimSpace(sel.Text()); raw != "" {
			if t, ok := parseFlexibleTime(raw); ok {
				return &t
			}
		}
	}
	return nil
}

func firstParsedJSONLDTime(raw []byte) *time.Time {
	matches := jsonLDDateFieldRe.FindAllSubmatch(raw, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		if t, ok := parseFlexibleTime(string(m[1])); ok {
			return &t
		}
	}
	return nil
}

func parseFlexibleTime(raw string) (time.Time, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return time.Time{}, false
	}

	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05-0700",
		"2006-01-02 15:04:05-0700",
		"2006-01-02 15:04:05",
		"2006-01-02",
		time.RFC1123,
		time.RFC1123Z,
		time.RFC822,
		time.RFC822Z,
		time.RFC850,
		"Mon, 02 Jan 2006 15:04:05 MST",
	}

	for _, layout := range layouts {
		t, err := time.Parse(layout, v)
		if err == nil {
			t = t.UTC()
			return t, true
		}
	}

	return time.Time{}, false
}
