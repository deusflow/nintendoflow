package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/ai"
)

type FreshnessCase struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	Source       string `json:"source"`
	PublishedAt  string `json:"published_at"`
	ExpectedSkip bool   `json:"expected_skip"`
	Description  string `json:"description"`
}

func loadEnvKey(path, keyName string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if strings.TrimSpace(parts[0]) == keyName {
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`)
			return val
		}
	}
	return ""
}

func getGeminiKey() (string, string) {
	if k := os.Getenv("GEMINI_API_KEY"); k != "" {
		return k, "environment variable GEMINI_API_KEY"
	}
	if k := loadEnvKey(".env", "GEMINI_API_KEY"); k != "" {
		return k, "local .env file"
	}
	// Check neighboring project in Documents as permitted by user instructions
	neighborPath := filepath.Join("/Users/deuswork/Documents/IT projects/awesomeProject1", ".env")
	if k := loadEnvKey(neighborPath, "GEMINI_API_KEY"); k != "" {
		return k, "neighboring project awesomeProject1/.env"
	}
	return "", ""
}

func main() {
	key, keySource := getGeminiKey()
	if key == "" {
		fmt.Println("[ERROR] GEMINI_API_KEY not found in environment, .env, or neighboring project .env.")
		os.Exit(1)
	}
	fmt.Printf("[INFO] Using Gemini API key from %s\n", keySource)

	dataFilePath := filepath.Join("testdata", "freshness_cases.json")
	content, err := os.ReadFile(dataFilePath)
	if err != nil {
		// Try parent path
		dataFilePath = filepath.Join("..", "testdata", "freshness_cases.json")
		content, err = os.ReadFile(dataFilePath)
		if err != nil {
			fmt.Printf("[ERROR] Failed to read freshness_cases.json: %v\n", err)
			os.Exit(1)
		}
	}

	var cases []FreshnessCase
	if err := json.Unmarshal(content, &cases); err != nil {
		fmt.Printf("[ERROR] Failed to parse freshness_cases.json: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	provider, err := ai.NewGeminiProvider(ctx, key, "gemini-flash-latest")
	if err != nil {
		fmt.Printf("[ERROR] Failed to initialize Gemini provider: %v\n", err)
		os.Exit(1)
	}

	currentTime := time.Date(2026, 10, 5, 8, 30, 0, 0, time.UTC)
	fmt.Printf("[INFO] Running %d freshness test cases (current simulated date: %s)...\n\n", len(cases), currentTime.Format("2006-01-02"))

	allPassed := true

	for i, tc := range cases {
		pubDate, err := time.Parse(time.RFC3339, tc.PublishedAt)
		if err != nil {
			fmt.Printf("[FAIL] Case %d (%s): invalid published_at date %s: %v\n", i+1, tc.ID, tc.PublishedAt, err)
			allPassed = false
			continue
		}

		prompt := ai.BuildPrompt(ai.NewsInput{
			Title:       tc.Title,
			Body:        tc.Body,
			Source:      tc.Source,
			PublishedAt: &pubDate,
			CurrentTime: currentTime,
			FactsPath:   "facts.md",
		})

		resp, err := provider.Complete(ctx, prompt)
		if err != nil {
			fmt.Printf("[FAIL] Case %d (%s): API error: %v\n", i+1, tc.ID, err)
			allPassed = false
			continue
		}

		post, parseErr := ai.ParseJSONPost(resp)
		if parseErr != nil {
			fmt.Printf("[FAIL] Case %d (%s): JSON parse error: %v\nRaw response:\n%s\n", i+1, tc.ID, parseErr, resp)
			allPassed = false
			continue
		}

		matched := post.Skip == tc.ExpectedSkip
		status := "PASS"
		if !matched {
			status = "FAIL"
			allPassed = false
		}

		fmt.Printf("[%s] Case %d: %s\n", status, i+1, tc.Name)
		fmt.Printf("       Title:       %s\n", tc.Title)
		fmt.Printf("       Published:   %s\n", tc.PublishedAt)
		fmt.Printf("       Expected:    skip=%v | Got: skip=%v\n", tc.ExpectedSkip, post.Skip)
		if post.Skip {
			fmt.Printf("       Skip Reason: %s\n", post.Reason)
		} else {
			fmt.Printf("       ArticleType: %s\n", post.Type)
		}
		fmt.Println("--------------------------------------------------------------------------------")

		if i < len(cases)-1 {
			time.Sleep(2 * time.Second)
		}
	}

	if allPassed {
		fmt.Println("\n[SUCCESS] All freshness cases passed expectations!")
	} else {
		fmt.Println("\n[FAILURE] One or more freshness cases did not match expectations.")
		os.Exit(1)
	}
}
