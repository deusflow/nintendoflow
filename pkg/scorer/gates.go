package scorer

import "strings"

// IsRoutineNoiseTitle detects low-signal filler posts (NSO icon drops, Nintendo Music tracks,
// weekly retail charts, walkthrough/guides, standalone game reviews, cosplay/merch promos).
func IsRoutineNoiseTitle(title string) (bool, string) {
	lower := strings.ToLower(strings.TrimSpace(title))
	if lower == "" {
		return true, "empty title"
	}

	// 1. NSO profile/user icons & Nintendo Music track drops
	if (strings.Contains(lower, "icon") || strings.Contains(lower, "icons")) &&
		(strings.Contains(lower, "nintendo switch online") || strings.Contains(lower, "nso") || strings.Contains(lower, "wave")) {
		return true, "routine_nso_icons"
	}
	if strings.Contains(lower, "nintendo music") &&
		(strings.Contains(lower, "added") || strings.Contains(lower, "adds") || strings.Contains(lower, "soundtrack") || strings.Contains(lower, "tracks")) {
		return true, "routine_nintendo_music"
	}

	// 2. Walkthroughs / guides / tip lists
	guideSignals := []string{
		"walkthrough", "guide:", ": guide", "tips and tricks", "tips & tricks",
		"where to find", "how to get", "how to unlock", "how to beat",
		"all bag capacity", "every upgrade", "tier list", "best settings for",
	}
	for _, g := range guideSignals {
		if strings.Contains(lower, g) {
			return true, "game_guide_or_listicle"
		}
	}

	// 3. Game reviews (unless reviewing Switch 2 hardware/console itself)
	if (containsKeyword(lower, "review") || strings.Contains(lower, "review:") || strings.Contains(lower, "[review]")) &&
		!strings.Contains(lower, "switch 2 console") && !strings.Contains(lower, "switch 2 hardware") {
		// Allow legal/FTC/business reviews while dropping "[Game] review"
		if !containsKeyword(lower, "ftc") && !containsKeyword(lower, "court") && !containsKeyword(lower, "antitrust") && !containsKeyword(lower, "digital foundry") {
			return true, "standalone_review"
		}
	}

	// 4. Weekly retail / eShop charts
	chartSignals := []string{
		"uk charts", "weekly charts", "eshop charts", "top 10", "top 20", "top 30",
		"best-selling games this week", "best sellers this week",
	}
	for _, c := range chartSignals {
		if strings.Contains(lower, c) {
			return true, "weekly_sales_chart"
		}
	}

	// 5. Merch / cosplay fluff
	if containsKeyword(lower, "cosplay") || containsKeyword(lower, "plush") || containsKeyword(lower, "merchandise") {
		return true, "merch_or_cosplay"
	}

	return false, ""
}

// CalculateImpactScore calculates non-routine editorial priority points combining category priority,
// technical breakthrough weight, and cross-topic signal bonus.
func CalculateImpactScore(title, body string, cat Category, res Result) int {
	score := CategoryPriorityBonus(cat)

	if res.TechScore >= 120 {
		score += 25
	} else if res.TechScore >= 60 {
		score += 12
	}

	// Cross-domain stories (e.g. hardware + company + legal) are inherently more interesting than pure game clips.
	if res.WeirdnessScore > 0 {
		score += res.WeirdnessScore
	}

	// Only reward comparison articles when backed by genuine hardware/technical signals.
	if res.HasComparison && res.TechScore >= 60 {
		score += 10
	}

	// Penalize obvious discount/promo fluff if classified under games.
	lowerTitle := strings.ToLower(title)
	if cat == CategoryGames && (strings.Contains(lowerTitle, "on sale") || strings.Contains(lowerTitle, "lowest price")) {
		score -= 25
	}

	_ = body
	return score
}

// PassesEditorialGate validates post-AI metadata before saving/publishing.
func PassesEditorialGate(title string, cat Category, audienceScore int, concreteAnchor string) (bool, string) {
	if strings.EqualFold(strings.TrimSpace(concreteAnchor), "insufficient_substance") {
		return false, "insufficient_substance"
	}
	if noisy, reason := IsRoutineNoiseTitle(title); noisy {
		return false, reason
	}
	if audienceScore > 0 && audienceScore < 4 {
		return false, "low_audience_score"
	}
	if cat == CategoryGames && audienceScore > 0 && audienceScore < 6 {
		return false, "routine_game_news"
	}
	return true, "accepted"
}
