package fetcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/deuswork/nintendoflow/internal/config"
	"github.com/mmcdole/gofeed"
)

// interDomainDelay is the minimum pause between consecutive requests to the
// same hostname. Prevents rate-limiting on feeds like Google News.
const interDomainDelay = 2 * time.Second

// Item is a normalised article fetched from an RSS feed.
type Item struct {
	Title         string
	Link          string
	Description   string
	ImageURL      string
	PublishedAt   *time.Time
	SourceName    string
	SourceType    string
	RequireAnchor bool
	ContentHash   string
}

// FetchAll fetches all active feeds and returns collected items.
// Feeds that share the same hostname are fetched sequentially with a
// interDomainDelay pause between them. Feeds on different hostnames run
// in parallel goroutines so the overall fetch stays fast.
func FetchAll(ctx context.Context, feeds []config.Feed) []Item {
	// Group feeds by hostname so same-domain requests are serialised.
	groups := make(map[string][]config.Feed)
	for _, f := range feeds {
		domain := extractDomain(f.URL)
		groups[domain] = append(groups[domain], f)
	}

	var (
		mu    sync.Mutex
		items []Item
		wg    sync.WaitGroup
	)

	for domain, group := range groups {
		wg.Add(1)
		go func(d string, domainFeeds []config.Feed) {
			defer wg.Done()
			for i, f := range domainFeeds {
				if i > 0 {
					// Respect rate limit: wait before the next request to this domain.
					slog.Debug("domain throttle pause", "domain", d, "delay_ms", interDomainDelay.Milliseconds())
					select {
					case <-ctx.Done():
						return
					case <-time.After(interDomainDelay):
					}
				}
				result, err := fetchSource(ctx, f)
				if err != nil {
					slog.Warn("fetch source failed", "source", f.Name, "error", err)
					continue
				}
				mu.Lock()
				items = append(items, result...)
				mu.Unlock()
			}
		}(domain, group)
	}

	wg.Wait()
	return items
}

// extractDomain returns the hostname of rawURL, used as the grouping key for
// per-domain rate limiting. Falls back to the full URL string on parse error.
func extractDomain(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Host
}

func fetchSource(ctx context.Context, f config.Feed) ([]Item, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", f.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; NintendoNewsBot/1.0)")
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml, */*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	parser := gofeed.NewParser()
	feed, err := parser.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}

	var items []Item
	for _, entry := range feed.Items {
		link := entry.Link
		if f.NeedsRedirectResolve {
			resolved, err := ResolveRedirect(link)
			if err == nil {
				link = resolved
			}
		}

		imgURL := ""
		if entry.Image != nil {
			imgURL = entry.Image.URL
		}

		var pub *time.Time
		if entry.PublishedParsed != nil {
			t := *entry.PublishedParsed
			pub = &t
		}

		hash := hashContent(entry.Title + entry.Description)

		items = append(items, Item{
			Title:         entry.Title,
			Link:          link,
			Description:   entry.Description,
			ImageURL:      imgURL,
			PublishedAt:   pub,
			SourceName:    f.Name,
			SourceType:    f.Type,
			RequireAnchor: f.RequireAnchor,
			ContentHash:   hash,
		})
	}
	return items, nil
}

// ResolveRedirect follows a redirect and returns the final URL.
func ResolveRedirect(rawURL string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Head(rawURL)
	if err != nil {
		return rawURL, nil
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.Request.URL.String(), nil
}

func hashContent(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
