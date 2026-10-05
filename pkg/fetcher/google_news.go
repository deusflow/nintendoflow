package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	dataNASgRe   = regexp.MustCompile(`data-n-a-sg="([^"]+)"`)
	dataNATsRe   = regexp.MustCompile(`data-n-a-ts="([^"]+)"`)
	dataNAIdRe   = regexp.MustCompile(`data-n-a-id="([^"]+)"`)
	garturlresRe = regexp.MustCompile(`garturlres[\\",\s]+(https?://[^\\",\s]+)`)

	extraGoogleHostsMu sync.RWMutex
	extraGoogleHosts   = make(map[string]bool)
)

// RegisterGoogleNewsHost registers an additional hostname (e.g. for mock HTTP test servers)
// to be treated as a Google News endpoint.
func RegisterGoogleNewsHost(host string) {
	extraGoogleHostsMu.Lock()
	defer extraGoogleHostsMu.Unlock()
	extraGoogleHosts[strings.ToLower(host)] = true
}

// IsGoogleNewsURL returns true if the URL points to news.google.com or a registered test host.
func IsGoogleNewsURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "news.google.com" || strings.HasSuffix(host, ".news.google.com") {
		return true
	}
	extraGoogleHostsMu.RLock()
	defer extraGoogleHostsMu.RUnlock()
	if extraGoogleHosts[host] {
		return true
	}
	if u.Host != "" && extraGoogleHosts[strings.ToLower(u.Host)] {
		return true
	}
	return false
}

// ResolveGoogleNewsURL attempts to resolve a news.google.com article URL to the
// original publisher's URL using Google's DotsSplashUi batchexecute RPC.
func ResolveGoogleNewsURL(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	if client == nil {
		client = redirectHTTPClient
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL, fmt.Errorf("parse url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return rawURL, fmt.Errorf("new request: %w", err)
	}
	cfg := NewScraperConfig(10 * time.Second)
	req = PrepareScraperRequest(req, cfg)

	resp, err := client.Do(req)
	if err != nil {
		return rawURL, fmt.Errorf("fetch google news page: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// If the HTTP client followed a redirect to an external non-Google site, return it.
	if resp.Request != nil && resp.Request.URL != nil {
		finalHost := strings.ToLower(resp.Request.URL.Hostname())
		if finalHost != "" && !strings.Contains(finalHost, "google") {
			extraGoogleHostsMu.RLock()
			isMock := extraGoogleHosts[finalHost] || (resp.Request.URL.Host != "" && extraGoogleHosts[strings.ToLower(resp.Request.URL.Host)])
			extraGoogleHostsMu.RUnlock()
			if !isMock {
				return resp.Request.URL.String(), nil
			}
		}
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return rawURL, fmt.Errorf("read response body: %w", err)
	}
	body := string(bodyBytes)

	// Extract data-n-a attributes
	sgMatch := dataNASgRe.FindStringSubmatch(body)
	tsMatch := dataNATsRe.FindStringSubmatch(body)
	if len(sgMatch) < 2 || len(tsMatch) < 2 {
		return rawURL, fmt.Errorf("data-n-a attributes not found in google news page")
	}

	sig := sgMatch[1]
	tsStr := tsMatch[1]
	ts, _ := strconv.ParseInt(tsStr, 10, 64)

	artID := ""
	idMatch := dataNAIdRe.FindStringSubmatch(body)
	if len(idMatch) >= 2 {
		artID = idMatch[1]
	}
	if artID == "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) > 0 {
			artID = parts[len(parts)-1]
		}
	}
	if artID == "" {
		return rawURL, fmt.Errorf("article id not found")
	}

	// Prepare batchexecute RPC request
	ctxParam := []any{
		[]any{"X", "X", []any{"X", "X"}, nil, nil, 1, 1, "US:en", nil, 1, nil, nil, nil, nil, nil, 0, 1},
		"X",
		"X",
		1,
		[]any{1, 1, 1},
		1,
		1,
		nil,
		0,
		0,
		nil,
		0,
	}
	inner := []any{
		"garturlreq",
		ctxParam,
		artID,
		ts,
		sig,
	}
	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return rawURL, fmt.Errorf("marshal inner: %w", err)
	}

	envelope := []any{
		[]any{
			[]any{"Fbv4je", string(innerJSON), nil, "1"},
		},
	}
	envJSON, err := json.Marshal(envelope)
	if err != nil {
		return rawURL, fmt.Errorf("marshal envelope: %w", err)
	}

	form := url.Values{}
	form.Set("f.req", string(envJSON))

	batchURL := fmt.Sprintf("%s://%s/_/DotsSplashUi/data/batchexecute", u.Scheme, u.Host)
	batchReq, err := http.NewRequestWithContext(ctx, http.MethodPost, batchURL, strings.NewReader(form.Encode()))
	if err != nil {
		return rawURL, fmt.Errorf("new batch request: %w", err)
	}
	batchReq.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	batchReq.Header.Set("User-Agent", RandomUserAgent())

	batchResp, err := client.Do(batchReq)
	if err != nil {
		return rawURL, fmt.Errorf("do batch request: %w", err)
	}
	defer func() { _ = batchResp.Body.Close() }()

	batchBody, err := io.ReadAll(batchResp.Body)
	if err != nil {
		return rawURL, fmt.Errorf("read batch response: %w", err)
	}

	// 1. Structured JSON parsing
	bodyStr := strings.TrimSpace(string(batchBody))
	if strings.HasPrefix(bodyStr, ")]}'") {
		bodyStr = strings.TrimSpace(bodyStr[4:])
	}
	var rows []any
	if err := json.Unmarshal([]byte(bodyStr), &rows); err == nil {
		for _, r := range rows {
			row, ok := r.([]any)
			if !ok || len(row) < 3 {
				continue
			}
			payloadStr, ok := row[2].(string)
			if !ok {
				continue
			}
			var payload []any
			if err := json.Unmarshal([]byte(payloadStr), &payload); err == nil && len(payload) >= 2 {
				if payload[0] == "garturlres" {
					if decodedURL, ok := payload[1].(string); ok && decodedURL != "" {
						return decodedURL, nil
					}
				}
			}
		}
	}

	// 2. Regex fallback
	resMatch := garturlresRe.FindSubmatch(batchBody)
	if len(resMatch) >= 2 {
		resolved := string(resMatch[1])
		resolved = strings.ReplaceAll(resolved, `\/`, `/`)
		resolved = strings.ReplaceAll(resolved, `\u003d`, `=`)
		resolved = strings.ReplaceAll(resolved, `\u0026`, `&`)
		return resolved, nil
	}

	return rawURL, fmt.Errorf("could not extract decoded url from batchexecute response")
}
