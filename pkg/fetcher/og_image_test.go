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
