package pipeline

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/deuswork/nintendoflow/pkg/fetcher"
	"github.com/deuswork/nintendoflow/pkg/scorer"
)

// applyCrossSourceBoost replaces dead hype calculation: events reported by multiple distinct
// sources receive a real ranking bonus before top-N candidate selection.
func applyCrossSourceBoost(candidates []candidate, allItems []fetcher.Item) []candidate {
	for i := range candidates {
		hype := calculateHype(candidates[i].item, allItems)
		candidates[i].hypeCount = hype
		if hype > 1 {
			boost := (hype - 1) * 22
			if boost > 66 {
				boost = 66
			}
			candidates[i].rankScore += boost
		}
	}
	return candidates
}

// applyCategoryAndFranchiseAntiFatigue keeps a healthy editorial mix across the whole Nintendo world
// while keeping standout games in rotation without flooding one franchise (e.g. Pikmin).
func applyCategoryAndFranchiseAntiFatigue(candidates []candidate, recentCategories []string, recentFranchises map[string]int) []candidate {
	recentGamesStreak := 0
	for _, c := range recentCategories {
		if strings.EqualFold(c, string(scorer.CategoryGames)) {
			recentGamesStreak++
		} else {
			break
		}
	}
	lastCat := ""
	if len(recentCategories) > 0 {
		lastCat = strings.ToLower(strings.TrimSpace(recentCategories[0]))
	}

	for i := range candidates {
		cat := strings.ToLower(string(candidates[i].category))

		// Games stay in rotation, just throttled when the channel recently posted games back-to-back.
		if cat == string(scorer.CategoryGames) {
			switch {
			case recentGamesStreak >= 2:
				candidates[i].rankScore -= 60
			case recentGamesStreak == 1:
				candidates[i].rankScore -= 35
			}
		} else if lastCat != "" && cat == lastCat && cat != string(scorer.CategoryDirect) && cat != string(scorer.CategoryTech) {
			candidates[i].rankScore -= 15
		}

		// Cooldown repeated franchises within 72h (e.g. Pikmin, Zelda).
		if tag := candidates[i].franchiseTag; tag != "" && len(recentFranchises) > 0 {
			if cnt := recentFranchises[tag]; cnt > 0 {
				penalty := cnt * 45
				if penalty > 90 {
					penalty = 90
				}
				candidates[i].rankScore -= penalty
				candidates[i].recentSimilarPosted = true
			}
		}
	}
	return candidates
}

// capCandidatesPerSource ensures no single source monopolizes the selector top pool.
func capCandidatesPerSource(candidates []candidate, maxPerSource int) []candidate {
	if maxPerSource <= 0 {
		return candidates
	}
	counts := make(map[string]int)
	out := make([]candidate, 0, len(candidates))
	overflow := make([]candidate, 0)
	for _, c := range candidates {
		if counts[c.item.SourceName] < maxPerSource {
			counts[c.item.SourceName]++
			out = append(out, c)
		} else {
			overflow = append(overflow, c)
		}
	}
	return append(out, overflow...)
}

// scrapeTopCandidates fetches full article bodies in parallel before AI selection
// and deprioritizes paywalled or anti-bot stubs whenever open alternatives exist.
func scrapeTopCandidates(ctx context.Context, candidates []candidate, maxToScrape int) []candidate {
	if len(candidates) == 0 || maxToScrape <= 0 {
		return candidates
	}
	n := maxToScrape
	if len(candidates) < n {
		n = len(candidates)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	hasNonPaywalled := false

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			scrapeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			body, err := fetcher.FetchArticleContent(scrapeCtx, candidates[idx].item.Link)
			if err != nil {
				return
			}
			paywalled, reason := fetcher.IsPaywalledOrStub(body, candidates[idx].item.Link)

			mu.Lock()
			candidates[idx].scrapedBody = body
			candidates[idx].paywallReason = reason
			candidates[idx].isPaywalled = paywalled
			if !paywalled {
				hasNonPaywalled = true
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	// Filter or demote confirmed paywall/stub items only if clean non-paywalled alternatives exist.
	if hasNonPaywalled {
		clean := make([]candidate, 0, len(candidates))
		demoted := make([]candidate, 0)
		for _, c := range candidates {
			if c.isPaywalled && len([]rune(strings.TrimSpace(c.item.Description))) < 180 {
				c.rankScore -= 80
				demoted = append(demoted, c)
			} else {
				clean = append(clean, c)
			}
		}
		if len(clean) > 0 {
			return append(clean, demoted...)
		}
	}

	return candidates
}
