package fetcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseFlexibleTimeAsianDates(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected time.Time
	}{
		{
			name:     "standard chinese date",
			input:    "2026年10月03日",
			expected: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "chinese date with single digit month and day",
			input:    "2026年1月2日",
			expected: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "chinese date with time",
			input:    "2026年10月03日 14:35:20",
			expected: time.Date(2026, 10, 3, 14, 35, 20, 0, time.UTC),
		},
		{
			name:     "chinese date with extra text",
			input:    "發布時間：2026年10月03日",
			expected: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseFlexibleTime(tc.input)
			if !ok {
				t.Fatalf("expected successful parse for %q, got false", tc.input)
			}
			if !got.Equal(tc.expected) {
				t.Fatalf("for %q expected %v, got %v", tc.input, tc.expected, got)
			}
		})
	}
}

func TestResolveRedirectStandardHTTP(t *testing.T) {
	finalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("final destination"))
	}))
	defer finalServer.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, finalServer.URL, http.StatusFound)
	}))
	defer redirectServer.Close()

	resolved, err := ResolveRedirect(redirectServer.URL)
	if err != nil {
		t.Fatalf("unexpected error resolving redirect: %v", err)
	}
	if resolved != finalServer.URL {
		t.Fatalf("expected %q, got %q", finalServer.URL, resolved)
	}
}

func TestResolveGoogleNewsRedirectMockHTTP(t *testing.T) {
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/rss/articles/"):
			// Return Google News landing page with data-n-a attributes
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`
				<!DOCTYPE html>
				<html>
				<head><title>Google News</title></head>
				<body>
					<div data-n-a-id="MOCK_ART_123" data-n-a-ts="1791184282" data-n-a-sg="MOCK_SIGNATURE_XYZ"></div>
				</body>
				</html>
			`))
		case r.URL.Path == "/_/DotsSplashUi/data/batchexecute":
			// Return decoded batchexecute response
			w.WriteHeader(http.StatusOK)
			finalTargetURL := "https://www.toy-people.com/en/?p=98521"
			resp := fmt.Sprintf(")]}'\n\n[[\"wrb.fr\",\"Fbv4je\",\"[\\\"garturlres\\\",\\\"%s\\\",1]\",null,null,null,\"1\"]]\n", finalTargetURL)
			_, _ = w.Write([]byte(resp))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockServer.Close()

	// Register mock host so ResolveGoogleNewsURL treats it as Google News
	RegisterGoogleNewsHost(mockServer.Listener.Addr().String())

	client := mockServer.Client()
	articleURL := mockServer.URL + "/rss/articles/MOCK_ART_123"

	resolved, err := ResolveGoogleNewsURL(context.Background(), client, articleURL)
	if err != nil {
		t.Fatalf("unexpected error in ResolveGoogleNewsURL: %v", err)
	}

	expectedTarget := "https://www.toy-people.com/en/?p=98521"
	if resolved != expectedTarget {
		t.Fatalf("expected resolved url to be %q, got %q", expectedTarget, resolved)
	}
}

func TestFetchSourcePublishedAtWithAsianDateHTML(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`
			<!DOCTYPE html>
			<html>
			<head>
				<title>Taiwan News</title>
			</head>
			<body>
				<span class="date">2026年10月03日</span>
			</body>
			</html>
		`))
	}))
	defer ts.Close()

	pubAt, err := FetchSourcePublishedAt(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pubAt == nil {
		t.Fatalf("expected non-nil published date")
	}

	expected := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	if !pubAt.Equal(expected) {
		t.Fatalf("expected date %v, got %v", expected, *pubAt)
	}
}
