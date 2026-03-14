package ai

import (
	"context"
	"errors"
)

// ErrSkipped is returned when the AI decides the article is not worth posting.
var ErrSkipped = errors.New("article skipped by AI")

// ErrAllProvidersFailed is returned when every provider in the chain fails.
var ErrAllProvidersFailed = errors.New("all AI providers failed")

// AIProvider is the interface implemented by all AI backends.
type AIProvider interface {
	Name() string
	Rewrite(ctx context.Context, title, body, source string) (string, error)
}
