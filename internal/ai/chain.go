package ai

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// Chain tries providers in order, returning the first successful result.
type Chain struct {
	providers []AIProvider
	sleep     time.Duration
}

// NewChain creates a fallback chain with a sleep delay between calls.
func NewChain(sleep time.Duration, providers ...AIProvider) *Chain {
	return &Chain{providers: providers, sleep: sleep}
}

// Complete runs a generic prompt through providers in order.
// Returns generated text, provider name that succeeded, and error if all fail.
func (c *Chain) Complete(ctx context.Context, prompt string) (text string, providerName string, err error) {
	for _, p := range c.providers {
		time.Sleep(c.sleep)

		t, err := p.Complete(ctx, prompt)
		if err != nil {
			slog.Warn("AI provider failed, trying next",
				"provider", p.Name(),
				"error", err.Error(),
			)
			continue
		}
		return t, p.Name(), nil
	}
	return "", "", ErrAllProvidersFailed
}

// Rewrite rewrites the article text using the first available provider.
// Returns the rewritten text, the provider name that succeeded, and any error.
func (c *Chain) Rewrite(ctx context.Context, title, body, source string) (text string, providerName string, err error) {
	for _, p := range c.providers {
		time.Sleep(c.sleep)

		t, err := p.Rewrite(ctx, title, body, source)
		if err != nil {
			slog.Warn("AI provider failed, trying next",
				"provider", p.Name(),
				"error", err.Error(),
			)
			continue
		}
		return t, p.Name(), nil
	}
	return "", "", ErrAllProvidersFailed
}

// sanitizeOutput trims, checks for SKIP, and enforces the 900-char hard limit.
// This is the shared utility used by all providers after they get a response.
func sanitizeOutput(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "SKIP" {
		return "", ErrSkipped
	}
	if runes := []rune(text); len(runes) > 900 {
		text = string(runes[:870]) + "..."
	}
	return text, nil
}
