package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/genai"
)

const geminiAttemptTimeout = 25 * time.Second

// Free tier (March 2026): 10 RPM, 500 RPD.
// Caller should sleep 8s between calls to stay under RPM.

// GeminiProvider calls Gemini as primary AI.
type GeminiProvider struct {
	client *genai.Client
	model  string
	label  string
}

// NewGeminiProvider initialises a Gemini client.
func NewGeminiProvider(ctx context.Context, apiKey, model string) (*GeminiProvider, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		return nil, fmt.Errorf("gemini init: %w", err)
	}
	return &GeminiProvider{client: client, model: model, label: model}, nil
}

// NewGeminiProviderWithLabel initialises a Gemini client with a distinct key label for per-key rate tracking.
func NewGeminiProviderWithLabel(ctx context.Context, apiKey, model, suffix string) (*GeminiProvider, error) {
	p, err := NewGeminiProvider(ctx, apiKey, model)
	if err != nil {
		return nil, err
	}
	if suffix != "" {
		p.label = model + suffix
	}
	return p, nil
}

func (g *GeminiProvider) Name() string { return g.label }

func (g *GeminiProvider) Complete(ctx context.Context, prompt string) (string, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, geminiAttemptTimeout)
	defer cancel()

	result, err := g.client.Models.GenerateContent(attemptCtx, g.model, genai.Text(prompt), nil)
	if err != nil {
		return "", fmt.Errorf("gemini generate: %w", err)
	}
	return strings.TrimSpace(result.Text()), nil
}
