package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Free tier: 20 RPM, 200 req/day (no credit card).
// DeepSeek intentionally excluded: servers in China, no opt-out from training.
var openRouterFreeModels = []string{
	"openrouter/auto",                        // auto-router: best available free model
	"google/gemini-2.0-flash-exp:free",       // explicit fallback
	"meta-llama/llama-3.3-70b-instruct:free", // reserve
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

func (o *OpenRouterProvider) Rewrite(ctx context.Context, title, body, source string) (string, error) {
	var lastErr error
	for _, model := range openRouterFreeModels {
		result, err := o.callModel(ctx, model, title, body, source)
		if err != nil {
			lastErr = err
			continue
		}
		return sanitizeOutput(result)
	}
	return "", fmt.Errorf("all openrouter models failed: %w", lastErr)
}

func (o *OpenRouterProvider) callModel(ctx context.Context, model, title, bodyText, source string) (string, error) {
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": BuildPrompt(title, bodyText, source)},
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
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("rate limited on model %s", model)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openrouter http %d for model %s: %s", resp.StatusCode, model, body)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty response from %s", model)
	}
	return result.Choices[0].Message.Content, nil
}
