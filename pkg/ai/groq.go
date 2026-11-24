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
	label      string
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
		label:      "groq-" + model,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 35 * time.Second},
	}
}

// NewGroqProviderWithLabel initialises a Groq client with a distinct key label for rotation.
func NewGroqProviderWithLabel(apiKey, model, baseURL, suffix string) *GroqProvider {
	p := NewGroqProvider(apiKey, model, baseURL)
	if suffix != "" {
		p.label = "groq-" + model + suffix
	}
	return p
}

func (g *GroqProvider) Name() string {
	if g.label != "" {
		return g.label
	}
	return "groq-" + g.model
}

func (g *GroqProvider) Complete(ctx context.Context, prompt string) (string, error) {
	needJSON := strings.Contains(strings.ToLower(prompt), "json")

	messages := []map[string]string{}
	parts := strings.Split(prompt, "=== КІНЕЦЬ ІНСТРУКЦІЙ ===")
	if len(parts) == 2 {
		systemContent := strings.TrimSpace(parts[0])
		if needJSON {
			systemContent += "\nYou must respond strictly in valid JSON format without markdown wrappers. All generated post text (telegram_html and threads_text) MUST be strictly in Ukrainian language."
		}
		messages = append(messages, map[string]string{"role": "system", "content": systemContent})
		messages = append(messages, map[string]string{"role": "user", "content": strings.TrimSpace(parts[1])})
	} else {
		if needJSON {
			messages = append(messages, map[string]string{
				"role":    "system",
				"content": "You must respond strictly in valid JSON format without markdown wrappers.",
			})
		}
		messages = append(messages, map[string]string{"role": "user", "content": prompt})
	}

	out, statusCode, bodyStr, err := g.doRequest(ctx, messages, needJSON, true)
	if err == nil {
		if needJSON {
			return SanitizeJSON(out), nil
		}
		return out, nil
	}

	// Retry once without response_format / reasoning_effort when Groq rejects strict JSON schema validation.
	lowerBody := strings.ToLower(bodyStr)
	if statusCode == http.StatusBadRequest && (strings.Contains(lowerBody, "json_validate_failed") ||
		strings.Contains(lowerBody, "failed_generation") ||
		strings.Contains(lowerBody, "failed to generate json") ||
		strings.Contains(lowerBody, "reasoning_effort")) {
		outRetry, _, _, retryErr := g.doRequest(ctx, messages, false, false)
		if retryErr == nil {
			if needJSON {
				return SanitizeJSON(outRetry), nil
			}
			return outRetry, nil
		}
		return "", retryErr
	}

	return "", err
}

func (g *GroqProvider) doRequest(ctx context.Context, messages []map[string]string, useJSONMode bool, allowReasoningEffort bool) (string, int, string, error) {
	payload := map[string]any{
		"model":                 g.model,
		"messages":              messages,
		"temperature":           0.3,
		"max_completion_tokens": 3072,
	}
	if allowReasoningEffort && supportsGroqReasoningEffort(g.model) {
		payload["reasoning_effort"] = "low"
	}
	if useJSONMode {
		payload["response_format"] = map[string]string{
			"type": "json_object",
		}
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return "", 0, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL, bytes.NewReader(jsonBody))
	if err != nil {
		return "", 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := strings.TrimSpace(string(body))
	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After"))
		if retryAfter != "" {
			return "", resp.StatusCode, bodyStr, fmt.Errorf("groq http 429 retry-after:%s for model %s: %s", retryAfter, g.model, bodyStr)
		}
		return "", resp.StatusCode, bodyStr, fmt.Errorf("groq http 429 for model %s: %s", g.model, bodyStr)
	}
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, bodyStr, fmt.Errorf("groq http %d for model %s: %s", resp.StatusCode, g.model, bodyStr)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&result); err != nil {
		return "", resp.StatusCode, bodyStr, err
	}
	if len(result.Choices) == 0 {
		return "", resp.StatusCode, bodyStr, fmt.Errorf("empty response from groq %s", g.model)
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), resp.StatusCode, bodyStr, nil
}

func supportsGroqReasoningEffort(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "gpt-oss")
}
