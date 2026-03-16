package fetcher

import (
	"context"
	"testing"
	"time"

	"github.com/deuswork/nintendoflow/internal/config"
)

func TestWithFeedTimeoutUsesOverride(t *testing.T) {
	ctx, cancel := withFeedTimeout(context.Background(), config.Feed{TimeoutSeconds: 1})
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 1500*time.Millisecond {
		t.Fatalf("expected deadline around 1s, got %v", remaining)
	}
}

func TestWithFeedTimeoutLeavesParentWhenUnset(t *testing.T) {
	parent := context.Background()
	ctx, cancel := withFeedTimeout(parent, config.Feed{})
	defer cancel()

	if ctx != parent {
		t.Fatal("expected parent context to be returned when timeout override is unset")
	}
}
