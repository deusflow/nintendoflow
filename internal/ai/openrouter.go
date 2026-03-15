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

// Free tier: 20 RPM, 200 req/day (no credit card).
// Fallback chain: DeepSeek V3 first (stronger model, better Ukrainian),
// then Llama 3.3 70B if DeepSeek is unavailable.
// Both excluded from training opt-out — pick your geopolitics 🙂
var openRouterModels = []string{
	"deepseek/deepseek-chat:free",            // DeepSeek V3 — безкоштовний
	"meta-llama/llama-3.3-70b-instruct:free", // Llama 3.3 70B — резерв
}

// OpenRouterProvider calls OpenRouter as fallback AI.
type OpenRouterProvider struct {
	apiKey     string
	httpClient *http.Client
}

// NewOpenRouterProvider creates an OpenRouter provider.
func NewOpenRouterProvider(apiKey string) *OpenRouterProvider {
	return &OpenRouterProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (o *OpenRouterProvider) Name() string { return "openrouter-free" }

func (o *OpenRouterProvider) Complete(ctx context.Context, prompt string) (string, error) {
	var lastErr error
	for _, model := range openRouterModels {
		result, err := o.callModel(ctx, model, prompt)
		if err != nil {
			lastErr = err
			continue // try next model
		}
		return strings.TrimSpace(result), nil
	}
	return "", fmt.Errorf("all openrouter models failed: %w", lastErr)
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
