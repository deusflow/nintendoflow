package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var (
	jsonLDDateFieldRe = regexp.MustCompile(`"(?:datePublished|dateCreated|uploadDate|dateModified)"\s*:\s*"([^"]+)"`)
	asianDateRe       = regexp.MustCompile(`(\d{4})年\s*(\d{1,2})月\s*(\d{1,2})日(?:\s*(\d{1,2}):(\d{1,2})(?::(\d{1,2}))?)?`)
)

// FetchSourcePublishedAt loads an article page and tries to extract its source
// publication date from common HTML meta fields, JSON-LD and <time> tags.
// If the given sourceURL is a Google News URL, it resolves it to the destination
// publisher site first.
func FetchSourcePublishedAt(ctx context.Context, sourceURL string) (*time.Time, error) {
	targetURL := sourceURL
	if IsGoogleNewsURL(targetURL) {
		resolved, err := ResolveGoogleNewsURL(ctx, redirectHTTPClient, targetURL)
		if err == nil && resolved != "" && !IsGoogleNewsURL(resolved) {
			targetURL = resolved
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	cfg := NewScraperConfig(15 * time.Second)
	req = PrepareScraperRequest(req, cfg)

	client := NewScraperClient(cfg)
	resp, err := client.Do(req)
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
		`time[datetime]`,
		`time`,
		`span.date`,
		`span.post-date`,
		`span.entry-date`,
		`div.date`,
		`div.post-date`,
		`.article-date`,
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
	}
	if t := firstParsedTime(doc, fallbackSelectors, "content", "datetime"); t != nil {
		return t, nil
	}

	return nil, nil
}

func firstParsedTime(doc *goquery.Document, selectors []string, attrs ...string) *time.Time {
	for _, selector := range selectors {
		var found *time.Time
		doc.Find(selector).EachWithBreak(func(i int, sel *goquery.Selection) bool {
			for _, attr := range attrs {
				if raw, ok := sel.Attr(attr); ok {
					if t, ok := parseFlexibleTime(raw); ok {
						found = &t
						return false
					}
				}
			}
			if raw := strings.TrimSpace(sel.Text()); raw != "" {
				if t, ok := parseFlexibleTime(raw); ok {
					found = &t
					return false
				}
			}
			return true
		})
		if found != nil {
			return found
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
		"2006年01月02日",
		"2006年1月2日",
		"2006年01月02日 15:04:05",
		"2006年01月02日 15:04",
		"2006年1月2日 15:04:05",
		"2006年1月2日 15:04",
	}

	for _, layout := range layouts {
		t, err := time.Parse(layout, v)
		if err == nil {
			t = t.UTC()
			return t, true
		}
	}

	// Try extracting Asian date pattern: 2026年10月03日
	if m := asianDateRe.FindStringSubmatch(v); len(m) >= 4 {
		year, errY := strconv.Atoi(m[1])
		month, errM := strconv.Atoi(m[2])
		day, errD := strconv.Atoi(m[3])
		if errY == nil && errM == nil && errD == nil && month >= 1 && month <= 12 && day >= 1 && day <= 31 {
			hour, min, sec := 0, 0, 0
			if len(m) >= 6 && m[4] != "" && m[5] != "" {
				hour, _ = strconv.Atoi(m[4])
				min, _ = strconv.Atoi(m[5])
				if len(m) >= 7 && m[6] != "" {
					sec, _ = strconv.Atoi(m[6])
				}
			}
			return time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC), true
		}
	}

	return time.Time{}, false
}
