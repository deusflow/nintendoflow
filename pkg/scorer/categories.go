package scorer

import "strings"

// Category represents a canonical editorial section of Nintendo World coverage.
type Category string

const (
	CategoryCompany   Category = "company"
	CategoryTech      Category = "tech"
	CategoryLegal     Category = "legal"
	CategoryEcosystem Category = "ecosystem"
	CategoryCulture   Category = "culture"
	CategoryDirect    Category = "direct"
	CategoryGames     Category = "games"
)

// CategoryPriorityBonus guides the feed mix toward the whole Nintendo world
// while still letting standout game announcements shine.
func CategoryPriorityBonus(c Category) int {
	switch c {
	case CategoryTech:
		return 35
	case CategoryCompany:
		return 30
	case CategoryLegal:
		return 28
	case CategoryDirect:
		return 30
	case CategoryCulture:
		return 20
	case CategoryEcosystem:
		return 15
	case CategoryGames:
		return 0
	default:
		return 0
	}
}

// InferCategory classifies an article into one of the Nintendo editorial categories
// based on title/body signals and matched scorer topics.
func InferCategory(title, body string, matchedTopics map[string]bool) Category {
	text := strings.ToLower(title + " " + body)

	switch {
	case hasAnySignal(text, "nintendo direct", "indie world", "partner showcase", "partner direct", "direct mini", "nintendo live", "nintendo showcase"):
		return CategoryDirect
	case hasAnySignal(text, "lawsuit", "sued", "suing", "patent", "patent filing", "injunction", "dmca", "court", "settlement", "copyright", "piracy", "pocketpair", "palworld"):
		return CategoryLegal
	case hasAnySignal(text, "dlss", "dlss 4", "dlss4", "tiny dlss", "nvidia", "tegra", "t239", "teraflops", "clock speed", "digital foundry", "datamining", "datamine", "datamined", "sdk", "ray reconstruction", "frame generation", "upscaling", "transformer model", "preset k", "preset m", "devkit", "dev kit", "hidden capabilities", "teardown", "die shot", "soc", "chipset", "silicon", "lpddr5x", "lpddr5", "microsd express", "ufs 3.1", "nvn", "nvn2", "tensor", "neural rendering", "gpu driver", "memory bandwidth", "undocumented", "hidden feature", "hidden mode", "secret feature", "reverse engineering", "reverse engineered", "firmware analysis", "fcc filing", "overclock"):
		return CategoryTech
	case hasAnySignal(text, "furukawa", "miyamoto", "doug bowser", "financial results", "earnings report", "fiscal year", "shareholder", "quarterly", "operating profit", "stock price", "shares", "acquisition", "acquires", "studio closure"):
		return CategoryCompany
	case hasAnySignal(text, "nintendo museum", "super nintendo world", "universal studios", "zelda movie", "mario movie", "animated film", "live-action", "theme park"):
		return CategoryCulture
	case hasAnySignal(text, "firmware update", "system update", "system software", "eshop policy", "backward compatibility", "virtual game card", "gamechat", "nintendo account", "nintendo switch online", "switch online"):
		return CategoryEcosystem
	}

	if matchedTopics != nil {
		if matchedTopics["events"] {
			return CategoryDirect
		}
		if matchedTopics["patents"] {
			return CategoryLegal
		}
		if matchedTopics["hardware_tech"] || matchedTopics["supply_chain"] {
			return CategoryTech
		}
		if matchedTopics["nintendo_company"] {
			return CategoryCompany
		}
		if matchedTopics["culture_media"] {
			return CategoryCulture
		}
	}

	return CategoryGames
}

var trackedFranchises = []struct {
	tag   string
	terms []string
}{
	{tag: "pikmin", terms: []string{"pikmin"}},
	{tag: "zelda", terms: []string{"zelda", "hyrule", "tears of the kingdom", "breath of the wild", "ocarina of time"}},
	{tag: "mario", terms: []string{"mario kart", "super mario", "paper mario", "mario party", "luigi"}},
	{tag: "pokemon", terms: []string{"pokemon", "pokémon", "pokopia", "pikachu", "game freak"}},
	{tag: "metroid", terms: []string{"metroid", "samus"}},
	{tag: "splatoon", terms: []string{"splatoon"}},
	{tag: "animal-crossing", terms: []string{"animal crossing", "tom nook"}},
	{tag: "kirby", terms: []string{"kirby"}},
	{tag: "fire-emblem", terms: []string{"fire emblem"}},
	{tag: "xenoblade", terms: []string{"xenoblade"}},
	{tag: "donkey-kong", terms: []string{"donkey kong"}},
	{tag: "smash-bros", terms: []string{"smash bros"}},
}

// ExtractFranchiseTag returns the primary game franchise tag mentioned in the headline/text, if any.
func ExtractFranchiseTag(title, body string) string {
	lowerTitle := strings.ToLower(title)
	for _, f := range trackedFranchises {
		for _, term := range f.terms {
			if containsKeyword(lowerTitle, term) {
				return f.tag
			}
		}
	}
	lowerBody := strings.ToLower(body)
	for _, f := range trackedFranchises {
		for _, term := range f.terms {
			if containsKeyword(lowerBody, term) {
				return f.tag
			}
		}
	}
	return ""
}

func hasAnySignal(text string, signals ...string) bool {
	for _, sig := range signals {
		if containsKeyword(text, sig) {
			return true
		}
	}
	return false
}
