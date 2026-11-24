package ai

import (
	"context"
	"errors"
	"strings"
)

// ErrSkipped is returned when the AI decides the article is not worth posting.
var ErrSkipped = errors.New("article skipped by AI")

// ErrAllProvidersFailed is returned when every provider in the chain fails.
var ErrAllProvidersFailed = errors.New("all AI providers failed")

// ErrAICallBudgetExhausted is returned when the run has consumed its AI call budget.
var ErrAICallBudgetExhausted = errors.New("AI call budget exhausted")

// ErrInvalidAIOutput is returned when an AI response fails quality/language/number validation.
var ErrInvalidAIOutput = errors.New("AI output failed quality validation")

// AIProvider is the interface implemented by all AI backends.
type AIProvider interface {
	Name() string
	Complete(ctx context.Context, prompt string) (string, error)
}

// IsPermanentProviderError reports whether err indicates an auth, billing, or project-tier
// block that will not recover within the current run (e.g. HTTP 401/403 or quota limit 0).
func IsPermanentProviderError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	permanentSignals := []string{
		"status 401", "http 401", "error 401",
		"status 403", "http 403", "error 403",
		"permission_denied",
		"free tier is not available",
		"api key not valid",
		"invalid_api_key",
		"billing",
		"quota exceeded",
		"resource_exhausted",
		"limit: 0",
	}
	for _, sig := range permanentSignals {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}
