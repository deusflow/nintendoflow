package scorer

import (
	"testing"

	"github.com/deuswork/nintendoflow/pkg/config"
)

func TestMustPublishNonNintendoEventStillRequiresAnchorOnStrictFeed(t *testing.T) {
	topics := map[string]config.Topic{
		"events": {
			Enabled:  true,
			Priority: 130,
			Keywords: []config.Keyword{
				{Word: "game awards", Role: "must_publish", Weight: 70, EventTag: "game_awards"},
				{Word: "nintendo direct", Role: "must_publish", Weight: 100, EventTag: "nintendo_direct"},
			},
		},
	}

	// Non-Nintendo Game Awards news on a strict general-gaming feed must be rejected.
	_, ok, reason := ShouldPost(
		"GTA 6 sweeps The Game Awards nominations on PS5",
		"Rockstar's latest title leads nominees at Game Awards.",
		topics,
		4,
		true,
	)
	if ok || reason != "missing_nintendo_anchor" {
		t.Fatalf("expected non-Nintendo Game Awards post to fail anchor check, got ok=%v reason=%s", ok, reason)
	}

	// Nintendo Direct has an explicit Nintendo keyword -> passes.
	_, okDirect, reasonDirect := ShouldPost(
		"Nintendo Direct set for Thursday morning",
		"Expect 40 minutes of upcoming reveals.",
		topics,
		4,
		true,
	)
	if !okDirect || reasonDirect != "must_publish_event" {
		t.Fatalf("expected Nintendo Direct to pass via must_publish_event, got ok=%v reason=%s", okDirect, reasonDirect)
	}
}

func TestDLSS4AndTechOutscoreRoutineIcons(t *testing.T) {
	topics, err := config.LoadKeywords("../../keywords.yaml")
	if err != nil {
		t.Fatalf("LoadKeywords: %v", err)
	}

	dlssRes, dlssOK, _ := ShouldPost(
		"Nintendo Switch 2 hides DLSS 4 Preset K and Tiny DLSS capabilities in SDK files",
		"Datamining of Nvidia Tegra architecture confirms transformer model upscaling support.",
		topics,
		10,
		true,
	)
	if !dlssOK {
		t.Fatalf("expected DLSS4 Switch 2 tech scoop to pass")
	}
	if dlssRes.Category != CategoryTech {
		t.Fatalf("expected CategoryTech, got %q", dlssRes.Category)
	}

	iconsRes := Evaluate(
		"Nintendo Switch Online adds new Pikmin user icons for members",
		"Redeem Platinum Points for icons this week.",
		topics,
	)
	if iconsRes.Score >= dlssRes.Score {
		t.Fatalf("expected DLSS4 score (%d) to massively beat NSO icons (%d)", dlssRes.Score, iconsRes.Score)
	}
	if iconsRes.ImpactScore >= dlssRes.ImpactScore {
		t.Fatalf("expected DLSS4 impact (%d) to beat icons impact (%d)", dlssRes.ImpactScore, iconsRes.ImpactScore)
	}
}

func TestIsRoutineNoiseTitleDetectsFillerWhileAllowingRealGames(t *testing.T) {
	noisy := []string{
		"Nintendo Switch Online adds Pikmin 4 profile icons",
		"Nintendo Music adds Mario Kart Wii soundtrack today",
		"Zelda Ocarina of Time: All bag capacity upgrades guide",
		"Pikmin 4 Switch 2 Edition Review",
		"UK Charts: Top 10 best-selling games this week",
	}
	for _, title := range noisy {
		if ok, _ := IsRoutineNoiseTitle(title); !ok {
			t.Errorf("expected routine filler %q to be detected", title)
		}
	}

	good := []string{
		"Nintendo confirms new Metroid Prime 4 release window and Switch 2 enhancements",
		"Digital Foundry reviews Switch 2 hardware DLSS 4 reconstruction quality",
		"FTC review of Nintendo eShop refund policy concludes",
	}
	for _, title := range good {
		if ok, reason := IsRoutineNoiseTitle(title); ok {
			t.Errorf("did not expect real news %q to be flagged as filler (reason=%s)", title, reason)
		}
	}
}

func TestExtractFranchiseTagAndCategoryInference(t *testing.T) {
	res := Evaluate("Nintendo quarterly earnings report shows strong Switch 2 momentum", "Furukawa commented on hardware sales.", nil)
	if res.Category != CategoryCompany {
		t.Fatalf("expected company category, got %s", res.Category)
	}

	if tag := ExtractFranchiseTag("Pikmin bloom event launches globally", ""); tag != "pikmin" {
		t.Fatalf("expected pikmin franchise tag, got %q", tag)
	}
	if tag := ExtractFranchiseTag("Nintendo Museum opens new Kyoto exhibit", ""); tag != "" {
		t.Fatalf("expected empty franchise tag for museum news, got %q", tag)
	}
}

func TestGeneralizedDiscoverySignalsMatchWithoutDLSSAndVSComparisonStaysGames(t *testing.T) {
	topics, err := config.LoadKeywords("../../keywords.yaml")
	if err != nil {
		t.Fatalf("LoadKeywords: %v", err)
	}

	// Future hardware/firmware discovery without "dlss" or "tegra"
	hidden := Evaluate(
		"Nintendo Switch 2 firmware analysis reveals undocumented MicroSD Express power mode",
		"Reverse engineered GPU driver notes point to higher memory bandwidth.",
		topics,
	)
	if hidden.Category != CategoryTech {
		t.Fatalf("expected generalized discovery to classify as CategoryTech, got %s", hidden.Category)
	}
	if hidden.TechScore < 120 {
		t.Fatalf("expected high TechScore for undocumented hardware discovery, got %d", hidden.TechScore)
	}

	// Pure "vs PS5" frame rate comparison without deeper hardware engineering belongs under games, not tech.
	vsGame := Evaluate(
		"Fortnite on Switch vs PS5 graphics comparison",
		"Side by side comparison looks at frame rate and resolution.",
		topics,
	)
	if vsGame.Category == CategoryTech {
		t.Fatalf("expected superficial vs comparison NOT to classify as CategoryTech, got %s", vsGame.Category)
	}
	if hidden.ImpactScore <= vsGame.ImpactScore {
		t.Fatalf("expected hardware discovery impact (%d) > superficial comparison (%d)", hidden.ImpactScore, vsGame.ImpactScore)
	}
}
