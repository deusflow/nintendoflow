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

const defaultGitHubModelsURL = "https://models.inference.ai.azure.com/chat/completions"

// GitHubModelsProvider calls GitHub Models as an OpenAI-compatible backend.
type GitHubModelsProvider struct {
	apiKey     string
	model      string
	baseURL    string
	httpClient *http.Client
}

func NewGitHubModelsProvider(apiKey, model, baseURL string) *GitHubModelsProvider {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultGitHubModelsURL
	}
	return &GitHubModelsProvider{
		apiKey:     apiKey,
		model:      model,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *GitHubModelsProvider) Name() string { return "github-models-" + g.model }

func (g *GitHubModelsProvider) Complete(ctx context.Context, prompt string) (string, error) {
	payload := map[string]any{
		"model": g.model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.3,
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
		return "", fmt.Errorf("github models http 429 for model %s: %s", g.model, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github models http %d for model %s: %s", resp.StatusCode, g.model, strings.TrimSpace(string(body)))
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
		return "", fmt.Errorf("empty response from github models %s", g.model)
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}
