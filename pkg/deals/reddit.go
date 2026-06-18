package deals

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/fetcher"
)

const redditUserAgent = "script:nintendo_discount_bot:v1.0 (by /u/nintendoflow_bot)"

// SearchReddit searches r/NintendoSwitchDeals for a game title and returns
// one catchy sentence describing why it's worth buying.
// Uses direct .json endpoints (Способ 3), no PRAW/OAuth.
func SearchReddit(gameTitle string) (string, error) {
	// Clean title for search — strip subtitles, editions, etc.
	cleanTitle := strings.Split(gameTitle, ":")[0]
	cleanTitle = strings.Split(cleanTitle, " - ")[0]
	cleanTitle = strings.TrimSpace(cleanTitle)
	if cleanTitle == "" {
		return "", nil
	}

	query := url.QueryEscape(cleanTitle)
	searchURL := fmt.Sprintf(
		"https://old.reddit.com/r/NintendoSwitchDeals/search.json?q=%s&restrict_sr=1&sort=relevance&limit=5",
		query,
	)

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", redditUserAgent)
	cfg := fetcher.NewScraperConfig(10 * time.Second)
	req = fetcher.PrepareScraperRequest(req, cfg)

	req.Header.Set("Accept", "application/json")

	client := fetcher.NewScraperClient(cfg)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("reddit returned %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Children []struct {
				Data struct {
					Title    string `json:"title"`
					Selftext string `json:"selftext"`
					Score    int    `json:"score"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Data.Children) == 0 {
		return "", nil
	}

	// Try to extract a meaningful sentence from the top post's selftext
	for _, child := range result.Data.Children {
		post := child.Data
		if post.Selftext == "" {
			continue
		}

		sentences := strings.Split(post.Selftext, ".")
		for _, s := range sentences {
			s = strings.TrimSpace(s)
			s = strings.ReplaceAll(s, "\n", " ")
			// Filter out URLs, too-short, or too-long sentences
			if len(s) > 20 && len(s) < 150 && !strings.Contains(s, "http") && !strings.Contains(s, "[") {
				return s + ".", nil
			}
		}
	}

	// Fallback: use the cleaned title of the top Reddit thread
	topTitle := result.Data.Children[0].Data.Title
	// Strip common prefixes like [eShop/US], [Deal], etc.
	if idx := strings.Index(topTitle, "]"); idx != -1 {
		topTitle = strings.TrimSpace(topTitle[idx+1:])
	}
	if len(topTitle) > 100 {
		topTitle = topTitle[:97] + "..."
	}
	if topTitle != "" {
		slog.Debug("reddit fallback to thread title", "title", topTitle)
		return "💬 " + topTitle, nil
	}

	return "", nil
}
