package deals

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	historyDays = 60 // Don't re-publish a game within 60 days
	maxTopDeals = 10
	redditDelay = 1500 * time.Millisecond // Polite delay between Reddit requests
)

// dealScore calculates a combined ranking score that rewards BOTH game quality
// AND a meaningful discount. The formula:
//
//	score = metacritic × (cut/100) × savingsFactor
//
// Where savingsFactor = log2(absoluteSavings + 1) to reward bigger real savings
// (50% off a €60 game is more interesting than 50% off a €5 game).
//
// Examples:
//
//	90 meta, 50% off €60 (saves €30) → 90 × 0.50 × 4.95 = 223
//	90 meta, 10% off €60 (saves €6)  → 90 × 0.10 × 2.81 =  25  ← filtered out
//	85 meta, 60% off €40 (saves €24) → 85 × 0.60 × 4.64 = 237
//	70 meta, 80% off €20 (saves €16) → 70 × 0.80 × 4.09 = 229
//	75 meta, 30% off €50 (saves €15) → 75 × 0.30 × 4.00 =  90
func dealScore(d Deal) float64 {
	savings := d.OldPrice - d.NewPrice
	if savings < 0 {
		savings = 0
	}
	savingsFactor := math.Log2(savings + 1)
	return float64(d.Metacritic) * (float64(d.Cut) / 100.0) * savingsFactor
}

// FetchAndFilter is the main orchestrator:
// 1. Fetches deals from Nintendo Europe, IsThereAnyDeal, and CheapShark, and merges them.
// 2. Filters by minCut, minMeta, and DB-backed history (60 days).
// 3. Ranks by combined score: game quality × discount depth × absolute savings.
// 4. Enriches top 10 results with Reddit quotes.
// 5. Returns at most 10 deals.
func FetchAndFilter(ctx context.Context, database *sql.DB, itadKey string, minCut, minMeta int) ([]Deal, error) {
	var rawDeals []Deal
	var allSourcesFailed = true

	// -- Source 1: Nintendo Europe Official Solr API --
	noeDeals, err := FetchNintendoOfficialDeals()
	if err != nil {
		slog.Warn("Nintendo Official fetch failed", "error", err)
	} else {
		slog.Info("Nintendo Official fetched", "count", len(noeDeals))
		rawDeals = append(rawDeals, noeDeals...)
		allSourcesFailed = false
	}

	// Убрано использование ITAD и CheapShark, так как они возвращают PC-игры.
	// Оставлен только Nintendo Official, который гарантирует 100% игры для Switch.

	if allSourcesFailed {
		return nil, fmt.Errorf("all deal sources failed to fetch")
	}

	// --- Step 1.1: Deduplicate deals by title ---
	merged := make(map[string]Deal)
	for _, d := range rawDeals {
		norm := normalizeTitle(d.Title)
		if norm == "" {
			continue
		}

		existing, exists := merged[norm]
		if !exists {
			merged[norm] = d
			continue
		}
		
		// If duplicate from same source, prefer the cheaper one
		if d.NewPrice < existing.NewPrice {
			merged[norm] = d
		}
	}

	var allDeals []Deal
	for _, d := range merged {
		allDeals = append(allDeals, d)
	}
	slog.Info("deals after deduplication", "count", len(allDeals))

	// --- Step 2: Filter by minimum discount % and metacritic ---
	var filtered []Deal
	for _, d := range allDeals {
		if d.Cut < minCut {
			continue
		}
		if d.Metacritic < minMeta {
			continue
		}
		filtered = append(filtered, d)
	}
	slog.Info("deals after cut/meta filter", "count", len(filtered), "minCut", minCut, "minMeta", minMeta)

	// --- Step 3: Filter by DB history (60 days) ---
	var fresh []Deal
	for _, d := range filtered {
		published, err := IsDealRecentlyPublished(ctx, database, d.ID, historyDays)
		if err != nil {
			slog.Warn("history check failed, keeping deal", "deal", d.Title, "error", err)
			fresh = append(fresh, d)
			continue
		}
		if published {
			slog.Debug("deal skipped (published recently)", "deal", d.Title)
			continue
		}
		fresh = append(fresh, d)
	}
	slog.Info("deals after history filter", "count", len(fresh))

	if len(fresh) == 0 {
		return nil, nil
	}

	// --- Step 4: Rank by combined score (quality × discount × savings) ---
	sort.Slice(fresh, func(i, j int) bool {
		return dealScore(fresh[i]) > dealScore(fresh[j])
	})

	// Log top candidates with their scores for debugging
	logN := len(fresh)
	if logN > 15 {
		logN = 15
	}
	for i := 0; i < logN; i++ {
		d := fresh[i]
		slog.Info("deal candidate",
			"rank", i+1,
			"title", d.Title,
			"score", int(dealScore(d)),
			"meta", d.Metacritic,
			"cut", d.Cut,
			"oldPrice", d.OldPrice,
			"newPrice", d.NewPrice,
		)
	}

	// --- Step 5: Take top 10 and enrich with Reddit ---
	top := fresh
	if len(top) > maxTopDeals {
		top = top[:maxTopDeals]
	}

	for i := range top {
		quote, err := SearchReddit(top[i].Title)
		if err != nil {
			slog.Warn("reddit search failed", "game", top[i].Title, "error", err)
		}
		if quote != "" {
			top[i].RedditQuote = quote
		} else {
			top[i].RedditQuote = "Вигідна знижка в Nintendo eShop!"
		}

		// Polite delay between Reddit requests
		if i < len(top)-1 {
			time.Sleep(redditDelay)
		}
	}

	return top, nil
}

// normalizeTitle lowercases the title and keeps only alphanumeric characters.
func normalizeTitle(title string) string {
	title = strings.ToLower(title)
	var sb strings.Builder
	for _, r := range title {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

