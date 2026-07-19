package highlight

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/deuswork/nintendoflow/pkg/ai"
	"github.com/deuswork/nintendoflow/pkg/config"
	"github.com/deuswork/nintendoflow/pkg/db"
	"github.com/deuswork/nintendoflow/pkg/dedup"
	"github.com/deuswork/nintendoflow/pkg/telegram"
)

func Run(ctx context.Context, database *sql.DB, manager *ai.Manager, cfg *config.Config) {
	slog.Info("starting in highlight mode")

	// 1. Fetch already posted games
	previousGames, err := db.FetchRecentlyHighlightedGames(ctx, database)
	if err != nil {
		slog.Error("failed to fetch previously highlighted games", "error", err)
		previousGames = []string{}
	}

	// 2. Build prompt for Gemini
	exclusionList := strings.Join(previousGames, ", ")
	if exclusionList == "" {
		exclusionList = "None"
	}

	prompt := fmt.Sprintf(`Choose a legendary, critically acclaimed Nintendo game with Metacritic 85+ (from NES, SNES, N64, GameCube, Wii, DS, 3DS, Wii U, or Switch eras) that is NOT in this exclusion list: [%s].

Write a highly engaging, atmospheric, and emotional storytelling post in Ukrainian about this game, acting as a passionate gaming historian.

Structure of the Telegram post (telegram_html):
1. **Title**: Start the narrative with a catchy title enclosed in <b> tags (e.g. <b>Super Mario 64: Безчасна Класика (N64, 1996, 94)</b>).
2. **Body**: Write a compelling narrative focusing on the game's innovation, development magic, and legacy.
3. **CRITICAL LIMIT**: The text MUST be exactly 2 or 3 paragraphs maximum (excluding the title). Do not write 4 or more paragraphs.

Structure of the Threads post (threads_text):
- Write a condensed, informative mini-article summarizing the game's development, innovation, and legacy.
- Tone: conversational but educational, using a friendly blogger style.
- Use introductory phrases like "Трошки з світу Нінтендо:" and transitions like "Такі от справи :) Що особливого?".
- Length: STRICTLY between 450 and 498 characters. DO NOT exceed 500 characters.
- No HTML or Markdown.
- EXACT STYLE REFERENCE (Follow this rhythm and structure exactly):
"Трошки з світу Нінтендо: цю легендарну гру \"The Legend of Zelda: A Link to the Past\" розробили під керівництвом Міямото, це був революційний жанр action-adventure та встановила новий стандарт для майбутніх ігор серії. Такі от справи :) Що особливого? Її інноваційна система перемикання між світами, складні лабіринти та епічна історія. Сьогодні Zelda залишається однією з найкращих ігор усіх часів, Її вплив на індустрію незаперечний, а її спадщина продовжує жити у серцях шанувальників."

CRITICAL RULES for quality and accuracy:
- **Zero Hallucinations**: You must only use 100%% verified historical facts. Never guess or invent details.
- **Strict Name Check**: Verify spelling of Japanese creators. Example: Shigeru Miyamoto is Шігеру Міямото.
- **Formatting (Telegram)**: DO NOT USE Markdown. MUST use HTML tags: <b>text</b> and <i>text</i>.
- **Output Format**: You MUST return ONLY a valid JSON object. Do not include markdown code blocks.

Example JSON output:
{
  "game_name": "Super Mario 64",
  "telegram_html": "<b>Super Mario 64: Безчасна Класика</b>\n\nПерший абзац тексту про гру та інновації...\n\nДругий і останній абзац про спадщину та вплив...",
  "threads_text": "Трошки з світу Нінтендо: цю легендарну гру \"Super Mario 64\" розробили під керівництвом Міямото..."
}
`, exclusionList)

	var post struct {
		GameName     string `json:"game_name"`
		TelegramHTML string `json:"telegram_html"`
		ThreadsText  string `json:"threads_text"`
	}

	var success bool
	for attempt := 1; attempt <= 3; attempt++ {
		rewritten, err := manager.Generate(ctx, prompt)
		if err != nil {
			slog.Error("AI highlight generation failed", "error", err, "attempt", attempt)
			continue
		}

		// 3. Parse AI response
		start := strings.Index(rewritten, "{")
		end := strings.LastIndex(rewritten, "}")
		if start == -1 || end == -1 || end < start {
			slog.Error("failed to find JSON in AI response", "raw", rewritten, "attempt", attempt)
			continue
		}
		
		jsonStr := rewritten[start : end+1]
		if err := json.Unmarshal([]byte(jsonStr), &post); err != nil {
			slog.Error("failed to parse AI JSON response", "error", err, "raw", jsonStr, "attempt", attempt)
			continue
		}

		// Verify it's not a duplicate
		cleanNew := strings.ToLower(strings.TrimSpace(post.GameName))
		isDuplicate := false
		for _, g := range previousGames {
			cleanPrev := strings.ToLower(strings.TrimSpace(g))
			if cleanPrev != "" && cleanNew != "" && (strings.Contains(cleanPrev, cleanNew) || strings.Contains(cleanNew, cleanPrev)) {
				isDuplicate = true
				break
			}
		}

		if !isDuplicate {
			success = true
			break
		}
		
		slog.Warn("AI generated a duplicate game, retrying...", "game", post.GameName, "attempt", attempt)
	}

	if !success {
		slog.Error("failed to generate a unique highlight game after 3 attempts")
		return
	}

	gameTitle := post.GameName
	if gameTitle == "" {
		gameTitle = "Legendary Nintendo Game"
	}
	cleanBody := strings.TrimSpace(post.TelegramHTML)
	threadsBody := strings.TrimSpace(post.ThreadsText)

	// 4. Create and insert article
	now := time.Now()
	article := db.Article{
		SourceURL:   "https://www.nintendo.com/masterpiece/" + urlSafeName(gameTitle),
		URLHash:     dedup.HashURL("https://www.nintendo.com/masterpiece/" + urlSafeName(gameTitle)),
		TitleHash:   dedup.HashTitle(gameTitle + " Highlight"),
		ContentHash: dedup.HashTitle(cleanBody),
		TitleRaw:    gameTitle,
		BodyUA:      cleanBody,
		BodyThreads: threadsBody,
		SourceName:  "Nintendo Masterpiece",
		SourceType:  "highlight",
		ArticleType: "insight",
		Score:       99,
		Status:      db.StatusPending,
		PublishedAt: &now,
	}

	articleID, err := db.InsertArticle(ctx, database, article)
	if err != nil {
		slog.Error("insert highlight article failed", "error", err)
		return
	}
	article.ID = articleID

	// Save the translated/generated body to the database so that it is not empty when approved.
	if err := db.UpdateBodies(ctx, database, articleID, cleanBody, threadsBody, manager.LastProvider()); err != nil {
		slog.Error("update highlight bodies failed", "error", err)
		return
	}

	// 5. Send moderation preview
	testBot, err := tgbotapi.NewBotAPI(cfg.TestTelegramToken)
	if err != nil {
		slog.Error("telegram bot init failed", "error", err)
		return
	}

	previewChatID := cfg.TestAdminChatID
	if strings.TrimSpace(previewChatID) == "" {
		previewChatID = cfg.TestChannelID
	}

	previewMessageID, err := telegram.SendModerationPreview(testBot, previewChatID, article)
	if err != nil {
		slog.Error("send highlight moderation preview failed", "error", err, "article_id", article.ID)
		return
	}

	slog.Info("highlight moderation preview sent", "article_id", article.ID, "preview_message_id", previewMessageID)
}

func urlSafeName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
