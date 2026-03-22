package fetcher

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var youtubeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// FetchOGImage loads page HTML and returns the og:image content if present.
func FetchOGImage(ctx context.Context, sourceURL string) (string, error) {
	_, imageURL, err := FetchPreferredMedia(ctx, sourceURL)
	if err != nil {
		return "", err
	}
	return imageURL, nil
}

// FetchPreferredMedia loads page HTML and returns preferred media URLs.
// Priority: YouTube video first, then og:image.
func FetchPreferredMedia(ctx context.Context, sourceURL string) (videoURL string, imageURL string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; NintendoNewsBot/1.0)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("http get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("http status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("parse html: %w", err)
	}

	if yt := extractYouTubeURL(doc, sourceURL); yt != "" {
		return yt, "", nil
	}

	imageURL, exists := doc.Find(`meta[property="og:image"]`).First().Attr("content")
	if !exists {
		return "", "", nil
	}
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return "", "", nil
	}

	base, err := url.Parse(sourceURL)
	if err != nil {
		return "", imageURL, nil
	}
	rel, err := url.Parse(imageURL)
	if err != nil {
		return "", imageURL, nil
	}
	return "", base.ResolveReference(rel).String(), nil
}

func extractYouTubeURL(doc *goquery.Document, sourceURL string) string {
	if doc == nil {
		return ""
	}
	selectors := []string{
		`meta[property="og:video:url"]`,
		`meta[property="og:video"]`,
		`meta[name="twitter:player"]`,
		`iframe[src*="youtube.com"]`,
		`iframe[src*="youtu.be"]`,
		`a[href*="youtube.com/watch"]`,
		`a[href*="youtu.be/"]`,
	}
	for _, selector := range selectors {
		attr := "content"
		if strings.HasPrefix(selector, "iframe") {
			attr = "src"
		}
		if strings.HasPrefix(selector, "a[") {
			attr = "href"
		}
		raw, ok := doc.Find(selector).First().Attr(attr)
		if !ok {
			continue
		}
		if normalized := normalizeYouTubeURL(raw, sourceURL); normalized != "" {
			return normalized
		}
	}
	return ""
}

func normalizeYouTubeURL(raw, pageURL string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	base, _ := url.Parse(pageURL)
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}

	host := strings.ToLower(u.Hostname())
	if !strings.Contains(host, "youtube.com") && host != "youtu.be" {
		return ""
	}

	var id string
	if host == "youtu.be" {
		id = strings.Trim(strings.TrimSpace(u.Path), "/")
	} else {
		qID := strings.TrimSpace(u.Query().Get("v"))
		if qID != "" {
			id = qID
		} else {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) >= 2 {
				switch parts[0] {
				case "embed", "shorts", "live":
					id = parts[1]
				}
			}
		}
	}

	id = strings.Trim(path.Clean("/"+id), "/")
	if !youtubeIDPattern.MatchString(id) {
		return ""
	}

	return "https://www.youtube.com/watch?v=" + id
}
