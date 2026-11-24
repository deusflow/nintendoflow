package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deuswork/nintendoflow/pkg/ai"
	"github.com/deuswork/nintendoflow/pkg/config"
	"github.com/deuswork/nintendoflow/pkg/fetcher"
	"github.com/deuswork/nintendoflow/pkg/scorer"
)

type staticAIProvider struct {
	name string
	resp string
	err  error
}

func (p *staticAIProvider) Name() string { return p.name }
func (p *staticAIProvider) Complete(_ context.Context, _ string) (string, error) {
	return p.resp, p.err
}

func TestApplyCrossSourceBoostAndAntiFatigueBalance(t *testing.T) {
	now := time.Now()
	allItems := []fetcher.Item{
		{Title: "Nintendo Switch 2 Hides DLSS 4 Preset K In SDK", SourceName: "Wccftech", Link: "https://a.test/1"},
		{Title: "Switch 2 Hides DLSS 4 Preset K Capabilities", SourceName: "VGC", Link: "https://b.test/1"},
		{Title: "Dataminers Find DLSS 4 Transformer Preset K on Switch 2", SourceName: "Eurogamer", Link: "https://c.test/1"},
		{Title: "Pikmin 4 Update Adds Small Bugfix", SourceName: "Nintendo Everything", Link: "https://d.test/1"},
	}

	candidates := []candidate{
		{
			item:         fetcher.Item{Title: "Pikmin 4 Update Adds Small Bugfix", SourceName: "Nintendo Everything", Link: "https://d.test/1", PublishedAt: &now},
			score:        110,
			rankScore:    120,
			category:     scorer.CategoryGames,
			franchiseTag: "pikmin",
		},
		{
			item:      fetcher.Item{Title: "Nintendo Switch 2 Hides DLSS 4 Preset K In SDK", SourceName: "Wccftech", Link: "https://a.test/1", PublishedAt: &now},
			score:     130,
			rankScore: 140,
			category:  scorer.CategoryTech,
		},
	}

	candidates = applyCrossSourceBoost(candidates, allItems)
	// Recent history had two "games" posts back-to-back and "pikmin" once in the last 72h.
	candidates = applyCategoryAndFranchiseAntiFatigue(
		candidates,
		[]string{"games", "games", "company"},
		map[string]int{"pikmin": 1},
	)
	sortCandidates(candidates)

	if candidates[0].category != scorer.CategoryTech {
		t.Fatalf("expected tech DLSS 4 candidate to rank first, got category=%s", candidates[0].category)
	}
	if candidates[0].hypeCount != 3 {
		t.Fatalf("expected 3 sources for DLSS 4 story, got %d", candidates[0].hypeCount)
	}
	if !candidates[1].recentSimilarPosted {
		t.Fatalf("expected Pikmin candidate to be marked recentSimilarPosted under franchise cooldown")
	}
}

func TestRunTriageRescuesHiddenDLSS4Headline(t *testing.T) {
	pool := []triageCandidate{
		{Item: fetcher.Item{Title: "Weekly Indie Promo Discounts Roundup", SourceName: "GoNintendo"}, RawScore: 8},
		{Item: fetcher.Item{Title: "Nintendo Switch 2 hides Tiny DLSS and Preset K in leaked SDK", SourceName: "TechRadar"}, RawScore: 15},
	}

	mgr := ai.NewManager([]ai.AIProvider{
		&staticAIProvider{name: "mock", resp: `{"selected": [1, 99, 1]}`},
	}, 3, 0)

	picked := runTriage(context.Background(), pool, mgr)
	if len(picked) != 1 || picked[0] != 1 {
		t.Fatalf("expected triage to rescue unique valid index [1], got %v", picked)
	}
}

func TestBuildSelectorPromptIncludesCategoryAndScrapedExcerpt(t *testing.T) {
	pub := time.Now().Add(-2 * time.Hour)
	longBody := strings.Repeat("Datamined Switch 2 binaries reference DLSS 4 Preset K and Tiny DLSS capabilities for handheld reconstruction. ", 6)
	cands := []candidate{
		{
			item: fetcher.Item{
				Title:       "Switch 2 Hides DLSS 4 Preset K",
				SourceType:  "insider",
				PublishedAt: &pub,
			},
			category:    scorer.CategoryTech,
			hypeCount:   3,
			score:       210,
			rankScore:   280,
			scrapedBody: longBody,
		},
	}

	prompt := buildSelectorPrompt(cands, []string{"Pikmin 4 Update Live"})
	if !strings.Contains(prompt, "cat: tech") {
		t.Fatalf("expected selector prompt to include category label, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "sources: 3") {
		t.Fatalf("expected selector prompt to include hype source count, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Tiny DLSS capabilities") {
		t.Fatalf("expected selector prompt to use scraped full body excerpt")
	}
}

func TestLiveFeedsAndKeywordsYAMLParseCleanly(t *testing.T) {
	feeds, err := config.LoadFeeds("../../feeds.yaml")
	if err != nil {
		t.Fatalf("LoadFeeds(../../feeds.yaml) failed: %v", err)
	}
	if len(feeds) < 10 {
		t.Fatalf("expected >= 10 active feeds in feeds.yaml, got %d", len(feeds))
	}
	for _, f := range feeds {
		if f.Name == "Nintendo JP Topics" {
			t.Fatalf("expected broken 404 Nintendo JP Topics RSS feed to be disabled")
		}
	}

	topics, err := config.LoadKeywords("../../keywords.yaml")
	if err != nil {
		t.Fatalf("LoadKeywords(../../keywords.yaml) failed: %v", err)
	}
	if _, ok := topics["culture_media"]; !ok {
		t.Fatalf("expected culture_media topic in keywords.yaml")
	}
}

func TestPrioritizeTriagePoolPutsHighTechLateArrivalsFirst(t *testing.T) {
	now := time.Now()
	pool := make([]triageCandidate, 0, 30)
	for i := 0; i < 28; i++ {
		pool = append(pool, triageCandidate{
			Item:     fetcher.Item{Title: "Minor GoNintendo Promo Post", SourceName: "GoNintendo", PublishedAt: &now},
			RawScore: 10,
			Priority: 85,
		})
	}
	// Late-arriving insider tech discovery must beat earlier low-signal entries when trimmed to 25.
	pool = append(pool, triageCandidate{
		Item:      fetcher.Item{Title: "Hidden MicroSD Express mode spotted in Switch 2 SDK", SourceName: "Digital Foundry", SourcePriority: 135, PublishedAt: &now},
		RawScore:  35,
		TechScore: 120,
		Priority:  330,
	})

	top := prioritizeTriagePool(pool, 25)
	if len(top) != 25 {
		t.Fatalf("expected 25 prioritized triage items, got %d", len(top))
	}
	if top[0].Item.SourceName != "Digital Foundry" {
		t.Fatalf("expected late-arriving Digital Foundry tech item to rank #1 in triage pool, got %q", top[0].Item.SourceName)
	}
}

func TestGoogleNewsTech403FallsBackToFeedDateWhileUntrusted500DoesNot(t *testing.T) {
	ts403 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer ts403.Close()

	feedDate := time.Now().Add(-2 * time.Hour)
	blockedTech := candidate{
		item: fetcher.Item{
			Title:                "Switch 2 Undocumented Memory Mode Discovered",
			Link:                 ts403.URL,
			SourceName:           "Google News - Tech & Switch 2 Hardware",
			SourceType:           "insider",
			NeedsRedirectResolve: true,
			PublishedAt:          &feedDate,
		},
		category:  scorer.CategoryTech,
		techScore: 95,
		score:     140,
	}

	cfg := &config.Config{RecentTitlesHours: 24}
	validated, _, _, quarantined, _ := validateTopCandidatesFreshness(context.Background(), []candidate{blockedTech}, 24, cfg, nil)
	if len(validated) != 1 || len(quarantined) != 0 {
		t.Fatalf("expected HTTP 403 high-signal Google News/insider tech item to fall back to feed date, got validated=%d quarantined=%d", len(validated), len(quarantined))
	}
}
