package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/deuswork/nintendoflow/pkg/db"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var telegramHTTPClient = &http.Client{Timeout: 15 * time.Second}

// PostArticle sends an article to the Telegram channel.
// If the article has an image, sendPhoto is used; otherwise sendMessage.
func PostArticle(bot *tgbotapi.BotAPI, channelID string, article db.Article) error {
	videoURL := strings.TrimSpace(article.VideoURL)
	imageURL := strings.TrimSpace(article.ImageURL)

	// Ensure an image exists if all video uploads fail
	if imageURL == "" {
		imageURL = getFallbackImageURL(article.ArticleType)
	}

	// --- SCENARIO A: WE HAVE A VIDEO ---
	if videoURL != "" {
		// 1. Try to send as actual Native Video
		if strings.Contains(videoURL, "youtube.com") || strings.Contains(videoURL, "youtu.be") {
			// Try to download and stream to Telegram
			stream, size, err := getYouTubeStream(context.Background(), videoURL)
			if err == nil {
				// Telegram bot API limits local files to 50MB
				if size < 49*1024*1024 {
					video := tgbotapi.VideoConfig{
						BaseFile: tgbotapi.BaseFile{
							BaseChat: tgbotapi.BaseChat{ChannelUsername: channelID},
							File:     tgbotapi.FileReader{Name: "video.mp4", Reader: stream},
						},
						Caption:   buildCaption(&article, 1024, ""),
						ParseMode: "HTML",
					}
					_, errSend := bot.Send(video)
					stream.Close()
					if errSend == nil {
						return nil
					} else {
						slog.Warn("youtube video upload failed, fallback to embed", "article_id", article.ID, "error", errSend)
					}
				} else {
					stream.Close()
					slog.Info("youtube video too large, fallback to embed", "article_id", article.ID, "size", size)
				}
			} else {
				slog.Warn("youtube download failed, fallback to embed", "error", err)
			}
		} else {
			// Non-youtube video (direct remote URL mp4)
			video := tgbotapi.VideoConfig{
				BaseFile: tgbotapi.BaseFile{
					BaseChat: tgbotapi.BaseChat{ChannelUsername: channelID},
					File:     tgbotapi.FileURL(videoURL),
				},
				Caption:   buildCaption(&article, 1024, ""),
				ParseMode: "HTML",
			}
			if _, err := bot.Send(video); err == nil {
				return nil
			}
		}

		// 2. Native Video Failed -> Fallback to Embed (Second Best for Videos: beautiful YouTube player!)
		if err := sendTextWithVideoLink(bot, channelID, article); err == nil {
			return nil
		} else {
			slog.Warn("telegram video embed failed, fallback to photo", "error", err)
		}
	}

	// --- SCENARIO B: NO VIDEO, OR VIDEO EMBED FAILED. Send as Photo ---
	var extra string
	if videoURL != "" {
		extra = "🎥 Відео: " + videoURL
	}

	photo := tgbotapi.PhotoConfig{
		BaseFile: tgbotapi.BaseFile{
			BaseChat: tgbotapi.BaseChat{ChannelUsername: channelID},
			File:     tgbotapi.FileURL(imageURL),
		},
		Caption:   buildCaption(&article, 1024, extra),
		ParseMode: "HTML",
	}
	if _, err := bot.Send(photo); err == nil {
		return nil
	} else {
		slog.Warn("telegram publish send photo failed", "article_id", article.ID, "image", imageURL, "error", err)
	}

	// --- SCENARIO C: ABSOLUTE LAST RESORT ---
	return sendText(bot, channelID, article)
}

func getFallbackImageURL(articleType string) string {
	baseURL := "https://deusflow.github.io/nintendoflow/assets/placeholders"
	switch articleType {
	case "insight", "инсайт":
		return baseURL + "/news-fallback-16x9.webp"
	case "news", "новость", "факт":
		return baseURL + "/newstwo-fallback-16x9.webp"
	case "rumor", "слух", "слухи", "чутка":
		return baseURL + "/card-fallback-16x9.webp"
	case "offtop":
		return baseURL + "/offtop-fallback-16x9.webp"
	default:
		return baseURL + "/newstwo-fallback-16x9.webp"
	}
}

func sendTextWithVideoLink(bot *tgbotapi.BotAPI, channelID string, article db.Article) error {
	videoURL := strings.TrimSpace(article.VideoURL)
	extra := "🎥 Відео: " + videoURL
	text := buildCaption(&article, 3800, extra)

	type linkPreviewOptions struct {
		IsDisabled       bool   `json:"is_disabled"`
		URL              string `json:"url"`
		PreferLargeMedia bool   `json:"prefer_large_media"`
		ShowAboveText    bool   `json:"show_above_text"`
	}
	type payload struct {
		ChatID             string             `json:"chat_id"`
		Text               string             `json:"text"`
		ParseMode          string             `json:"parse_mode"`
		LinkPreviewOptions linkPreviewOptions `json:"link_preview_options"`
	}

	body, err := json.Marshal(payload{
		ChatID:    channelID,
		Text:      text,
		ParseMode: "HTML",
		LinkPreviewOptions: linkPreviewOptions{
			IsDisabled:       false,
			URL:              videoURL,
			PreferLargeMedia: true,
			ShowAboveText:    true,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", bot.Token)
	resp, err := telegramHTTPClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram sendMessage with video link: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram api error %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func sendText(bot *tgbotapi.BotAPI, channelID string, article db.Article) error {
	msg := tgbotapi.NewMessageToChannel(channelID, buildCaption(&article, 4096, ""))
	msg.ParseMode = "HTML"
	if _, err := bot.Send(msg); err != nil {
		return fmt.Errorf("telegram sendMessage: %w", err)
	}
	return nil
}

// buildCaption forms the final post text based on source and article type.
func buildCaption(article *db.Article, maxLen int, appendExtra string) string {
	var prefix string
	switch article.SourceType {
	case "official":
		prefix = "📢 <b>ОФІЦІЙНО</b>\n\n"
	case "insider":
		prefix = "🕵️ <i>Інсайд</i>\n\n"
	default:
		// For aggregators and other sources, use the AI-determined article type.
		switch article.ArticleType {
		case "rumor":
			prefix = "🤫 <i>Чутки</i>\n\n"
		case "insight":
			prefix = "🔍 <i>Інсайт</i>\n\n"
		}
	}

	body := stripSourceFooter(article.BodyUA)

	// Create clickable source link
	sourceLink := ""
	if strings.TrimSpace(article.SourceURL) != "" {
		sourceName := article.SourceName
		if sourceName == "" {
			sourceName = "Джерело"
		}
		sourceLink = fmt.Sprintf("\n\n🔗 Джерело: <a href=\"%s\"><b>%s</b></a>", escapeHTML(article.SourceURL), escapeHTML(sourceName))
	}

	full := prefix + body

	// Add a little extra spacing before extra material if any
	if appendExtra != "" {
		appendExtra = "\n\n" + appendExtra
	}

	// Leave space for the source link and ellipsis
	reserve := len([]rune(sourceLink)) + len([]rune(appendExtra)) + 3

	runes := []rune(full)
	var finalBody string
	if len(runes) <= maxLen-reserve {
		finalBody = full
	} else {
		bodyRunes := []rune(body)
		allowed := maxLen - reserve - len([]rune(prefix))
		if allowed > 0 && allowed < len(bodyRunes) {
			finalBody = prefix + string(bodyRunes[:allowed]) + "..."
		} else {
			finalBody = string(runes[:maxLen-reserve]) + "..."
		}
	}

	return finalBody + appendExtra + sourceLink
}

// stripSourceFooter removes stray inline-link lines (🔗) left from older
// bot versions. "Джерело:" lines are now generated by the AI and are kept.
func stripSourceFooter(body string) string {
	lines := strings.Split(body, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmed, "🔗") {
			continue
		}
		if lower == "preview" || strings.HasPrefix(lower, "title:") || strings.HasPrefix(lower, "source:") || strings.HasPrefix(lower, "score:") {
			continue
		}
		if strings.HasPrefix(lower, "<b>title:</b>") || strings.HasPrefix(lower, "<b>source:</b>") || strings.HasPrefix(lower, "<b>score:</b>") {
			continue
		}
		if strings.Contains(lower, "original link") {
			continue
		}
		kept = append(kept, line)
	}
	clean := strings.TrimSpace(strings.Join(kept, "\n"))
	clean = strings.TrimPrefix(clean, "<b>Preview</b>")
	return strings.TrimSpace(clean)
}
