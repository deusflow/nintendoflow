# 🎮 Nintendo News Telegram Bot — AI Agent Implementation Plan
> **Актуально: березень 2026**

**Stack**: Go 1.26 · PostgreSQL (Neon) · Gemini 2.5 Flash + OpenRouter fallback · Telegram Bot API · GitHub Actions  
**Scope**: Telegram-only (Phase 1).  
**Rule**: Якщо нема якісної новини — нічого не постимо. Краще тиша ніж сміття.

---

## ⚠️ Версії та актуальність (березень 2026)

| Що | Значення |
|---|---|
| Go | **1.26** |
| Gemini модель (primary) | **gemini-2.5-flash** |
| Gemini SDK | `google.golang.org/genai` |
| Fallback AI | **OpenRouter** (`:free` моделі) |
| Fallback модель | `google/gemini-2.0-flash-exp:free` або `meta-llama/llama-3.3-70b-instruct:free` |
| GitHub Actions checkout | `actions/checkout@v6` |
| GitHub Actions setup-go | `actions/setup-go@v6` |

> `gemini-1.5-flash` — RETIRED. `gemini-2.0-flash` — DEPRECATED (шатдаун червень 2026). Не використовувати.

---

## 📁 Project Structure

```
nintendo-news-bot/
├── cmd/
│   └── bot/
│       └── main.go
├── internal/
│   ├── config/
│   │   └── config.go
│   ├── db/
│   │   ├── postgres.go
│   │   └── queries.go
│   ├── fetcher/
│   │   ├── rss.go
│   │   └── sources.go
│   ├── scorer/
│   │   └── scorer.go
│   ├── dedup/
│   │   └── dedup.go
│   ├── ai/
│   │   ├── prompt.go         # ← ЄДИНЕ місце де живе промпт і стиль
│   │   ├── provider.go       # інтерфейс AIProvider + помилки
│   │   ├── gemini.go         # primary: Gemini 2.5 Flash
│   │   ├── openrouter.go     # fallback: OpenRouter free models
│   │   └── chain.go          # fallback chain логіка + sanitizeOutput
│   ├── telegram/
│   │   └── poster.go
│   └── cleaner/
│       └── cleaner.go
├── migrations/
│   └── 001_init.sql
├── .github/
│   └── workflows/
│       └── cron.yml
├── .go-version               # 1.26
├── .env.example
├── go.mod
└── README.md
```

---

## 🗄️ Phase 1 — Database Schema

**File**: `migrations/001_init.sql`

```sql
CREATE TABLE IF NOT EXISTS articles (
    id           SERIAL PRIMARY KEY,
    source_url   TEXT UNIQUE NOT NULL,
    content_hash TEXT NOT NULL,
    title_raw    TEXT NOT NULL,
    title_ua     TEXT,
    body_ua      TEXT,
    image_url    TEXT,
    source_name  TEXT NOT NULL,
    source_type  TEXT NOT NULL DEFAULT 'media',
    score        INT DEFAULT 0,
    posted_tg    BOOLEAN DEFAULT FALSE,
    ai_provider  TEXT,                           -- який AI реально обробив новину
    published_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_articles_posted_tg    ON articles(posted_tg);
CREATE INDEX IF NOT EXISTS idx_articles_created_at   ON articles(created_at);
CREATE INDEX IF NOT EXISTS idx_articles_content_hash ON articles(content_hash);
CREATE INDEX IF NOT EXISTS idx_articles_score        ON articles(score DESC);
```

Cleanup в кінці кожного cron-запуску:
```sql
DELETE FROM articles WHERE created_at < NOW() - INTERVAL '30 days';
```

---

## 📡 Phase 2 — RSS Sources

**File**: `internal/fetcher/sources.go`

```go
type Source struct {
    Name                 string
    FeedURL              string
    Type                 string // official / media / insider / aggregator
    Priority             int
    NeedsRedirectResolve bool   // true для Google News RSS
}

var Sources = []Source{
    // ОФІЦІЙНІ
    {Name: "Nintendo Official Blog", FeedURL: "https://www.nintendo.com/en-GB/news/", Type: "official", Priority: 10},
    // МЕДІА
    {Name: "Nintendo Life",       FeedURL: "https://www.nintendolife.com/feeds/latest",    Type: "media", Priority: 8},
    {Name: "My Nintendo News",    FeedURL: "https://mynintendonews.com/feed/",              Type: "media", Priority: 7},
    {Name: "GoNintendo",          FeedURL: "https://gonintendo.com/feed",                   Type: "media", Priority: 7},
    {Name: "Nintendo Everything", FeedURL: "https://nintendoeverything.com/feed/",          Type: "media", Priority: 7},
    {Name: "Eurogamer",           FeedURL: "https://www.eurogamer.net/?format=rss",         Type: "media", Priority: 6},
    // ІНСАЙДЕРИ
    {Name: "Video Games Chronicle", FeedURL: "https://www.videogameschronicle.com/feed/",  Type: "insider", Priority: 9},
    // GOOGLE NEWS RSS (агрегатор, резолвить редиректи!)
    {
        Name:    "Google News — Nintendo insider",
        FeedURL: "https://news.google.com/rss/search?q=Nintendo+Switch+2+insider+leak&hl=en-US&gl=US&ceid=US:en",
        Type: "aggregator", Priority: 5, NeedsRedirectResolve: true,
    },
    {
        Name:    "Google News — Nintendo Direct",
        FeedURL: "https://news.google.com/rss/search?q=Nintendo+Direct+announce+2026&hl=en-US&gl=US&ceid=US:en",
        Type: "aggregator", Priority: 6, NeedsRedirectResolve: true,
    },
    {
        Name:    "Google News — Nintendo hardware",
        FeedURL: "https://news.google.com/rss/search?q=Nintendo+hardware+new+console&hl=en-US&gl=US&ceid=US:en",
        Type: "aggregator", Priority: 5, NeedsRedirectResolve: true,
    },
}
```

RSS parser: `github.com/mmcdole/gofeed`.

HTTP headers щоб не отримати 403:
```go
req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; NintendoNewsBot/1.0)")
req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml, */*")
```

Redirect resolve для Google News:
```go
func ResolveRedirect(rawURL string) (string, error) {
    client := &http.Client{Timeout: 10 * time.Second}
    resp, err := client.Head(rawURL)
    if err != nil { return rawURL, nil }
    defer resp.Body.Close()
    return resp.Request.URL.String(), nil
}
```

---

## 🔍 Phase 3 — Deduplication

**File**: `internal/dedup/dedup.go`

**Layer 1 — URL hash**:
```go
func HashURL(url string) string {
    h := sha256.Sum256([]byte(url))
    return hex.EncodeToString(h[:])
}
```

**Layer 2 — Jaccard similarity** (останні 24 години):
```go
func IsDuplicate(title string, existing []string) bool {
    for _, e := range existing {
        if jaccardSimilarity(title, e) > 0.65 { return true }
    }
    return false
}
```

```sql
SELECT title_raw FROM articles WHERE created_at > NOW() - INTERVAL '24 hours';
```

---

## 🎯 Phase 4 — Relevance Scoring

**File**: `internal/scorer/scorer.go`

```go
const MinScoreThreshold = 4

var rules = []struct{ keywords []string; score int }{
    {[]string{"nintendo direct", "direct mini", "partner direct"},         10},
    {[]string{"switch 2", "nintendo switch 2"},                            10},
    {[]string{"announced", "reveal", "new game", "launch", "release date"}, 8},
    {[]string{"free-to-play", "f2p", "free to play", "free game"},          7},
    {[]string{"update", "patch", "dlc", "expansion"},                       5},
    {[]string{"rumor", "leak", "insider", "report"},                        4},
    {[]string{"sale", "discount", "eshop sale"},                            2},
    // негативні
    {[]string{"top 10", "best games of", "tier list"},                     -5},
    {[]string{"our review", "we reviewed"},                                -3},
    {[]string{"guide", "how to", "tips and tricks"},                       -4},
}

func ScoreArticle(title, body, sourceType string) int {
    score := 0
    text := strings.ToLower(title + " " + body)
    for _, r := range rules {
        for _, kw := range r.keywords {
            if strings.Contains(text, kw) { score += r.score; break }
        }
    }
    switch sourceType {
    case "official": score += 5
    case "insider":  score += 2
    }
    return score
}
```

---

## 🤖 Phase 5 — AI Rewrite з Fallback Chain

Це найважливіша частина. Архітектура: **інтерфейс + два провайдери + chain**.

---

### 5.0 Промпт — окремий файл для всіх провайдерів

**File**: `internal/ai/prompt.go`

> ⚠️ Це ЄДИНИЙ файл де живе промпт. Gemini, OpenRouter і будь-який майбутній
> провайдер імпортують звідси. Не дублювати промпт в інших файлах.

```go
package ai

import "fmt"

// BuildPrompt — формує повний промпт для будь-якого AI провайдера.
// Всі провайдери викликають саме цю функцію — стиль єдиний для всіх.
func BuildPrompt(title, body, source string) string {
    return fmt.Sprintf("%s\n\n---\nЗаголовок оригінал: %s\nТекст оригінал: %s\nДжерело: %s\n\nПерепиши цю новину в моєму стилі. Ліміт 850 символів.",
        styleGuide, title, body, source,
    )
}

// styleGuide — детальний опис авторського стилю.
// Змінювати тут — підхоплять автоматично всі провайдери.
const styleGuide = `Ти пишеш для україномовного Telegram-каналу про Nintendo від імені автора з дуже специфічним голосом.

== ХАРАКТЕР ГОЛОСУ ==
Саркастичний, але без злоби. Поетичний, але без пафосу. Розмовний, як друг що розповідає новину за кавою — але друг начитаний і дотепний. Іронія є завжди, але факти не спотворюються.

== ОБОВ'ЯЗКОВІ ПРИЙОМИ СТИЛЮ ==

1. ПЕРШЕ РЕЧЕННЯ — це завжди образ або метафора, не переказ факту.
   ПОГАНО:  "Nintendo анонсувала нову гру для Switch 2."
   ДОБРЕ:   "Гаманець гравця ще не знає що його чекає, але Nintendo вже готує список бажань."

2. "..." — використовується для незавершеної думки або драматичної паузи перед поворотом.
   Приклад: "Страх у серці PlayStation перед великим та могутнім Стім Машін який ще навіть ненародився..."

3. ДУЖКИ для пояснення термінів з іронією або акцентом.
   Приклад: "затягує корсет Exclusivity (ексклюзивності)"
   Приклад: "нова консоль (яку ніхто не просив, але всі хочуть)"

4. КОМПАНІЇ — живі персонажі зі страхами, бажаннями, слабкостями.
   Nintendo — примхлива але геніальна. Sony — горда але нервова. Microsoft — багатий дядько що хоче в крутий клуб.

5. "є це X чи ні, не знаю, але..." — авторська невизначеність як прийом, не як слабкість.
   Показує що автор думає, а не просто переказує.

6. ПРОТИСТАВЛЕННЯ через "значно сильніше, ніж..." або "не X, а Y".
   Створює динаміку і фокус.

7. ЕПІТЕТИ з гіперболою: "великим та могутнім", "всесильним", "безсмертним" — але з іронією.

== СТРУКТУРА КОЖНОЇ НОВИНИ ==
[Яскравий образ / метафора] ... [авторський коментар "є це чи ні — не знаю, але..."] [факт з характером] [протиставлення або висновок]
🔗 Джерело: [назва]

== ЖОРСТКІ ПРАВИЛА ==
- Довжина: СТРОГО до 850 символів включно з пробілами та емоджі
- Максимум 1-3 емоджі в усьому тексті
- Без хештегів (#)
- В кінці завжди: "🔗 Джерело: [назва сайту]"
- Мова: українська, але іноземні терміни в дужках залишати як є
- Факти не вигадувати — лише переосмислювати подачу
- Якщо новина нудна і нема за що зачепитись стилістично — відповісти лише словом: SKIP

== ЖИВИЙ ПРИКЛАД ==
Вхід (сухий факт):
"Технічний директор закритої Bluepoint Games вважає, що Sony відновила стратегію ексклюзивності через страх перед Valve та Steam Machine, більше ніж перед Xbox."

Вихід (твій стиль):
"Страх у серці PlayStation перед великим та могутнім Стім Машін який ще навіть ненародився... є цей страх чи ні — не знаю, але колишній техдиректор Bluepoint Games натякає: Sony знову затягує корсет Exclusivity (ексклюзивності), бо подих Valve лякає її значно сильніше, ніж старі сварки з Xbox.

🔗 Джерело: Video Games Chronicle"`
```

---

### 5.1 Інтерфейс провайдера

**File**: `internal/ai/provider.go`

> Тільки інтерфейс і помилки. Промпт — в `prompt.go`, не тут.

```go
package ai

import (
    "context"
    "errors"
)

var ErrSkipped = errors.New("article skipped by AI")
var ErrAllProvidersFailed = errors.New("all AI providers failed")

// AIProvider — інтерфейс який реалізують всі провайдери.
// Щоб додати нового провайдера — реалізуй цей інтерфейс і додай в chain.
type AIProvider interface {
    Name() string
    Rewrite(ctx context.Context, title, body, source string) (string, error)
}
```

---

### 5.2 Primary: Gemini 2.5 Flash

**File**: `internal/ai/gemini.go`

```go
package ai

import (
    "context"
    "fmt"
    "strings"
    "google.golang.org/genai"
)

// Free tier (березень 2026): 10 RPM · 500 RPD
// Пауза між викликами: 8 секунд

type GeminiProvider struct {
    client *genai.Client
    model  string // "gemini-2.5-flash"
}

func NewGeminiProvider(ctx context.Context, apiKey string) (*GeminiProvider, error) {
    client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
    if err != nil {
        return nil, fmt.Errorf("gemini init: %w", err)
    }
    return &GeminiProvider{client: client, model: "gemini-2.5-flash"}, nil
}

func (g *GeminiProvider) Name() string { return "gemini-2.5-flash" }

func (g *GeminiProvider) Rewrite(ctx context.Context, title, body, source string) (string, error) {
    prompt := BuildPrompt(title, body, source) // ← з prompt.go, не хардкод тут
    result, err := g.client.Models.GenerateContent(ctx, g.model, genai.Text(prompt), nil)
    if err != nil {
        return "", fmt.Errorf("gemini generate: %w", err)
    }
    return sanitizeOutput(result.Text())
}
```

---

### 5.3 Fallback: OpenRouter (безкоштовні моделі)

**File**: `internal/ai/openrouter.go`

OpenRouter API повністю сумісний з OpenAI-форматом.  
Безкоштовні ліміти: **20 RPM · 200 req/day** (без поповнення рахунку).  
Якщо поповниш на $10 — ліміт зростає до **1000 req/day**.

```go
package ai

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "strings"
)

// Моделі з суфіксом :free — повністю безкоштовні, без кредитної картки.
// "openrouter/free" — авторотер: OpenRouter сам вибирає найкращу доступну
// безкоштовну модель для запиту. Це оптимальний вибір для fallback.
// DeepSeek навмисно виключений: сервери в Китаї, дані можуть
// використовуватись для тренування без opt-out.
var openRouterFreeModels = []string{
    "openrouter/free",                        // авторотер — найпростіше і найнадійніше
    "google/gemini-2.0-flash-exp:free",       // явний fallback якщо авторотер недоступний
    "meta-llama/llama-3.3-70b-instruct:free", // резервний
}

type OpenRouterProvider struct {
    apiKey     string
    httpClient *http.Client
}

func NewOpenRouterProvider(apiKey string) *OpenRouterProvider {
    return &OpenRouterProvider{
        apiKey:     apiKey,
        httpClient: &http.Client{Timeout: 30 * time.Second},
    }
}

func (o *OpenRouterProvider) Name() string { return "openrouter-free" }

func (o *OpenRouterProvider) Rewrite(ctx context.Context, title, body, source string) (string, error) {
    // BuildPrompt викликається всередині callModel — стиль єдиний для всіх провайдерів
    var lastErr error
    for _, model := range openRouterFreeModels {
        result, err := o.callModel(ctx, model, title, body, source)
        if err != nil {
            lastErr = err
            continue // наступна модель
        }
        return sanitizeOutput(result)
    }
    return "", fmt.Errorf("all openrouter models failed: %w", lastErr)
}

func (o *OpenRouterProvider) callModel(ctx context.Context, model, title, body, source string) (string, error) {
    payload := map[string]any{
        "model": model,
        "messages": []map[string]string{
            {"role": "user", "content": BuildPrompt(title, body, source)}, // ← з prompt.go
        },
        "max_tokens": 500,
    }

    body, _ := json.Marshal(payload)
    req, err := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(body))
    if err != nil {
        return "", err
    }
    req.Header.Set("Authorization", "Bearer "+o.apiKey)
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("HTTP-Referer", "https://github.com/yourname/nintendo-news-bot")
    req.Header.Set("X-Title", "Nintendo News Bot")

    resp, err := o.httpClient.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    if resp.StatusCode == 429 {
        return "", fmt.Errorf("rate limited on model %s", model)
    }
    if resp.StatusCode != 200 {
        return "", fmt.Errorf("openrouter http %d for model %s", resp.StatusCode, model)
    }

    var result struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return "", err
    }
    if len(result.Choices) == 0 {
        return "", fmt.Errorf("empty response from %s", model)
    }
    return result.Choices[0].Message.Content, nil
}
```

---

### 5.4 Fallback Chain

**File**: `internal/ai/chain.go`

```go
package ai

import (
    "context"
    "fmt"
    "strings"
    "time"
    "log/slog"
)

type Chain struct {
    providers []AIProvider
    sleep     time.Duration // пауза між викликами (захист від rate limit)
}

func NewChain(sleep time.Duration, providers ...AIProvider) *Chain {
    return &Chain{providers: providers, sleep: sleep}
}

func (c *Chain) Rewrite(ctx context.Context, title, body, source string) (string, providerName string, err error) {
    for _, p := range c.providers {
        time.Sleep(c.sleep)

        text, err := p.Rewrite(ctx, title, body, source)
        if err != nil {
            slog.Warn("AI provider failed, trying next",
                "provider", p.Name(),
                "error", err.Error(),
            )
            continue
        }
        return text, p.Name(), nil
    }
    return "", "", ErrAllProvidersFailed
}

// sanitizeOutput — спільна утиліта для всіх провайдерів
func sanitizeOutput(text string) (string, error) {
    text = strings.TrimSpace(text)
    if text == "SKIP" {
        return "", ErrSkipped
    }
    if runes := []rune(text); len(runes) > 900 {
        text = string(runes[:870]) + "..."
    }
    return text, nil
}
```

**Використання в main.go**:
```go
chain := ai.NewChain(
    8*time.Second, // пауза між викликами
    geminiProvider,    // спробує першим
    openRouterProvider, // fallback якщо Gemini впав
)

text, usedProvider, err := chain.Rewrite(ctx, article.TitleRaw, article.BodyRaw, article.SourceName)
if errors.Is(err, ai.ErrSkipped) {
    // AI сказав що новина слабка — пропускаємо
    markAsPosted(db, article.ID)
    continue
}
if errors.Is(err, ai.ErrAllProvidersFailed) {
    // Обидва провайдери впали — логуємо, пропускаємо новину
    slog.Error("all AI providers failed", "article_id", article.ID)
    continue
}
// usedProvider зберігаємо в БД для дебагу
updateArticle(db, article.ID, text, usedProvider)
```

---

### 5.5 Ліміти та стратегія

| Провайдер | Модель | RPM | RPD | Пауза |
|---|---|---|---|---|
| Gemini (primary) | `gemini-2.5-flash` | 10 | 500 | 8s |
| OpenRouter (fallback) | `:free` моделі | 20 | 200* | 4s |

> *200 RPD без поповнення рахунку. Якщо покладеш $10 одноразово — стає **1000 RPD**.  
> Для нашого боту (max 15 новин на день) — обох лімітів більш ніж достатньо.

**Коли спрацьовує fallback**:
- Gemini повернув HTTP 429 (rate limit)
- Gemini повернув HTTP 503 (сервіс недоступний)
- Gemini timeout (> 30 секунд)
- Будь-яка інша помилка від Gemini

---

## 📬 Phase 6 — Telegram Posting

**File**: `internal/telegram/poster.go`  
**Library**: `github.com/go-telegram-bot-api/telegram-bot-api/v5`

**Telegram ліміти**:
- `sendPhoto` caption: max **1024 символів** ← ціль 850
- `sendMessage`: max 4096 символів
- Flood: ~20 повідомлень/хвилину в канал

```go
func PostArticle(bot *tgbotapi.BotAPI, channelID string, article Article) error {
    text := article.BodyUA
    if article.ImageURL != "" {
        msg := tgbotapi.NewPhotoShare(channelID, article.ImageURL)
        msg.Caption = text
        msg.ParseMode = "HTML"
        if _, err := bot.Send(msg); err != nil {
            return sendText(bot, channelID, text) // fallback на текст
        }
        return nil
    }
    return sendText(bot, channelID, text)
}
```

Пауза між постами: `time.Sleep(3 * time.Second)`.  
Максимум **3 пости за cron-запуск**.

---

## ⚙️ Phase 7 — Main Orchestrator

**File**: `cmd/bot/main.go`

```
Потік кожного cron-запуску:

1.  Load config
2.  DB connect (retry 3x — Neon може прокидатись)
3.  DB cleanup (> 30 днів)
4.  Init AI chain: GeminiProvider → OpenRouterProvider
5.  Fetch RSS паралельно (goroutines + WaitGroup, timeout 30s)
6.  Для кожної статті:
    a. ResolveRedirect якщо потрібно
    b. URL dedup → є? → skip
    c. Title similarity dedup (24h) → дублікат? → skip
    d. Score < 4 → skip
    e. INSERT в БД (posted_tg = false)
7.  SELECT top-3 unposted ORDER BY score DESC
8.  Для кожної:
    a. chain.Rewrite(ctx, ...) — з автофолбеком
    b. ErrSkipped → mark posted, continue
    c. ErrAllProvidersFailed → log error, continue
    d. UPDATE body_ua + ai_provider в БД
    e. Post to Telegram
    f. Sleep(3s)
    g. UPDATE posted_tg = true
9.  slog.Info("run complete",
        "fetched", fetchedCount,
        "deduped", dedupedCount,
        "scored_out", scoredOutCount,
        "posted", postedCount,
        "gemini_used", geminiCount,
        "openrouter_used", openRouterCount,
    )

Якщо 0 статей → виходимо тихо. Ніяких постів.
```

---

## 🔄 Phase 8 — GitHub Actions

**File**: `.github/workflows/cron.yml`

```yaml
name: Nintendo News Bot

on:
  schedule:
    - cron: '0 6 * * *'
    - cron: '0 10 * * *'
    - cron: '0 14 * * *'
    - cron: '0 18 * * *'
    - cron: '0 21 * * *'
  workflow_dispatch:

jobs:
  run-bot:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v6

      - name: Set up Go
        uses: actions/setup-go@v6
        with:
          go-version-file: '.go-version'
          cache: true

      - name: Build
        run: go build -o bot ./cmd/bot

      - name: Run bot
        env:
          DATABASE_URL:          ${{ secrets.DATABASE_URL }}
          TELEGRAM_BOT_TOKEN:    ${{ secrets.TELEGRAM_BOT_TOKEN }}
          TELEGRAM_CHANNEL_ID:   ${{ secrets.TELEGRAM_CHANNEL_ID }}
          GEMINI_API_KEY:        ${{ secrets.GEMINI_API_KEY }}
          OPENROUTER_API_KEY:    ${{ secrets.OPENROUTER_API_KEY }}
        run: ./bot
```

**File**: `.go-version`
```
1.26
```

---

## 🔧 Phase 9 — Config

**File**: `.env.example`
```env
DATABASE_URL=postgresql://user:pass@ep-xxx.eu-central-1.aws.neon.tech/neondb?sslmode=require
TELEGRAM_BOT_TOKEN=123456:ABC-your-token
TELEGRAM_CHANNEL_ID=@your_channel
GEMINI_API_KEY=AIzaSy...
OPENROUTER_API_KEY=sk-or-v1-...

# Опціонально
GEMINI_MODEL=gemini-2.5-flash
MAX_POSTS_PER_RUN=3
MIN_SCORE=4
DRY_RUN=false
```

**Config struct** додаткові поля:
```go
type Config struct {
    // ... всі попередні поля ...
    OpenRouterAPIKey        string
    SleepBetweenAICalls     time.Duration // default: 8s
}
```

---

## 📦 Go Dependencies

```
module github.com/yourname/nintendo-news-bot

go 1.26

require (
    github.com/mmcdole/gofeed                          v1.3.0
    github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1
    github.com/lib/pq                                  v1.10.9
    github.com/joho/godotenv                           v1.5.1
    google.golang.org/genai                            v1.0.0
    // OpenRouter використовує стандартний net/http — без зовнішніх залежностей
)
```

---

## 🧪 Testing Checklist

- [ ] `DRY_RUN=true` — fetch + score + AI rewrite без посту в TG
- [ ] Спеціально вичерпати Gemini ліміт → переконатись що OpenRouter підхоплює
- [ ] Перевірити що `ai_provider` в БД показує який провайдер реально спрацював
- [ ] Дедуп ловить одну новину з 2 різних джерел
- [ ] score < 4 відкидаються без виклику AI
- [ ] Пост ніколи не перевищує 1000 символів
- [ ] `workflow_dispatch` ручний тригер
- [ ] Перший реальний пост на приватний тест-канал
- [ ] `GOEXPERIMENT=goroutineleak go test ./...`

---

## 🚀 Implementation Order

```
Step 1:  migrations/001_init.sql
Step 2:  internal/config/config.go
Step 3:  internal/db/postgres.go + queries.go
Step 4:  internal/fetcher/sources.go + rss.go
Step 5:  internal/dedup/dedup.go
Step 6:  internal/scorer/scorer.go
Step 7:  internal/ai/prompt.go          ← СПОЧАТКУ промпт і стиль (всі провайдери залежать від нього)
Step 8:  internal/ai/provider.go        ← інтерфейс + ErrSkipped (без промпту)
Step 9:  internal/ai/gemini.go          ← primary, викликає BuildPrompt()
Step 10: internal/ai/openrouter.go      ← fallback, викликає BuildPrompt()
Step 11: internal/ai/chain.go           ← chain логіка + sanitizeOutput
Step 12: internal/telegram/poster.go
Step 13: internal/cleaner/cleaner.go
Step 14: cmd/bot/main.go
Step 15: .github/workflows/cron.yml
Step 16: .go-version, go.mod, .env.example, README.md
```

Реалізуй строго по порядку. Кожен зовнішній виклик — обов'язковий error handling.  
Логування через `log/slog`. AI провайдери реалізують єдиний інтерфейс `AIProvider`.
