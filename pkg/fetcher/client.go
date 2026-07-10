package fetcher

import (
	"crypto/tls"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

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
func PrepareScraperRequest(req *http.Request, cfg ScraperConfig) *http.Request {
	if cfg.ScraperAPI != "" {
		wrappedURL := WrapWithScraperAPI(req.URL.String(), cfg.ScraperAPI)
		if u, err := url.Parse(wrappedURL); err == nil {
			req.URL = u
			req.Host = u.Host
		}
	}

	if req.Header.Get("User-Agent") == "" || strings.Contains(req.Header.Get("User-Agent"), "Go-http-client") || strings.Contains(req.Header.Get("User-Agent"), "NintendoNewsBot") || strings.Contains(req.Header.Get("User-Agent"), "Mozilla/5.0 (Windows NT 10.0") {
		req.Header.Set("User-Agent", RandomUserAgent())
	}

	return req
}
