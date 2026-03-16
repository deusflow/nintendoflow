package fetcher

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// FetchOGImage loads page HTML and returns the og:image content if present.
func FetchOGImage(ctx context.Context, sourceURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; NintendoNewsBot/1.0)")

	resp, err := http.DefaultClient.Do(req)
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

	imageURL, exists := doc.Find(`meta[property="og:image"]`).First().Attr("content")
	if !exists {
		return "", nil
	}
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return "", nil
	}

	base, err := url.Parse(sourceURL)
	if err != nil {
		return imageURL, nil
	}
	rel, err := url.Parse(imageURL)
	if err != nil {
		return imageURL, nil
	}
	return base.ResolveReference(rel).String(), nil
}
