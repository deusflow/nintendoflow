package fetcher

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type cachedPage struct {
	finalURL  string
	body      []byte
	fetchedAt time.Time
}

var (
	pageCacheMu sync.RWMutex
	pageCache   = make(map[string]cachedPage)
)

const (
	pageCacheTTL  = 10 * time.Minute
	maxHTMLBytes  = 4 << 20 // 4 MB cap for HTML pages
)

// FetchPageHTML downloads an article page once (unwrapping Google News redirects if needed)
// and caches the HTTP 200 HTML body in memory so subsequent calls (date check, media extraction,
// content extraction) reuse the exact same response without hitting the publisher server 3 times.
func FetchPageHTML(ctx context.Context, rawURL string) (string, []byte, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", nil, fmt.Errorf("empty url")
	}

	pageCacheMu.RLock()
	if entry, ok := pageCache[rawURL]; ok && time.Since(entry.fetchedAt) < pageCacheTTL {
		pageCacheMu.RUnlock()
		return entry.finalURL, entry.body, nil
	}
	pageCacheMu.RUnlock()

	targetURL := rawURL
	if IsGoogleNewsURL(targetURL) {
		resolved, err := ResolveGoogleNewsURL(ctx, redirectHTTPClient, targetURL)
		if err == nil && resolved != "" && !IsGoogleNewsURL(resolved) {
			targetURL = resolved
		}
	}

	pageCacheMu.RLock()
	if entry, ok := pageCache[targetURL]; ok && time.Since(entry.fetchedAt) < pageCacheTTL {
		pageCacheMu.RUnlock()
		return entry.finalURL, entry.body, nil
	}
	pageCacheMu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return targetURL, nil, fmt.Errorf("build request: %w", err)
	}

	cfg := NewScraperConfig(15 * time.Second)
	req = PrepareScraperRequest(req, cfg)

	client := NewScraperClient(cfg)
	resp, err := client.Do(req)
	if err != nil {
		return targetURL, nil, fmt.Errorf("http get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return targetURL, nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLBytes))
	if err != nil {
		return targetURL, nil, fmt.Errorf("read body: %w", err)
	}

	finalURL := targetURL
	if resp.Request != nil && resp.Request.URL != nil && cfg.ScraperAPI == "" {
		finalURL = resp.Request.URL.String()
	}

	entry := cachedPage{
		finalURL:  finalURL,
		body:      body,
		fetchedAt: time.Now(),
	}
	pageCacheMu.Lock()
	pageCache[rawURL] = entry
	pageCache[targetURL] = entry
	if finalURL != "" {
		pageCache[finalURL] = entry
	}
	pageCacheMu.Unlock()

	return finalURL, body, nil
}


var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:124.0) Gecko/20100101 Firefox/124.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3 Safari/605.1.15",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 17_4_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.3 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPad; CPU OS 17_4_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/123.0.6312.52 Mobile/15E148 Safari/604.1",
}

// RandomUserAgent returns a randomly selected real browser User-Agent.
func RandomUserAgent() string {
	return userAgents[rand.Intn(len(userAgents))]
}

// ScraperConfig holds settings for the scraper client.
type ScraperConfig struct {
	Timeout   time.Duration
	ProxyURL  string // from SCRAPER_PROXY_URL
	ScraperAPI string // from SCRAPER_API_KEY
}

// NewScraperConfig initializes a config reading from environment variables.
func NewScraperConfig(timeout time.Duration) ScraperConfig {
	return ScraperConfig{
		Timeout:    timeout,
		ProxyURL:   os.Getenv("SCRAPER_PROXY_URL"),
		ScraperAPI: os.Getenv("SCRAPER_API_KEY"),
	}
}

// NewScraperClient returns an http.Client configured to bypass simple blocks.
// If SCRAPER_PROXY_URL is set, it proxies traffic through it.
func NewScraperClient(cfg ScraperConfig) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment, // default
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
		},
	}

	if cfg.ProxyURL != "" {
		if u, err := url.Parse(cfg.ProxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
	}
}

// WrapWithScraperAPI modifies the target URL to use ScraperAPI if the API key is set.
// This is useful for hard-blocked sites like Cloudflare protected endpoints.
func WrapWithScraperAPI(targetURL string, scraperAPIKey string) string {
	if scraperAPIKey == "" {
		return targetURL
	}

	// Example using scraperapi.com
	u, err := url.Parse("https://api.scraperapi.com")
	if err != nil {
		return targetURL
	}

	q := u.Query()
	q.Set("api_key", scraperAPIKey)
	q.Set("url", targetURL)

	u.RawQuery = q.Encode()
	return u.String()
}

// PrepareScraperRequest applies anti-bot measures to the request (ScraperAPI + random UA).
// It only overrides the User-Agent if it's unset or still has Go's default value.
// Callers who set a custom UA (e.g. Reddit bot UA) before calling this function
// will keep their UA intact.
func PrepareScraperRequest(req *http.Request, cfg ScraperConfig) *http.Request {
	if cfg.ScraperAPI != "" {
		wrappedURL := WrapWithScraperAPI(req.URL.String(), cfg.ScraperAPI)
		if u, err := url.Parse(wrappedURL); err == nil {
			req.URL = u
			req.Host = u.Host
		}
	}

	currentUA := req.Header.Get("User-Agent")
	needsOverride := currentUA == "" ||
		strings.HasPrefix(currentUA, "Go-http-client") ||
		strings.Contains(currentUA, "NintendoNewsBot")
	if needsOverride {
		req.Header.Set("User-Agent", RandomUserAgent())
	}

	return req
}
