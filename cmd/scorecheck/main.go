package main

import (
	"fmt"

	"github.com/deuswork/nintendoflow/internal/config"
	"github.com/deuswork/nintendoflow/internal/scorer"
)

func main() {
	topics, err := config.LoadKeywords("keywords.yaml")
	if err != nil {
		panic(err)
	}

	fmt.Printf("hardware_anchor priority=%d\n", topics["hardware_anchor"].Priority)
	fmt.Printf("hardware_tech priority=%d\n", topics["hardware_tech"].Priority)

	cases := []struct {
		name          string
		title         string
		body          string
		requireAnchor bool
	}{
		{
			name:          "tech with switch 2 anchor",
			title:         "Switch 2 handheld performance looks better than expected",
			body:          "Developer insight says the portable mode has hidden capabilities and more power.",
			requireAnchor: true,
		},
		{
			name:          "tech without anchor strict feed",
			title:         "Developer insight reveals hidden capabilities in portable mode",
			body:          "Better than expected handheld performance surprised by hardware.",
			requireAnchor: true,
		},
		{
			name:          "tech without anchor trusted feed",
			title:         "Developer insight reveals hidden capabilities in portable mode",
			body:          "Better than expected handheld performance surprised by hardware.",
			requireAnchor: false,
		},
		{
			name:          "comparison only strict feed",
			title:         "Performance comparison benchmark shows cleaner fps",
			body:          "Frame rate and resolution analysis compared steam deck versus ps5.",
			requireAnchor: true,
		},
		{
			name:          "high tech strict feed bypass",
			title:         "Developer insight reveals hidden capabilities in portable mode",
			body:          "Better than expected handheld performance with more power and stronger than expected results.",
			requireAnchor: true,
		},
	}

	for _, tc := range cases {
		result, ok, reason := scorer.ShouldPost(tc.title, tc.body, topics, 4, tc.requireAnchor)
		fmt.Printf("\nCASE: %s\n", tc.name)
		fmt.Printf("score=%d tech_score=%d anchor=%v comparison=%v ok=%v reason=%s\n", result.Score, result.TechScore, result.HasAnchor, result.HasComparison, ok, reason)
	}
}
