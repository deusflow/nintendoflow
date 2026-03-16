package fetcher

// Reddit blocks requests from GitHub Actions (Azure) IP ranges on the RSS endpoint
// regardless of headers. The JSON API uses a separate routing layer and is significantly
// more reliable from datacenter IPs when the correct User-Agent is sent.
//
// Rate limit (unauthenticated): 10 req/min per IP.
// Rate limit (OAuth): 100 req/min per client_id.
//
// With REDDIT_CLIENT_ID + REDDIT_CLIENT_SECRET in env this file uses OAuth2
// bearer tokens (Application-Only auth — no user login needed) and hits
// oauth.reddit.com, which is the most reliable path.
// Without credentials it falls back to the public JSON API.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/deuswork/nintendoflow/internal/config"
)

const (
	redditJSONTimeout  = 15 * time.Second
	redditPublicBase   = "https://www.reddit.com"
	redditOAuthBase    = "https://oauth.reddit.com"
	redditTokenURL     = "https://www.reddit.com/api/v1/access_token"
	redditBotUserAgent = "NintendoFlowBot/2.0 (Nintendo news aggregator; +https://github.com/deusflow/nintendoflow)"
)

var (
	redditHTTPClient = &http.Client{Timeout: redditJSONTimeout}

	// OAuth token cache — shared across fetches within one bot run.
	redditTokenMu      sync.Mutex
	redditTokenValue   string
	redditTokenExpires time.Time
)

// redditListing matches the top-level shape of Reddit's /new.json response.
type redditListing struct {
	Data struct {
		Children []struct {
			Data struct {
				Title      string  `json:"title"`
				URL        string  `json:"url"`
				Selftext   string  `json:"selftext"`
				Permalink  string  `json:"permalink"`
				CreatedUTC float64 `json:"created_utc"`
				Thumbnail  string  `json:"thumbnail"`
				IsSelf     bool    `json:"is_self"`
				Over18     bool    `json:"over_18"`
			} `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

// FetchRedditJSON fetches a subreddit using the Reddit JSON API.
// It attempts OAuth Application-Only auth when REDDIT_CLIENT_ID and
// REDDIT_CLIENT_SECRET are set; otherwise falls back to public JSON API.
func FetchRedditJSON(ctx context.Context, f config.Feed) ([]Item, error) {
	apiURL := buildRedditAPIURL(f.URL)
	authHeader, err := redditAuthHeader(ctx)
	if err != nil {
		// Non-fatal: fall back to unauthenticated.
		slog.Warn("reddit OAuth unavailable, using public JSON API", "error", err)
		authHeader = ""
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("reddit request: %w", err)
	}
	req.Header.Set("User-Agent", redditBotUserAgent)
	req.Header.Set("Accept", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := redditHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reddit http get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		// ok
	case http.StatusForbidden:
		return nil, fmt.Errorf("reddit blocked 403 (datacenter IP ban; add REDDIT_CLIENT_ID/REDDIT_CLIENT_SECRET for OAuth)")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("reddit rate limited 429")
	default:
		return nil, fmt.Errorf("reddit http %d", resp.StatusCode)
	}

	var listing redditListing
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("reddit parse JSON: %w", err)
	}

	var items []Item
	for _, child := range listing.Data.Children {
		post := child.Data
		if post.Title == "" || post.Over18 {
			continue
		}

		link := post.URL
		// Self-posts link back to Reddit itself.
		if post.IsSelf || link == "" || !strings.HasPrefix(link, "http") {
			link = redditPublicBase + post.Permalink
		}

		description := strings.TrimSpace(post.Selftext)
		if runes := []rune(description); len(runes) > 500 {
			description = string(runes[:500]) + "..."
		}

		t := time.Unix(int64(post.CreatedUTC), 0).UTC()

		imgURL := ""
		if th := post.Thumbnail; th != "" && th != "self" && th != "default" && th != "nsfw" && th != "image" && strings.HasPrefix(th, "http") {
			imgURL = th
		}

		items = append(items, Item{
			Title:          post.Title,
			Link:           link,
			Description:    description,
			ImageURL:       imgURL,
			PublishedAt:    &t,
			SourceName:     f.Name,
			SourcePriority: f.Priority,
			SourceType:     f.Type,
			RequireAnchor:  f.RequireAnchor,
			ContentHash:    hashContent(post.Title + post.Permalink),
		})
	}
	return items, nil
}

// buildRedditAPIURL converts any reddit.com URL to a JSON API URL.
// Examples:
//
//	https://www.reddit.com/r/NintendoSwitch/new/.rss  → https://www.reddit.com/r/NintendoSwitch/new.json?limit=50&raw_json=1
//	https://www.reddit.com/r/NintendoSwitch/new.json  → (unchanged, just adds params)
func buildRedditAPIURL(raw string) string {
	// Strip trailing slash, .rss extension, and query string — then append .json params.
	base := raw
	if i := strings.Index(base, "?"); i != -1 {
		base = base[:i]
	}
	base = strings.TrimSuffix(base, "/")
	base = strings.TrimSuffix(base, ".rss")
	base = strings.TrimSuffix(base, ".json")

	// Use OAuth base if we have credentials (will be set by caller via auth header).
	clientID := os.Getenv("REDDIT_CLIENT_ID")
	if clientID != "" {
		// Replace www.reddit.com with oauth.reddit.com for authenticated requests.
		base = strings.Replace(base, "https://www.reddit.com", redditOAuthBase, 1)
	}

	return base + ".json?limit=50&raw_json=1"
}

// redditAuthHeader returns "Bearer <token>" if Reddit OAuth credentials are
// available, or an empty string when they are not configured.
func redditAuthHeader(ctx context.Context) (string, error) {
	clientID := os.Getenv("REDDIT_CLIENT_ID")
	clientSecret := os.Getenv("REDDIT_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return "", nil // credentials not configured — caller falls back to public API
	}

	redditTokenMu.Lock()
	defer redditTokenMu.Unlock()

	if redditTokenValue != "" && time.Now().Before(redditTokenExpires) {
		return "Bearer " + redditTokenValue, nil
	}

	token, expiresIn, err := fetchRedditAppToken(ctx, clientID, clientSecret)
	if err != nil {
		return "", fmt.Errorf("reddit token: %w", err)
	}
	redditTokenValue = token
	redditTokenExpires = time.Now().Add(time.Duration(expiresIn-60) * time.Second)
	return "Bearer " + token, nil
}

// fetchRedditAppToken obtains an Application-Only OAuth2 token from Reddit.
// This does NOT require a user login — it uses the "client_credentials" flow.
func fetchRedditAppToken(ctx context.Context, clientID, clientSecret string) (token string, expiresIn int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, redditTokenURL,
		strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return "", 0, err
	}
	req.SetBasicAuth(clientID, clientSecret)
	req.Header.Set("User-Agent", redditBotUserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := redditHTTPClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", 0, fmt.Errorf("parse token response: %w", err)
	}
	if result.AccessToken == "" {
		return "", 0, fmt.Errorf("empty access_token in response")
	}
	return result.AccessToken, result.ExpiresIn, nil
}
