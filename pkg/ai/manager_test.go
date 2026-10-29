package ai

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mockProvider struct {
	name      string
	responses []mockResponse
	calls     int
}

type mockResponse struct {
	text string
	err  error
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Complete(_ context.Context, _ string) (string, error) {
	m.calls++
	if len(m.responses) == 0 {
		return "", errors.New("no mock responses configured")
	}
	idx := m.calls - 1
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	return m.responses[idx].text, m.responses[idx].err
}

func TestGenerateFallsBackOnProviderErrors(t *testing.T) {
	t.Run("falls back to next provider on 429", func(t *testing.T) {
		first := &mockProvider{
			name: "first",
			responses: []mockResponse{
				{err: errors.New("http 429 too many requests")},
				{err: errors.New("http 429 too many requests")},
			},
		}
		second := &mockProvider{
			name:      "second",
			responses: []mockResponse{{text: "ok"}},
		}

		m := NewManager([]AIProvider{first, second}, 2, 0)
		got, err := m.Generate(context.Background(), "prompt")
		if err != nil {
			t.Fatalf("Generate returned error: %v", err)
		}
		if got != "ok" {
			t.Fatalf("unexpected result: %q", got)
		}
		if second.calls != 1 {
			t.Fatalf("expected second provider to be called once, got %d", second.calls)
		}
		if m.RetriesUsed() != 1 {
			t.Fatalf("expected retriesUsed=1, got %d", m.RetriesUsed())
		}
	})

	t.Run("falls back on non-rate-limit error", func(t *testing.T) {
		first := &mockProvider{
			name:      "first",
			responses: []mockResponse{{err: errors.New("http 500 internal")}},
		}
		second := &mockProvider{
			name:      "second",
			responses: []mockResponse{{text: "ok"}},
		}

		m := NewManager([]AIProvider{first, second}, 2, 0)
		got, err := m.Generate(context.Background(), "prompt")
		if err != nil {
			t.Fatalf("Generate returned error: %v", err)
		}
		if got != "ok" {
			t.Fatalf("unexpected result: %q", got)
		}
		if second.calls != 1 {
			t.Fatalf("expected second provider to be called once, got %d", second.calls)
		}
	})
}

func TestGenerateBudgetCountsOnlyGenerateCalls(t *testing.T) {
	first := &mockProvider{
		name: "first",
		responses: []mockResponse{
			{err: errors.New("http 429 too many requests")},
			{err: errors.New("http 429 too many requests")},
		},
	}
	second := &mockProvider{
		name:      "second",
		responses: []mockResponse{{text: "ok"}},
	}

	m := NewManager([]AIProvider{first, second}, 1, 0*time.Second)
	_, err := m.Generate(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if m.CallsUsed() != 1 {
		t.Fatalf("expected callsUsed=1, got %d", m.CallsUsed())
	}
	if m.RetriesUsed() != 1 {
		t.Fatalf("expected retriesUsed=1, got %d", m.RetriesUsed())
	}
}

func TestFallbackToAnotherProviderDoesNotSleepDelay(t *testing.T) {
	first := &mockProvider{
		name:      "gemini",
		responses: []mockResponse{{err: errors.New("http 500 server error")}},
	}
	second := &mockProvider{
		name:      "groq",
		responses: []mockResponse{{text: "fast-fallback"}},
	}

	// Set a large delay (5s); switching from failed gemini to groq must be instant (<200ms).
	m := NewManager([]AIProvider{first, second}, 2, 5*time.Second)
	start := time.Now()
	got, err := m.Generate(context.Background(), "prompt")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if got != "fast-fallback" {
		t.Fatalf("unexpected result: %q", got)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected instant fallback to different provider, took %v", elapsed)
	}
}

