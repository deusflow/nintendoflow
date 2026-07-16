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

const defaultGroqURL = "https://api.groq.com/openai/v1/chat/completions"

// GroqProvider calls Groq via OpenAI-compatible chat completions API.
type GroqProvider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

func NewGroqProvider(apiKey, model, baseURL string) *GroqProvider {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultGroqURL
	}
	return &GroqProvider{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *GroqProvider) Name() string { return "groq-" + g.model }

func (g *GroqProvider) Complete(ctx context.Context, prompt string) (string, error) {
	messages := []map[string]string{}
	parts := strings.Split(prompt, "=== КІНЕЦЬ ІНСТРУКЦІЙ ===")
	if len(parts) == 2 {
		messages = append(messages, map[string]string{"role": "system", "content": strings.TrimSpace(parts[0])})
		messages = append(messages, map[string]string{"role": "user", "content": strings.TrimSpace(parts[1])})
	} else {
		messages = append(messages, map[string]string{"role": "user", "content": prompt})
	}

	payload := map[string]any{
		"model":       g.model,
		"messages":    messages,
		"temperature": 0.3,
		"response_format": map[string]string{
			"type": "json_object",
		},
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", fmt.Errorf("groq http 429 for model %s: %s", g.model, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("groq http %d for model %s: %s", resp.StatusCode, g.model, strings.TrimSpace(string(body)))
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
		return "", fmt.Errorf("empty response from groq %s", g.model)
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}
