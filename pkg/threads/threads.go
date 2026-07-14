package threads

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type ContainerResponse struct {
	ID string `json:"id"`
}

type PublishResponse struct {
	ID string `json:"id"`
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// PostThread publishes a text thread to the Meta Threads API.
func PostThread(ctx context.Context, text string) (string, error) {
	accessToken := strings.TrimSpace(os.Getenv("THREADS_ACCESS_TOKEN"))
	if accessToken == "" {
		return "", fmt.Errorf("missing THREADS_ACCESS_TOKEN in environment")
	}

	// Step 1: Create a container for the text post
	// Using /me/threads is default for the token owner
	apiURL := "https://graph.threads.net/v1.0/me/threads"
	
	q := url.Values{}
	q.Set("media_type", "TEXT")
	q.Set("text", text)
	q.Set("access_token", accessToken)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL+"?"+q.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("create container request build: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("create container execute: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		var errData map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errData)
		return "", fmt.Errorf("create container returned status %d: %v", resp.StatusCode, errData)
	}

	var cr ContainerResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", fmt.Errorf("decode container response: %w", err)
	}

	containerID := cr.ID
	if containerID == "" {
		return "", fmt.Errorf("empty container ID returned by Threads API")
	}

	// Step 2: Publish the container
	publishURL := "https://graph.threads.net/v1.0/me/threads_publish"
	pq := url.Values{}
	pq.Set("creation_id", containerID)
	pq.Set("access_token", accessToken)

	publishReq, err := http.NewRequestWithContext(ctx, "POST", publishURL+"?"+pq.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("publish request build: %w", err)
	}

	publishResp, err := httpClient.Do(publishReq)
	if err != nil {
		return "", fmt.Errorf("publish execute: %w", err)
	}
	defer func() { _ = publishResp.Body.Close() }()

	if publishResp.StatusCode != http.StatusOK {
		var errData map[string]interface{}
		_ = json.NewDecoder(publishResp.Body).Decode(&errData)
		return "", fmt.Errorf("publish returned status %d: %v", publishResp.StatusCode, errData)
	}

	var pr PublishResponse
	if err := json.NewDecoder(publishResp.Body).Decode(&pr); err != nil {
		return "", fmt.Errorf("decode publish response: %w", err)
	}

	return pr.ID, nil
}

// FormatThread formats the article title and Telegram channel message link into a Threads post.
// Respects the 500-character limit of Threads and limits hashtags to 1 as per recommended guidelines.
func FormatThread(title, tgChannelUsername string, tgMessageID int) string {
	var tgLink string
	if tgChannelUsername != "" && tgMessageID > 0 {
		username := strings.TrimPrefix(tgChannelUsername, "@")
		tgLink = fmt.Sprintf("https://t.me/%s/%d", username, tgMessageID)
	}

	// Safe limit for Threads is 500 characters.
	// tgLink takes ~30 chars, static text "🎮 \n\nЧитати далі: \n\n#Nintendo" takes ~40 chars.
	// Safe title budget = 500 - 70 = 430 characters.
	titleRunes := []rune(title)
	if len(titleRunes) > 420 {
		title = string(titleRunes[:417]) + "..."
	}

	if tgLink != "" {
		return fmt.Sprintf("🎮 %s\n\nЧитати далі: %s\n\n#Nintendo", title, tgLink)
	}
	
	return fmt.Sprintf("🎮 %s\n\n#Nintendo", title)
}

// MaybeCrossPost posts a thread for the given article if Threads credentials are set.
func MaybeCrossPost(ctx context.Context, title string, messageID int) {
	accessToken := strings.TrimSpace(os.Getenv("THREADS_ACCESS_TOKEN"))
	if accessToken == "" {
		slog.Debug("threads: access token is empty, skipping cross-post")
		return
	}

	tgUsername := os.Getenv("TELEGRAM_CHANNEL_USERNAME")
	if tgUsername == "" {
		tgUsername = "deusflow"
	}

	threadText := FormatThread(title, tgUsername, messageID)
	slog.Info("threads: preparing to cross-post to Threads", "text", threadText)

	postID, err := PostThread(ctx, threadText)
	if err != nil {
		slog.Error("threads: cross-post failed", "error", err)
		return
	}

	slog.Info("threads: successfully cross-posted to Threads", "post_id", postID)
}
