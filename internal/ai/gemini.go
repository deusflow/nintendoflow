package ai

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// Free tier (March 2026): 10 RPM, 500 RPD.
// Caller should sleep 8s between calls to stay under RPM.

// GeminiProvider calls Gemini 2.5 Flash as primary AI.
type GeminiProvider struct {
	client *genai.Client
	model  string
}

// NewGeminiProvider initialises a Gemini client.
func NewGeminiProvider(ctx context.Context, apiKey string) (*GeminiProvider, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		return nil, fmt.Errorf("gemini init: %w", err)
	}
	return &GeminiProvider{client: client, model: "gemini-2.5-flash"}, nil
}

func (g *GeminiProvider) Name() string { return "gemini-2.5-flash" }

func (g *GeminiProvider) Complete(ctx context.Context, prompt string) (string, error) {
	result, err := g.client.Models.GenerateContent(ctx, g.model, genai.Text(prompt), nil)
	if err != nil {
		return "", fmt.Errorf("gemini generate: %w", err)
	}
	return strings.TrimSpace(result.Text()), nil
}

func (g *GeminiProvider) Rewrite(ctx context.Context, title, body, source string) (string, error) {
	prompt := BuildPrompt(NewsInput{
		Title:  title,
		Body:   body,
		Source: source,
	})
	result, err := g.Complete(ctx, prompt)
	if err != nil {
		return "", err
	}
	return sanitizeOutput(result)
}
