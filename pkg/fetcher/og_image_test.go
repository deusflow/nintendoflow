package fetcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchOGImageFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><meta property="og:image" content="https://cdn.example.com/a.jpg"></head></html>`))
	}))
	defer ts.Close()

	img, err := FetchOGImage(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img != "https://cdn.example.com/a.jpg" {
		t.Fatalf("unexpected image url: %q", img)
	}
}

func TestFetchPreferredMediaYouTubePriority(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head>
			<meta property="og:image" content="https://cdn.example.com/a.jpg">
			<meta property="og:video:url" content="https://www.youtube.com/embed/dQw4w9WgXcQ">
		</head></html>`))
	}))
	defer ts.Close()

	video, image, err := FetchPreferredMedia(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if video != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("unexpected video url: %q", video)
	}
	if image != "https://cdn.example.com/a.jpg" {
		t.Fatalf("expected og:image to also be preserved alongside youtube video, got %q", image)
	}
}

func TestFetchPreferredMediaFallbackToImage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head><meta property="og:image" content="/img/card.jpg"></head></html>`))
	}))
	defer ts.Close()

	video, image, err := FetchPreferredMedia(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if video != "" {
		t.Fatalf("expected empty video url, got %q", video)
	}
	if image != ts.URL+"/img/card.jpg" {
		t.Fatalf("unexpected image url: %q", image)
	}
}

func TestFetchOGImageNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><head></head><body>no image</body></html>`))
	}))
	defer ts.Close()

	img, err := FetchOGImage(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img != "" {
		t.Fatalf("expected empty image url, got %q", img)
	}
}

func TestFetchOGImageHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts.Close()

	_, err := FetchOGImage(context.Background(), ts.URL)
	if err == nil {
		t.Fatal("expected error for non-200 response")
	}
}

func TestFetchPreferredMediaYouTubeNoCookieAndLazyIframe(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><article>
			<div class="rll-youtube-player" data-id="dQw4w9WgXcQ" data-src="https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?rel=0"></div>
		</article></body></html>`))
	}))
	defer ts.Close()

	video, _, err := FetchPreferredMedia(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if video != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Fatalf("expected youtube-nocookie lazy div/iframe to resolve, got %q", video)
	}
}

func TestSinglePassHTMLCacheAvoidsTripleFetch(t *testing.T) {
	hits := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`<html>
			<head>
				<meta property="article:published_time" content="2026-10-29T10:00:00Z">
				<meta property="og:image" content="https://cdn.example.com/switch2.jpg">
			</head>
			<body><article><p>Nintendo Switch 2 receives a major system update today with new features and improvements for all players worldwide.</p></article></body>
		</html>`))
	}))
	defer ts.Close()

	ctx := context.Background()
	if _, err := FetchSourcePublishedAt(ctx, ts.URL); err != nil {
		t.Fatalf("FetchSourcePublishedAt error: %v", err)
	}
	if _, _, err := FetchPreferredMedia(ctx, ts.URL); err != nil {
		t.Fatalf("FetchPreferredMedia error: %v", err)
	}
	if _, err := FetchArticleContent(ctx, ts.URL); err != nil {
		t.Fatalf("FetchArticleContent error: %v", err)
	}

	if hits != 1 {
		t.Fatalf("expected server to be fetched only 1 time across all 3 stages, got %d hits", hits)
	}
}

