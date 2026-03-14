package fetcher

// Source represents a single RSS news source.
type Source struct {
	Name                 string
	FeedURL              string
	Type                 string // official / media / insider / aggregator
	Priority             int
	NeedsRedirectResolve bool // true for Google News RSS
}

// Sources is the complete list of RSS feeds to poll.
var Sources = []Source{
	// OFFICIAL
	{Name: "Nintendo Official Blog", FeedURL: "https://www.nintendo.com/en-GB/news/", Type: "official", Priority: 10},
	// MEDIA
	{Name: "Nintendo Life", FeedURL: "https://www.nintendolife.com/feeds/latest", Type: "media", Priority: 8},
	{Name: "My Nintendo News", FeedURL: "https://mynintendonews.com/feed/", Type: "media", Priority: 7},
	{Name: "GoNintendo", FeedURL: "https://gonintendo.com/feed", Type: "media", Priority: 7},
	{Name: "Nintendo Everything", FeedURL: "https://nintendoeverything.com/feed/", Type: "media", Priority: 7},
	{Name: "Eurogamer", FeedURL: "https://www.eurogamer.net/?format=rss", Type: "media", Priority: 6},
	// INSIDERS
	{Name: "Video Games Chronicle", FeedURL: "https://www.videogameschronicle.com/feed/", Type: "insider", Priority: 9},
	// GOOGLE NEWS RSS (aggregator — redirect resolve needed)
	{
		Name:                 "Google News - Nintendo insider",
		FeedURL:              "https://news.google.com/rss/search?q=Nintendo+Switch+2+insider+leak&hl=en-US&gl=US&ceid=US:en",
		Type:                 "aggregator",
		Priority:             5,
		NeedsRedirectResolve: true,
	},
	{
		Name:                 "Google News - Nintendo Direct",
		FeedURL:              "https://news.google.com/rss/search?q=Nintendo+Direct+announce+2026&hl=en-US&gl=US&ceid=US:en",
		Type:                 "aggregator",
		Priority:             6,
		NeedsRedirectResolve: true,
	},
	{
		Name:                 "Google News - Nintendo hardware",
		FeedURL:              "https://news.google.com/rss/search?q=Nintendo+hardware+new+console&hl=en-US&gl=US&ceid=US:en",
		Type:                 "aggregator",
		Priority:             5,
		NeedsRedirectResolve: true,
	},
}
