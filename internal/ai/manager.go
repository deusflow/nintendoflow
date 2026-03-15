package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	retryDelayDurationRe = regexp.MustCompile(`(?i)retryDelay["'=:\s]*([0-9]+(?:\.[0-9]+)?s)`)
	retryAfterSecondsRe  = regexp.MustCompile(`(?i)retry[_ -]?after["'=:\s]*([0-9]+)`)
	retryInSecondsRe     = regexp.MustCompile(`(?i)in\s+([0-9]+(?:\.[0-9]+)?)s`)
)

// Manager coordinates all AI calls for a single run.
type Manager struct {
	providers          []AIProvider
	maxCalls           int
	delay              time.Duration
	callsUsed          int
	lastCallFinishedAt time.Time
	lastProvider       string
}

// NewManager creates a budget-aware AI manager.
func NewManager(providers []AIProvider, maxCalls int, delay time.Duration) *Manager {
	copied := make([]AIProvider, len(providers))
	copy(copied, providers)
	return &Manager{
		providers: copied,
		maxCalls:  maxCalls,
		delay:     delay,
	}
}

// Generate sends one prompt through providers sequentially with retry/fallback rules.
func (m *Manager) Generate(ctx context.Context, prompt string) (string, error) {
	if m.callsUsed >= m.maxCalls {
		return "", ErrAICallBudgetExhausted
	}
	// One Generate invocation = one budget slot, regardless of provider retries/fallbacks.
	m.callsUsed++

	var lastErr error

	for _, provider := range m.providers {
		text, err := m.generateWithProvider(ctx, provider, prompt)
		if err == nil {
			return sanitize(text)
		}
		if errorsIsContext(err) || errorsIsBudget(err) {
			return "", err
		}

		lastErr = err
		slog.Warn("AI provider failed, trying next",
			"provider", provider.Name(),
			"error", err.Error(),
		)
	}

	if lastErr == nil {
		return "", ErrAllProvidersFailed
	}
	return "", fmt.Errorf("%w: %v", ErrAllProvidersFailed, lastErr)
}

// CallsUsed returns how many external AI calls were spent in this run.
func (m *Manager) CallsUsed() int { return m.callsUsed }

// CallsBudget returns the max AI call budget for this run.
func (m *Manager) CallsBudget() int { return m.maxCalls }

// LastProvider returns the provider that most recently succeeded.
func (m *Manager) LastProvider() string { return m.lastProvider }

func (m *Manager) generateWithProvider(ctx context.Context, provider AIProvider, prompt string) (string, error) {
	text, err := m.callProvider(ctx, provider, prompt)
	if err == nil {
		m.lastProvider = provider.Name()
		return text, nil
	}

	retryDelay, rateLimited := parseRateLimitRetryDelay(err)
	if !rateLimited {
		return "", err
	}
	if retryDelay <= 0 {
		retryDelay = m.delay
		slog.Warn("AI rate limit retryDelay missing, using default",
			"provider", provider.Name(),
			"retry_delay_ms", retryDelay.Milliseconds(),
		)
	}

	slog.Warn("AI rate limited, retrying same provider once",
		"provider", provider.Name(),
		"retry_delay_ms", retryDelay.Milliseconds(),
		"error", err.Error(),
	)
	if err := waitWithContext(ctx, retryDelay); err != nil {
		return "", err
	}

	text, retryErr := m.callProvider(ctx, provider, prompt)
	if retryErr == nil {
		m.lastProvider = provider.Name()
		return text, nil
	}
	return "", fmt.Errorf("provider %s retry failed: %w", provider.Name(), retryErr)
}

func (m *Manager) callProvider(ctx context.Context, provider AIProvider, prompt string) (string, error) {
	if err := m.waitBetweenCalls(ctx); err != nil {
		return "", err
	}

	text, err := provider.Complete(ctx, prompt)
	m.lastCallFinishedAt = time.Now()
	if err != nil {
		return "", err
	}
	return text, nil
}

func (m *Manager) waitBetweenCalls(ctx context.Context) error {
	if m.lastCallFinishedAt.IsZero() || m.delay <= 0 {
		return nil
	}
	remaining := time.Until(m.lastCallFinishedAt.Add(m.delay))
	if remaining <= 0 {
		return nil
	}
	return waitWithContext(ctx, remaining)
}

func waitWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseRateLimitRetryDelay(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}

	text := err.Error()
	lower := strings.ToLower(text)
	if !strings.Contains(text, "429") && !strings.Contains(lower, "rate limit") && !strings.Contains(lower, "too many requests") {
		return 0, false
	}

	if match := retryDelayDurationRe.FindStringSubmatch(text); len(match) == 2 {
		delay, parseErr := time.ParseDuration(match[1])
		if parseErr == nil {
			return delay, true
		}
	}
	if match := retryAfterSecondsRe.FindStringSubmatch(text); len(match) == 2 {
		seconds, parseErr := strconv.Atoi(match[1])
		if parseErr == nil {
			return time.Duration(seconds) * time.Second, true
		}
	}
	if match := retryInSecondsRe.FindStringSubmatch(text); len(match) == 2 {
		seconds, parseErr := strconv.ParseFloat(match[1], 64)
		if parseErr == nil {
			return time.Duration(seconds * float64(time.Second)), true
		}
	}

	return 0, true
}

func sanitize(text string) (string, error) {
	text = strings.TrimSpace(text)
	if strings.ToUpper(strings.Trim(text, ".,!? \n\r\t ")) == "SKIP" {
		return "", ErrSkipped
	}
	if runes := []rune(text); len(runes) > 900 {
		text = string(runes[:870]) + "..."
	}
	return text, nil
}

func errorsIsContext(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func errorsIsBudget(err error) bool {
	return errors.Is(err, ErrAICallBudgetExhausted)
}
