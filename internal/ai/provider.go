package ai

import (
	"context"
	"errors"
)

// ErrSkipped is returned when the AI decides the article is not worth posting.
var ErrSkipped = errors.New("article skipped by AI")

// ErrAllProvidersFailed is returned when every provider in the chain fails.
var ErrAllProvidersFailed = errors.New("all AI providers failed")

// ErrAICallBudgetExhausted is returned when the run has consumed its AI call budget.
var ErrAICallBudgetExhausted = errors.New("AI call budget exhausted")

// AIProvider is the interface implemented by all AI backends.
type AIProvider interface {
	Name() string
	Complete(ctx context.Context, prompt string) (string, error)
}
