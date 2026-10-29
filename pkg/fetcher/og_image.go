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
	rawYouTubeEmbedRe = regexp.MustCompile(`(?:https?:)?//(?:www\.|m\.)?(?:youtube(?:-nocookie)?\.com/(?:embed/|shorts/|live/|v/|watch\?(?:[^"'\s<>]*&)?v=)|youtu\.be/)([A-Za-z0-9_-]{11})`)
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
// Both videoURL (if a YouTube trailer/video is present) and imageURL (og:image / twitter:image)
// are returned so the caller always has a fallback image alongside the video.
func FetchPreferredMedia(ctx context.Context, sourceURL string) (videoURL string, imageURL string, err error) {
	if directYT := normalizeYouTubeURL(sourceURL, sourceURL); directYT != "" {
		return directYT, "", nil
	}

	finalURL, raw, err := FetchPageHTML(ctx, sourceURL)
	if err != nil {
		return "", "", err
	}
	if finalURL == "" {
		finalURL = sourceURL
	}

	if directYT := normalizeYouTubeURL(finalURL, finalURL); directYT != "" {
		return directYT, "", nil
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(raw))
	if err != nil {
		return "", "", fmt.Errorf("parse html: %w", err)
	}

	videoURL = extractYouTubeURL(doc, string(raw), finalURL)

	rawImg, exists := doc.Find(`meta[property="og:image"]`).First().Attr("content")
	if !exists || strings.TrimSpace(rawImg) == "" {
		rawImg, _ = doc.Find(`meta[name="twitter:image"]`).First().Attr("content")
	}
	rawImg = strings.TrimSpace(rawImg)
	if rawImg != "" {
		imageURL = rawImg
		if base, errBase := url.Parse(finalURL); errBase == nil {
			if rel, errRel := url.Parse(rawImg); errRel == nil {
				imageURL = base.ResolveReference(rel).String()
			}
		}
	}

	return videoURL, imageURL, nil
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
		// 0. Check page-level OpenGraph / Twitter video player meta tags first
		metaSelectors := []string{
			`meta[property="og:video:url"]`,
			`meta[property="og:video:secure_url"]`,
			`meta[property="og:video"]`,
			`meta[name="twitter:player"]`,
		}
		for _, sel := range metaSelectors {
			if raw, ok := doc.Find(sel).First().Attr("content"); ok {
				if normalized := normalizeYouTubeURL(raw, sourceURL); normalized != "" {
					return normalized
				}
			}
		}

		// Scope search to article body container first so sidebar/footer channel links are ignored,
		// then fall back to the full document if no article container matched.
		scopes := []*goquery.Selection{
			doc.Find(`article, [itemprop="articleBody"], .article-body, .entry-content, .post-content, .article-content, .story-body, main`),
			doc.Selection,
		}

		idSelectors := []struct {
			sel  string
			attr string
		}{
			{"lite-youtube", "videoid"},
			{"[data-youtube-id]", "data-youtube-id"},
			{"[data-video-id]", "data-video-id"},
			{"[data-embed-id]", "data-embed-id"},
			{".rll-youtube-player[data-id]", "data-id"},
			{".youtube-player[data-id]", "data-id"},
			{".youtube-video[data-id]", "data-id"},
			{`[class*="youtube"][data-id]`, "data-id"},
		}

		urlSelectors := []struct {
			sel   string
			attrs []string
		}{
			{`iframe[src*="youtube"]`, []string{"src"}},
			{`iframe[data-src*="youtube"]`, []string{"data-src"}},
			{`iframe[data-lazy-src*="youtube"]`, []string{"data-lazy-src"}},
			{`iframe[src*="youtu.be"]`, []string{"src"}},
			{`iframe[data-src*="youtu.be"]`, []string{"data-src"}},
			{`[data-src*="youtube.com/embed"]`, []string{"data-src"}},
			{`[data-src*="youtube-nocookie.com/embed"]`, []string{"data-src"}},
			{`[data-lazy-src*="youtube.com/embed"]`, []string{"data-lazy-src"}},
			{`a[href*="youtube.com/watch"]`, []string{"href"}},
			{`a[href*="youtu.be/"]`, []string{"href"}},
		}

		for _, scope := range scopes {
			if scope == nil || scope.Length() == 0 {
				continue
			}
			for _, item := range idSelectors {
				if id, ok := scope.Find(item.sel).First().Attr(item.attr); ok {
					id = strings.TrimSpace(id)
					if youtubeIDPattern.MatchString(id) {
						return "https://www.youtube.com/watch?v=" + id
					}
				}
			}
			for _, item := range urlSelectors {
				sel := scope.Find(item.sel).First()
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
			// Also scan raw inner HTML of the scope (catches <noscript><iframe> and inline JSON embeds)
			if scopeHTML, err := scope.Html(); err == nil && scopeHTML != "" {
				unescaped := strings.ReplaceAll(scopeHTML, `\/`, `/`)
				if m := rawYouTubeEmbedRe.FindStringSubmatch(unescaped); len(m) >= 2 {
					id := strings.TrimSpace(m[1])
					if youtubeIDPattern.MatchString(id) {
						return "https://www.youtube.com/watch?v=" + id
					}
				}
			}
		}
	}

	// Fallback regex scan on raw HTML (catches JSON-LD embedUrl, escaped scripts, <noscript>, etc.)
	unescapedRaw := strings.ReplaceAll(rawHTML, `\/`, `/`)
	if m := rawYouTubeEmbedRe.FindStringSubmatch(unescapedRaw); len(m) >= 2 {
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

