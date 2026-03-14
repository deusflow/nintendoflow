package fetcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

// Item is a normalised article fetched from an RSS feed.
type Item struct {
	Title       string
	Link        string
	Description string
	ImageURL    string
	PublishedAt *time.Time
	SourceName  string
	SourceType  string
	ContentHash string
}

// FetchAll fetches all sources concurrently and returns deduplicated items.
func FetchAll(ctx context.Context) []Item {
	var (
		mu    sync.Mutex
		Items []Item
		wg    sync.WaitGroup
	)

	for _, src := range Sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			items, err := fetchSource(ctx, s)
			if err != nil {
				slog.Warn("fetch source failed", "source", s.Name, "error", err)
				return
			}
			mu.Lock()
			Items = append(Items, items...)
			mu.Unlock()
		}(src)
	}
	wg.Wait()
	return Items
}

func fetchSource(ctx context.Context, s Source) ([]Item, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", s.FeedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; NintendoNewsBot/1.0)")
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml, */*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

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
		if s.NeedsRedirectResolve {
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
			Title:       entry.Title,
			Link:        link,
			Description: entry.Description,
			ImageURL:    imgURL,
			PublishedAt: pub,
			SourceName:  s.Name,
			SourceType:  s.Type,
			ContentHash: hash,
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
	defer resp.Body.Close()
	return resp.Request.URL.String(), nil
}

func hashContent(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
