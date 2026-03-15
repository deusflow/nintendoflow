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

	"github.com/deuswork/nintendoflow/internal/config"
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

// FetchAll fetches all active feeds concurrently and returns collected items.
func FetchAll(ctx context.Context, feeds []config.Feed) []Item {
	var (
		mu    sync.Mutex
		items []Item
		wg    sync.WaitGroup
	)

	for _, feed := range feeds {
		wg.Add(1)
		go func(f config.Feed) {
			defer wg.Done()
			result, err := fetchSource(ctx, f)
			if err != nil {
				slog.Warn("fetch source failed", "source", f.Name, "error", err)
				return
			}
			mu.Lock()
			items = append(items, result...)
			mu.Unlock()
		}(feed)
	}
	wg.Wait()
	return items
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
			Title:       entry.Title,
			Link:        link,
			Description: entry.Description,
			ImageURL:    imgURL,
			PublishedAt: pub,
			SourceName:  f.Name,
			SourceType:  f.Type,
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
