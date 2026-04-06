package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultOpenRouterModel = "google/gemma-2-9b-it:free"

// OpenRouterProvider calls OpenRouter as fallback AI.
type OpenRouterProvider struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewOpenRouterProvider creates an OpenRouter provider.
func NewOpenRouterProvider(apiKey string) *OpenRouterProvider {
	return &OpenRouterProvider{
		apiKey:     apiKey,
		model:      defaultOpenRouterModel,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewOpenRouterProviderWithModel creates an OpenRouter provider with one configured model.
// If model is empty, it falls back to the default free model.
func NewOpenRouterProviderWithModel(apiKey, model string) *OpenRouterProvider {
	p := NewOpenRouterProvider(apiKey)
	if m := strings.TrimSpace(model); m != "" {
		p.model = m
	}
	return p
}

func (o *OpenRouterProvider) Name() string { return "openrouter-free" }

func (o *OpenRouterProvider) Complete(ctx context.Context, prompt string) (string, error) {
	result, err := o.callModel(ctx, o.model, prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result), nil
}

func (o *OpenRouterProvider) callModel(ctx context.Context, model string, prompt string) (string, error) {
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 500,
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/deuswork/nintendoflow")
	req.Header.Set("X-Title", "Nintendo News Bot")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("openrouter http 429 for model %s: %s", model, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openrouter http %d for model %s: %s", resp.StatusCode, model, body)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty response from %s", model)
	}
	return result.Choices[0].Message.Content, nil
}
