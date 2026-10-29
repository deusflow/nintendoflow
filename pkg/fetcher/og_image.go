package fetcher

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var (
	youtubeIDPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	rawYouTubeEmbedRe = regexp.MustCompile(`(?:https?:)?//(?:www\.|m\.)?(?:youtube(?:-nocookie)?\.com/(?:embed|shorts|live|v)/|youtu\.be/)([A-Za-z0-9_-]{11})`)
)

// FetchOGImage loads page HTML and returns the og:image content if present.
func FetchOGImage(ctx context.Context, sourceURL string) (string, error) {
	_, imageURL, err := FetchPreferredMedia(ctx, sourceURL)
	if err != nil {
		return "", err
	}
	return imageURL, nil
}

// FetchPreferredMedia loads page HTML and returns preferred media URLs.
// Priority: YouTube / direct MP4 video first, then og:image.
func FetchPreferredMedia(ctx context.Context, sourceURL string) (videoURL string, imageURL string, err error) {
	finalURL, raw, err := FetchPageHTML(ctx, sourceURL)
	if err != nil {
		return "", "", err
	}
	if finalURL == "" {
		finalURL = sourceURL
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(raw))
	if err != nil {
		return "", "", fmt.Errorf("parse html: %w", err)
	}

	if yt := extractYouTubeURL(doc, string(raw), finalURL); yt != "" {
		return yt, "", nil
	}

	imageURL, exists := doc.Find(`meta[property="og:image"]`).First().Attr("content")
	if !exists || strings.TrimSpace(imageURL) == "" {
		imageURL, _ = doc.Find(`meta[name="twitter:image"]`).First().Attr("content")
	}
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return "", "", nil
	}

	base, err := url.Parse(finalURL)
	if err != nil {
		return "", imageURL, nil
	}
	rel, err := url.Parse(imageURL)
	if err != nil {
		return "", imageURL, nil
	}
	return "", base.ResolveReference(rel).String(), nil
}

// ExtractYouTubeFromHTML searches an HTML snippet (such as an RSS item body) for a YouTube video URL.
func ExtractYouTubeFromHTML(htmlSnippet, pageURL string) string {
	if strings.TrimSpace(htmlSnippet) == "" {
		return ""
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlSnippet))
	if err != nil {
		return ""
	}
	return extractYouTubeURL(doc, htmlSnippet, pageURL)
}

func extractYouTubeURL(doc *goquery.Document, rawHTML string, sourceURL string) string {
	if doc != nil {
		// 1. Check custom YouTube web components & data attributes (used by Nintendo Life, Eurogamer, VGC, etc.)
		idSelectors := []struct {
			sel  string
			attr string
		}{
			{"lite-youtube", "videoid"},
			{"[data-youtube-id]", "data-youtube-id"},
			{"[data-video-id]", "data-video-id"},
			{".youtube-player[data-id]", "data-id"},
		}
		for _, item := range idSelectors {
			if id, ok := doc.Find(item.sel).First().Attr(item.attr); ok {
				id = strings.TrimSpace(id)
				if youtubeIDPattern.MatchString(id) {
					return "https://www.youtube.com/watch?v=" + id
				}
			}
		}

		// 2. Check meta tags, iframes (including youtube-nocookie.com and lazy data-src), and watch links
		selectors := []struct {
			sel   string
			attrs []string
		}{
			{`meta[property="og:video:url"]`, []string{"content"}},
			{`meta[property="og:video:secure_url"]`, []string{"content"}},
			{`meta[property="og:video"]`, []string{"content"}},
			{`meta[name="twitter:player"]`, []string{"content"}},
			{`iframe[src*="youtube"]`, []string{"src"}},
			{`iframe[data-src*="youtube"]`, []string{"data-src"}},
			{`iframe[data-lazy-src*="youtube"]`, []string{"data-lazy-src"}},
			{`iframe[src*="youtu.be"]`, []string{"src"}},
			{`iframe[data-src*="youtu.be"]`, []string{"data-src"}},
			{`a[href*="youtube.com/watch"]`, []string{"href"}},
			{`a[href*="youtu.be/"]`, []string{"href"}},
		}
		for _, item := range selectors {
			sel := doc.Find(item.sel).First()
			for _, attr := range item.attrs {
				raw, ok := sel.Attr(attr)
				if !ok {
					continue
				}
				if normalized := normalizeYouTubeURL(raw, sourceURL); normalized != "" {
					return normalized
				}
			}
		}
	}

	// 3. Fallback regex scan on raw HTML (catches JSON-LD embedUrl, escaped scripts, etc.)
	if m := rawYouTubeEmbedRe.FindStringSubmatch(rawHTML); len(m) >= 2 {
		id := strings.TrimSpace(m[1])
		if youtubeIDPattern.MatchString(id) {
			return "https://www.youtube.com/watch?v=" + id
		}
	}

	return ""
}

func normalizeYouTubeURL(raw, pageURL string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
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
	isYT := strings.Contains(host, "youtube.com") ||
		strings.Contains(host, "youtube-nocookie.com") ||
		host == "youtu.be"
	if !isYT {
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
				case "embed", "shorts", "live", "v":
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

