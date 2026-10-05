package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
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
	ExpectedType string `json:"expected_type"`
}

func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.Trim(val, `"'`)
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

func main() {
	var envFilePath string
	var aiConfigPath string
	flag.StringVar(&envFilePath, "env-file", "", "Path to .env file to load environment variables from")
	flag.StringVar(&aiConfigPath, "config", "ai_config.json", "Path to ai_config.json")
	flag.Parse()

	if envFilePath != "" {
		if err := loadEnvFile(envFilePath); err != nil {
			fmt.Printf("[ERROR] Failed to read specified --env-file %q: %v\n", envFilePath, err)
			os.Exit(1)
		}
		fmt.Printf("[INFO] Loaded environment variables from %s\n", envFilePath)
	} else {
		// Try local .env if present
		if _, err := os.Stat(".env"); err == nil {
			_ = loadEnvFile(".env")
		}
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		fmt.Println("[ERROR] GEMINI_API_KEY is not set. Please set the GEMINI_API_KEY environment variable or pass --env-file=<path_to_env>.")
		os.Exit(1)
	}

	ctx := context.Background()

	providers, err := ai.BuildProvidersFromConfig(ctx, aiConfigPath)
	if err != nil {
		fmt.Printf("[ERROR] AI router initialization failed from config %q: %v\n", aiConfigPath, err)
		os.Exit(1)
	}

	providerNames := make([]string, 0, len(providers))
	for _, p := range providers {
		providerNames = append(providerNames, p.Name())
	}
	fmt.Printf("[INFO] AI manager initialized using config %q with provider chain: [%s]\n", aiConfigPath, strings.Join(providerNames, " -> "))

	manager := ai.NewManager(providers, 30, 2*time.Second)

	dataFilePath := filepath.Join("testdata", "freshness_cases.json")
	content, err := os.ReadFile(dataFilePath)
	if err != nil {
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

	currentTime := time.Date(2026, 10, 5, 8, 30, 0, 0, time.UTC)
	fmt.Printf("[INFO] Running %d freshness test cases (simulated current date: %s)...\n\n", len(cases), currentTime.Format("2006-01-02"))

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

		resp, err := manager.Generate(ctx, prompt)
		if err != nil {
			fmt.Printf("[FAIL] Case %d (%s): AI generation error: %v\n", i+1, tc.ID, err)
			allPassed = false
			continue
		}

		post, parseErr := ai.ParseJSONPost(resp)
		if parseErr != nil {
			fmt.Printf("[FAIL] Case %d (%s): JSON parse error: %v\nRaw response:\n%s\n", i+1, tc.ID, parseErr, resp)
			allPassed = false
			continue
		}

		matchedSkip := post.Skip == tc.ExpectedSkip
		status := "PASS"
		if !matchedSkip {
			status = "FAIL"
			allPassed = false
		}

		fmt.Printf("[%s] Case %d: %s\n", status, i+1, tc.Name)
		fmt.Printf("       Title:       %s\n", tc.Title)
		fmt.Printf("       Published:   %s\n", tc.PublishedAt)
		fmt.Printf("       Expected:    skip=%v", tc.ExpectedSkip)
		if tc.ExpectedType != "" {
			fmt.Printf(", expected_type=%s", tc.ExpectedType)
		}
		fmt.Printf(" | Got: skip=%v, type=%s\n", post.Skip, post.Type)

		if post.Skip {
			fmt.Printf("       Skip Reason: %s\n", post.Reason)
		} else {
			// If expected rumor, check if type or text marks it as a rumor
			isRumorMarked := post.Type == "rumor" ||
				strings.Contains(strings.ToLower(post.TelegramHTML), "чутк") ||
				strings.Contains(strings.ToLower(post.TelegramHTML), "витік") ||
				strings.Contains(strings.ToLower(post.TelegramHTML), "непідтвердж") ||
				strings.Contains(strings.ToLower(post.TelegramHTML), "інсайдер")
			if tc.ExpectedType == "rumor" && !isRumorMarked {
				fmt.Printf("       [WARN] Rumor case was published without clear rumor marker in type or text!\n")
			}
			fmt.Printf("       Telegram:    %s\n", strings.ReplaceAll(post.TelegramHTML, "\n", " "))
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
