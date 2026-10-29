package highlight

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"
	"unicode"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/deuswork/nintendoflow/pkg/ai"
	"github.com/deuswork/nintendoflow/pkg/config"
	"github.com/deuswork/nintendoflow/pkg/db"
	"github.com/deuswork/nintendoflow/pkg/dedup"
	"github.com/deuswork/nintendoflow/pkg/telegram"
)

var curatedMasterpieces = []string{
	"Super Mario Bros. 3 (NES, 1988)",
	"The Legend of Zelda: A Link to the Past (SNES, 1991)",
	"Super Metroid (SNES, 1994)",
	"Chrono Trigger (SNES, 1995)",
	"EarthBound (SNES, 1994)",
	"Super Mario World 2: Yoshi's Island (SNES, 1995)",
	"Donkey Kong Country 2: Diddy's Kong Quest (SNES, 1995)",
	"Super Mario RPG: Legend of the Seven Stars (SNES, 1996)",
	"Final Fantasy VI (SNES, 1994)",
	"The Legend of Zelda: Link's Awakening (Game Boy, 1993)",
	"Super Mario 64 (N64, 1996)",
	"The Legend of Zelda: Ocarina of Time (N64, 1998)",
	"The Legend of Zelda: Majora's Mask (N64, 2000)",
	"GoldenEye 007 (N64, 1997)",
	"Banjo-Kazooie (N64, 1998)",
	"Paper Mario (N64, 2000)",
	"Star Fox 64 (N64, 1997)",
	"Perfect Dark (N64, 2000)",
	"Conker's Bad Fur Day (N64, 2001)",
	"F-Zero X (N64, 1998)",
	"Super Smash Bros. Melee (GameCube, 2001)",
	"Metroid Prime (GameCube, 2002)",
	"The Legend of Zelda: The Wind Waker (GameCube, 2002)",
	"Paper Mario: The Thousand-Year Door (GameCube, 2004)",
	"Eternal Darkness: Sanity's Requiem (GameCube, 2002)",
	"Resident Evil 4 (GameCube, 2005)",
	"Pikmin 2 (GameCube, 2004)",
	"F-Zero GX (GameCube, 2003)",
	"Fire Emblem: Path of Radiance (GameCube, 2005)",
	"Animal Crossing (GameCube, 2001)",
	"Luigi's Mansion (GameCube, 2001)",
	"Super Mario Sunshine (GameCube, 2002)",
	"The Legend of Zelda: Twilight Princess (GameCube/Wii, 2006)",
	"Metroid Fusion (GBA, 2002)",
	"Metroid: Zero Mission (GBA, 2004)",
	"Advance Wars (GBA, 2001)",
	"Mario & Luigi: Superstar Saga (GBA, 2003)",
	"Mother 3 (GBA, 2006)",
	"Castlevania: Aria of Sorrow (GBA, 2003)",
	"Golden Sun (GBA, 2001)",
	"The Legend of Zelda: The Minish Cap (GBA, 2004)",
	"WarioWare, Inc.: Mega Microgame$! (GBA, 2003)",
	"Mario Kart DS (DS, 2005)",
	"The World Ends with You (DS, 2007)",
	"Mario & Luigi: Bowser's Inside Story (DS, 2009)",
	"Chrono Trigger DS (DS, 2008)",
	"Ghost Trick: Phantom Detective (DS, 2010)",
	"999: Nine Hours, Nine Persons, Nine Doors (DS, 2009)",
	"Pokemon HeartGold & SoulSilver (DS, 2009)",
	"Pokemon Black & White (DS, 2010)",
	"Professor Layton and the Unwound Future (DS, 2008)",
	"Radiant Historia (DS, 2010)",
	"Super Mario Galaxy (Wii, 2007)",
	"Super Mario Galaxy 2 (Wii, 2010)",
	"Xenoblade Chronicles (Wii, 2010)",
	"Donkey Kong Country Returns (Wii, 2010)",
	"Metroid Prime 3: Corruption (Wii, 2007)",
	"Sin & Punishment: Star Successor (Wii, 2009)",
	"Zack & Wiki: Quest for Barbaros' Treasure (Wii, 2007)",
	"Punch-Out!! (Wii, 2009)",
	"Fire Emblem: Awakening (3DS, 2012)",
	"The Legend of Zelda: A Link Between Worlds (3DS, 2013)",
	"Kid Icarus: Uprising (3DS, 2012)",
	"Animal Crossing: New Leaf (3DS, 2012)",
	"Super Mario 3D Land (3DS, 2011)",
	"Shin Megami Tensei IV (3DS, 2013)",
	"Bravely Default (3DS, 2012)",
	"Metroid: Samus Returns (3DS, 2017)",
	"Donkey Kong Country: Tropical Freeze (Wii U, 2014)",
	"Super Mario 3D World (Wii U, 2013)",
	"Bayonetta 2 (Wii U, 2014)",
	"Pikmin 3 (Wii U, 2013)",
	"Splatoon (Wii U, 2015)",
	"The Wonderful 101 (Wii U, 2013)",
	"The Legend of Zelda: Breath of the Wild (Switch, 2017)",
	"Super Mario Odyssey (Switch, 2017)",
	"The Legend of Zelda: Tears of the Kingdom (Switch, 2023)",
	"Metroid Dread (Switch, 2021)",
	"Fire Emblem: Three Houses (Switch, 2019)",
	"Xenoblade Chronicles 3 (Switch, 2022)",
	"Super Smash Bros. Ultimate (Switch, 2018)",
	"Animal Crossing: New Horizons (Switch, 2020)",
	"Luigi's Mansion 3 (Switch, 2019)",
	"Astral Chain (Switch, 2019)",
	"Pikmin 4 (Switch, 2023)",
	"Super Mario Bros. Wonder (Switch, 2023)",
}

func Run(ctx context.Context, database *sql.DB, manager *ai.Manager, cfg *config.Config) {
	slog.Info("starting in highlight mode")

	// 1. Fetch already posted games (all-time history preserved in DB)
	previousGames, err := db.FetchRecentlyHighlightedGames(ctx, database)
	if err != nil {
		slog.Error("failed to fetch previously highlighted games", "error", err)
		previousGames = []string{}
	}

	var post struct {
		GameName     string `json:"game_name"`
		TelegramHTML string `json:"telegram_html"`
		ThreadsText  string `json:"threads_text"`
	}

	now := time.Now()
	worldFacts := ai.LoadWorldFacts("", now)

	var success bool
	for attempt := 1; attempt <= 3; attempt++ {
		candidates := pickUnusedCandidates(previousGames, 5)
		exclusionList := strings.Join(previousGames, ", ")
		if exclusionList == "" {
			exclusionList = "None"
		}
		candidateHint := ""
		if len(candidates) > 0 {
			candidateHint = fmt.Sprintf("\nОБОВ'ЯЗКОВО обери ОДНУ гру з цієї випадкової підбірки свіжих шедеврів (які ще не публікувалися на каналі):\n- %s\n", strings.Join(candidates, "\n- "))
		}

		prompt := fmt.Sprintf(`Ти — харизматичний український геймблогер та історик ігрової індустрії каналу DEUSFLOW (сьогодні %s).
%s
Обери легендарну гру Nintendo з оцінкою Metacritic 85+, якої КАТЕГОРИЧНО НЕМАЄ в цьому списку вже опублікованих ігор: [%s].
%s
Напиши атмосферний, живий, трохи іронічний та емоційний пост українською мовою про цю гру — так, ніби розповідаєш друзям-геймерам, чому ця гра досі виносить мозок і чому сучасним ААА-студіям варто в неї повчитися.

Структура Telegram-поста (telegram_html):
1. **Заголовок**: Почни з яскравого заголовка в тегах <b> (наприклад: <b>🎮 Super Mario 64: як Міямото навчив індустрію стрибати у 3D (N64, 1996)</b>).
2. **Тіло**: 2 або максимум 3 абзаци (без заголовка). Фокус на геймплейній магії, курйозах або геніальних рішеннях розробки та спадщині гри станом на 2026 рік.
3. Допускається використання <b>, <i>, <tg-spoiler> (для прихованих сюжетних твістів чи пасхалок) або <blockquote expandable> (для цікавої цитати розробника).

Структура Threads-поста (threads_text):
- Коротка інформативна міні-історія у дружньому блогерському тоні.
- Почни з "Трошки з світу Нінтендо:" і використай живий перехід на кшталт "Такі от справи :) Що особливого?".
- Довжина: СУВОРО від 400 до 495 символів. Не перевищуй 500 символів. Без HTML і Markdown.

КРИТИЧНІ ПРАВИЛА:
- **Zero Hallucinations**: Тільки 100%% реальні історичні факти.
- **Strict Name Check**: Перевіряй українську транскрипцію японських творців (Шігеру Міямото, Сатору Івата, Ейдзі Аонума, Йошіо Сакамото, Масахіро Сакурай).
- **game_name**: Поле "game_name" має містити ТІЛЬКИ канонічну назву гри без року та платформи (наприклад, "Metroid Prime").
- **Output Format**: Поверни ТІЛЬКИ валідний JSON без markdown-обгорток.

Example JSON output:
{
  "game_name": "Metroid Prime",
  "telegram_html": "<b>🎮 Metroid Prime: коли техасці зробили найкращу гру Nintendo (GameCube, 2002)</b>\n\nПерший абзац про скепсис фанів і магію Retro Studios...\n\nДругий абзац про спадщину та чому вона досі виглядає крутіше за половину шутерів...",
  "threads_text": "Трошки з світу Нінтендо: коли Nintendo доручила Metroid маловідомій студії з Техасу..."
}
`, now.Format("2006-01-02"), worldFacts, exclusionList, candidateHint)

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

		if !isDuplicateHighlightGame(post.GameName, previousGames) {
			success = true
			break
		}

		slog.Warn("AI generated a duplicate game, retrying...", "game", post.GameName, "attempt", attempt)
		previousGames = append(previousGames, post.GameName)
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
	now = time.Now()
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
	botToken := cfg.TestTelegramToken
	if botToken == "" {
		botToken = cfg.TelegramBotToken
	}
	testBot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		slog.Error("telegram bot init failed", "error", err)
		return
	}

	previewChatID := cfg.TestAdminChatID
	if strings.TrimSpace(previewChatID) == "" {
		previewChatID = cfg.TestChannelID
	}
	if strings.TrimSpace(previewChatID) == "" {
		previewChatID = cfg.TelegramChannelID
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

func pickUnusedCandidates(previousGames []string, count int) []string {
	var unused []string
	for _, candidate := range curatedMasterpieces {
		if !isDuplicateHighlightGame(candidate, previousGames) {
			unused = append(unused, candidate)
		}
	}
	if len(unused) == 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	rng.Shuffle(len(unused), func(i, j int) {
		unused[i], unused[j] = unused[j], unused[i]
	})
	if len(unused) > count {
		unused = unused[:count]
	}
	return unused
}

func isDuplicateHighlightGame(candidate string, previousGames []string) bool {
	candFull, candSub := normalizeHighlightParts(candidate)
	if candFull == "" {
		return false
	}
	for _, prev := range previousGames {
		prevFull, prevSub := normalizeHighlightParts(prev)
		if prevFull == "" {
			continue
		}
		if candFull == prevFull {
			return true
		}
		if candSub != "" && (candSub == prevFull || candSub == prevSub) {
			return true
		}
		if prevSub != "" && prevSub == candFull {
			return true
		}
	}
	return false
}

func normalizeHighlightParts(name string) (full string, subtitle string) {
	if idx := strings.Index(name, "("); idx != -1 {
		name = name[:idx]
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if colonIdx := strings.Index(name, ":"); colonIdx != -1 {
		sub := normalizeHighlightGameName(name[colonIdx+1:])
		if len(sub) >= 6 {
			subtitle = sub
		}
	}
	return normalizeHighlightGameName(name), subtitle
}

func normalizeHighlightGameName(name string) string {
	// Strip parenthetical platform/year suffixes such as "(SNES, 1994)"
	if idx := strings.Index(name, "("); idx != -1 {
		name = name[:idx]
	}
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "the legend of ")
	name = strings.TrimPrefix(name, "the ")
	var sb strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
