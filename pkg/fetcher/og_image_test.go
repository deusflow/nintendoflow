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
	if image != "" {
		t.Fatalf("expected empty image fallback when youtube exists, got %q", image)
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
