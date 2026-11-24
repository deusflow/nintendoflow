package embedding

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"
	"unicode"

	"google.golang.org/genai"
)

// Embedder generates vector embeddings for text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// DefaultEmbeddingModel is the active Gemini embedding model.
const DefaultEmbeddingModel = "gemini-embedding-001"

// DefaultEmbeddingDimensions keeps JSONB storage compact while preserving cosine similarity quality.
const DefaultEmbeddingDimensions int32 = 768

// GeminiEmbedder generates embeddings with multi-key rotation and per-run cooldown on 403/quota errors.
type GeminiEmbedder struct {
	clients        []*genai.Client
	modelName      string
	dimensions     int32
	keyIdx         int
	exhaustedUntil time.Time
	mu             sync.Mutex
}

// NewGeminiEmbedder initializes a GeminiEmbedder across provided API keys.
func NewGeminiEmbedder(apiKeys []string, modelName string) (*GeminiEmbedder, error) {
	if strings.TrimSpace(modelName) == "" || modelName == "text-embedding-004" {
		modelName = DefaultEmbeddingModel
	}
	ctx := context.Background()
	var clients []*genai.Client

	for _, key := range apiKeys {
		k := strings.TrimSpace(key)
		if k == "" {
			continue
		}
		c, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey:  k,
			Backend: genai.BackendGeminiAPI,
		})
		if err != nil {
			slog.Warn("failed to create gemini embedding client for key", "error", err)
			continue
		}
		clients = append(clients, c)
	}

	if len(clients) == 0 {
		return nil, fmt.Errorf("no valid gemini api keys for embedder")
	}

	return &GeminiEmbedder{
		clients:    clients,
		modelName:  modelName,
		dimensions: DefaultEmbeddingDimensions,
	}, nil
}

// Embed generates a normalized embedding vector, degrading quickly if the project hits 403/billing blocks.
func (e *GeminiEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty text for embedding")
	}

	e.mu.Lock()
	if !e.exhaustedUntil.IsZero() && time.Now().Before(e.exhaustedUntil) {
		e.mu.Unlock()
		return nil, fmt.Errorf("gemini embedder on run cooldown")
	}
	attempts := len(e.clients)
	e.mu.Unlock()

	var lastErr error
	for i := 0; i < attempts; i++ {
		e.mu.Lock()
		client := e.clients[e.keyIdx]
		e.keyIdx = (e.keyIdx + 1) % len(e.clients)
		e.mu.Unlock()

		attemptCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		var embCfg *genai.EmbedContentConfig
		if e.dimensions > 0 {
			dims := e.dimensions
			embCfg = &genai.EmbedContentConfig{OutputDimensionality: &dims}
		}
		resp, err := client.Models.EmbedContent(attemptCtx, e.modelName, genai.Text(text), embCfg)
		cancel()
		if err != nil {
			lastErr = err
			lower := strings.ToLower(err.Error())
			if strings.Contains(lower, "403") || strings.Contains(lower, "permission_denied") || strings.Contains(lower, "free tier") {
				slog.Warn("gemini embedding blocked by project permissions/403, placing on cooldown", "error", err)
				continue
			}
			slog.Warn("gemini EmbedContent attempt failed", "attempt", i+1, "error", err)
			continue
		}
		if resp == nil || len(resp.Embeddings) == 0 || len(resp.Embeddings[0].Values) == 0 {
			lastErr = fmt.Errorf("empty embedding returned")
			continue
		}
		return resp.Embeddings[0].Values, nil
	}

	if lastErr != nil && (strings.Contains(strings.ToLower(lastErr.Error()), "403") || strings.Contains(strings.ToLower(lastErr.Error()), "permission_denied")) {
		e.mu.Lock()
		e.exhaustedUntil = time.Now().Add(30 * time.Minute)
		e.mu.Unlock()
	}

	return nil, fmt.Errorf("all gemini embedding attempts failed: %w", lastErr)
}

// CosineSimilarity computes cosine similarity in [-1.0, 1.0].
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0.0
	}

	var dot, normA, normB float64
	for i := 0; i < len(a); i++ {
		valA := float64(a[i])
		valB := float64(b[i])
		dot += valA * valB
		normA += valA * valA
		normB += valB * valB
	}
	if normA == 0.0 || normB == 0.0 {
		return 0.0
	}
	sim := dot / (math.Sqrt(normA) * math.Sqrt(normB))
	if sim > 1.0 {
		return 1.0
	}
	if sim < -1.0 {
		return -1.0
	}
	return sim
}

// TokenizeClusterKey extracts lowercase alphanumeric tokens (>=2 chars) from a cluster key string.
func TokenizeClusterKey(key string) map[string]struct{} {
	tokens := make(map[string]struct{})
	var current strings.Builder

	for _, r := range strings.ToLower(key) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			if current.Len() >= 2 {
				tokens[current.String()] = struct{}{}
			}
			current.Reset()
		}
	}
	if current.Len() >= 2 {
		tokens[current.String()] = struct{}{}
	}
	return tokens
}

// ClusterKeySimilarity computes Jaccard similarity between two story_cluster_key strings.
func ClusterKeySimilarity(keyA, keyB string) float64 {
	normA := strings.TrimSpace(strings.ToLower(keyA))
	normB := strings.TrimSpace(strings.ToLower(keyB))
	if normA == "" || normB == "" {
		return 0.0
	}
	if normA == normB {
		return 1.0
	}
	tokensA := TokenizeClusterKey(normA)
	tokensB := TokenizeClusterKey(normB)
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0.0
	}
	intersection := 0
	for tok := range tokensA {
		if _, ok := tokensB[tok]; ok {
			intersection++
		}
	}
	union := len(tokensA) + len(tokensB) - intersection
	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}
