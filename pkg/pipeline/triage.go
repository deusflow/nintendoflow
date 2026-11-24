package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/deuswork/nintendoflow/pkg/ai"
	"github.com/deuswork/nintendoflow/pkg/fetcher"
)

type triageCandidate struct {
	Item      fetcher.Item
	URLHash   string
	TitleHash string
	RawScore  int
	TechScore int
	Priority  int
}

type triageResponse struct {
	Selected []int `json:"selected"`
}

const triagePromptTemplate = `You are the senior editor for a Ukrainian Nintendo World Telegram channel (DEUSFLOW).
Review the numbered headlines below that failed strict keyword thresholds and rescue UP TO 3 hidden gems that our Nintendo audience MUST NOT miss.

=== КІНЕЦЬ ІНСТРУКЦІЙ ===

=== WHAT TO RESCUE (HIGH VALUE) ===
- Switch 2 hardware/silicon/graphics discoveries (DLSS, DLSS 4, Nvidia Tegra, datamines, SDK files, frame generation, Digital Foundry specs)
- Nintendo corporate & business news (Furukawa, Miyamoto, earnings, studio acquisitions, Bloomberg/Nikkei exclusives)
- Legal/IP battles, patents, lawsuits, anti-piracy rulings involving Nintendo
- Cultural projects (Nintendo Museum, Super Nintendo World, Zelda/Mario movies, Nintendo Direct surprises)
- Genuinely major first-party or exclusive game announcements/milestones

=== WHAT TO IGNORE ===
- Non-Nintendo multiplatform filler (PS5/Xbox/PC-only news without Switch/Switch 2 connection)
- Minor NSO profile icons, Nintendo Music track additions, weekly sales charts, strategy guides, routine indie ports

Headlines:
%s

Return ONLY valid JSON: {"selected": [0, 5]} (0-based indices) or {"selected": []} if nothing stands out. No explanations.`

// prioritizeTriagePool orders rejected items so high-priority tech, company, and fresh insider
// headlines occupy the top slots sent to AI triage instead of arbitrary goroutine arrival order.
func prioritizeTriagePool(pool []triageCandidate, maxItems int) []triageCandidate {
	if len(pool) <= 1 {
		return pool
	}
	sorted := make([]triageCandidate, len(pool))
	copy(sorted, pool)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority > sorted[j].Priority
		}
		if sorted[i].TechScore != sorted[j].TechScore {
			return sorted[i].TechScore > sorted[j].TechScore
		}
		if sorted[i].Item.PublishedAt == nil {
			return false
		}
		if sorted[j].Item.PublishedAt == nil {
			return true
		}
		return sorted[i].Item.PublishedAt.After(*sorted[j].Item.PublishedAt)
	})
	if maxItems > 0 && len(sorted) > maxItems {
		return sorted[:maxItems]
	}
	return sorted
}

// runTriage asks AI to rescue up to 3 high-interest Nintendo World items that slipped below keyword MinScore.
func runTriage(ctx context.Context, pool []triageCandidate, manager *ai.Manager) []int {
	if len(pool) == 0 || manager == nil {
		return nil
	}
	if manager.CallsUsed() >= manager.CallsBudget() {
		return nil
	}

	const maxTriageItems = 25
	pool = prioritizeTriagePool(pool, maxTriageItems)

	var sb strings.Builder
	for i, tc := range pool {
		fmt.Fprintf(&sb, "%d: [%s] %s\n", i, tc.Item.SourceName, strings.TrimSpace(tc.Item.Title))
	}

	prompt := fmt.Sprintf(triagePromptTemplate, sb.String())
	raw, err := manager.GenerateRaw(ctx, prompt)
	if err != nil {
		slog.Warn("AI triage failed, continuing without rescued items", "error", err)
		return nil
	}

	cleaned := ai.SanitizeJSON(raw)
	if !strings.HasPrefix(cleaned, "{") {
		slog.Warn("AI triage returned non-JSON output", "raw", raw)
		return nil
	}

	var resp triageResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		slog.Warn("AI triage JSON parse failed", "error", err, "sanitized", cleaned)
		return nil
	}

	seen := make(map[int]struct{})
	valid := make([]int, 0, 3)
	for _, idx := range resp.Selected {
		if idx < 0 || idx >= len(pool) {
			continue
		}
		if _, dup := seen[idx]; dup {
			continue
		}
		seen[idx] = struct{}{}
		valid = append(valid, idx)
		if len(valid) >= 3 {
			break
		}
	}

	if len(valid) > 0 {
		titles := make([]string, len(valid))
		for i, idx := range valid {
			titles[i] = pool[idx].Item.Title
		}
		slog.Info("AI triage rescued hidden candidates", "count", len(valid), "titles", strings.Join(titles, " | "))
	}

	return valid
}
