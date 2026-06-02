package deals

import (
	"context"
	"database/sql"
	"log/slog"
	"sort"
	"time"
)

const (
	historyDays  = 60 // Don't re-publish a game within 60 days
	maxTopDeals  = 5
	redditDelay  = 1500 * time.Millisecond // Polite delay between Reddit requests
)

// FetchAndFilter is the main orchestrator:
// 1. Fetches deals from ITAD (primary) or CheapShark (fallback).
// 2. Filters by minCut, minMeta, and DB-backed history.
// 3. Enriches top results with Reddit quotes.
// 4. Returns at most 5 deals, sorted by (cut + metacritic) descending.
func FetchAndFilter(ctx context.Context, database *sql.DB, itadKey string, minCut, minMeta int) ([]Deal, error) {
	var allDeals []Deal
	var err error

	// --- Step 1: Fetch raw deals ---
	if itadKey != "" {
		allDeals, err = FetchITADDeals(itadKey)
		if err != nil {
			slog.Warn("ITAD fetch failed, falling back to CheapShark", "error", err)
			allDeals = nil
		} else {
			slog.Info("ITAD fetched", "count", len(allDeals))
		}
	} else {
		slog.Info("ITAD API key missing, using CheapShark directly")
	}

	if len(allDeals) == 0 {
		allDeals, err = FetchCheapSharkDeals()
		if err != nil {
			return nil, err
		}
		slog.Info("CheapShark fetched", "count", len(allDeals))
	}

	if len(allDeals) == 0 {
		return nil, nil
	}

	// --- Step 2: Filter by discount % and metacritic ---
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

	// --- Step 4: Sort by combined score (cut + metacritic) ---
	sort.Slice(fresh, func(i, j int) bool {
		scoreI := fresh[i].Cut + fresh[i].Metacritic
		scoreJ := fresh[j].Cut + fresh[j].Metacritic
		return scoreI > scoreJ
	})

	// --- Step 5: Take top N and enrich with Reddit ---
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
