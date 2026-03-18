# 🎮 Nintendo News Bot

Telegram-бот що автоматично збирає новини про Nintendo з RSS-стрічок, оцінює їх релевантність, переписує в авторському стилі через AI і публікує в Telegram-канал.

## Stack

| Компонент | Технологія |
|---|---|
| Мова | Go 1.26 |
| База даних | PostgreSQL (Neon) |
| AI routing | `ai_config.json` priority chain |
| CI/CD | GitHub Actions |

## Структура (Backend)

```
nintendoflow/
├── cmd/bot/main.go              # точка входу, оркестратор
├── pkg/
│   ├── config/config.go         # завантаження конфігурації з env
│   ├── db/postgres.go           # підключення до Neon PostgreSQL
│   ├── db/queries.go            # всі SQL-запити
│   ├── fetcher/rss.go           # паралельне завантаження RSS
│   ├── dedup/dedup.go           # дедуплікація (URL hash + Jaccard)
│   ├── scorer/scorer.go         # оцінка релевантності
│   ├── ai/prompt.go             # ← ЄДИНИЙ файл з промптом і стилем
│   ├── ai/provider.go           # AIProvider інтерфейс + помилки
│   ├── ai/gemini.go             # primary: Gemini 2.5 Flash
│   ├── ai/openrouter.go         # fallback: OpenRouter free models
│   ├── ai/manager.go            # AI manager + retry/fallback
│   ├── telegram/poster.go       # публікація в Telegram
│   └── cleaner/cleaner.go       # очищення старих записів
├── cmd/export/main.go           # export articles to GitHub Pages JSON
├── migrations/001_init.sql      # SQL схема
└── .github/workflows/cron.yml  # GitHub Actions cron
```

## GitHub Secrets

| Secret | Опис |
|---|---|
| `DATABASE_URL` | PostgreSQL DSN (Neon) |
| `TELEGRAM_BOT_TOKEN` | Токен бота від @BotFather |
| `TELEGRAM_CHANNEL_ID` | ID каналу, напр. `@mychannel` |
| `GEMINI_API_KEY` | Google AI Studio API key |
| `GH_MODELS_API_KEY` | GitHub Models token |
| `GROQ_API_KEY` | Groq API key |
| `OPENROUTER_API_KEY` | OpenRouter key (optional, disabled by default) |
| `AI_CONFIG_PATH` | Optional path to AI router JSON (default `ai_config.json`) |
| `TEST_TELEGRAM_TOKEN` | Test bot token for moderation preview + webhook callbacks |
| `TEST_CHANNEL_ID` | Test channel where approved posts are published |
| `TEST_ADMIN_CHAT_ID` | Admin chat/group for preview moderation messages (optional, fallback: `TEST_CHANNEL_ID`) |
| `TEST_MODERATION_MODE` | Enable two-step test pipeline (`true`/`false`) |
| `TELEGRAM_WEBHOOK_SECRET` | Optional secret token for Telegram webhook validation |

## Розклад постингу

Бот запускається через GitHub Actions 5 разів на день:
`06:00 · 10:00 · 14:00 · 18:00 · 21:00 UTC`

Максимум **3 пости** за один запуск. Якщо нема якісних новин — тиша.

## Локальна розробка

```bash
cp .env.example .env
# заповни .env реальними значеннями

go run ./cmd/bot
# або з dry run (не постить в TG):
DRY_RUN=true go run ./cmd/bot
```

## Web Archive on GitHub Pages (2026 Best Practices)

Static web archive deployed to GitHub Pages with zero frameworks and optimal performance.

**Features:**
- No JavaScript dependencies (zero framework overhead)
- Lazy-loaded images with fallback placeholders
- Dark mode by default with custom CSS tokens
- Glassmorphism 2.0 UI with parallax hero
- Fully responsive (mobile-first design)
- 100% SEO-friendly (pre-rendered HTML)

```bash
# Local preview (requires Python 3.9+)
cd docs/
python -m http.server 8080
```

Then open: `http://localhost:8080`

**Production Deployment:**

1. Push to GitHub (main branch)
2. Go to **Settings** → **Pages**
3. Set source to **GitHub Actions**
4. Run workflow `.github/workflows/export-to-pages.yml` (or wait for schedule)

**Automatic Article Export:**

Workflow `.github/workflows/export-to-pages.yml` runs every 4 hours (6 times/day):
- Connects to PostgreSQL (DATABASE_URL secret)
- Exports all published articles to `docs/data.json`
- Deploys `docs/` as Pages artifact via official `actions/deploy-pages`

Configure GitHub Actions secrets:
- `DATABASE_URL` — PostgreSQL connection string

Data requirement:
- only rows with `articles.status='published'` are exported to the site.

## AI router config

`ai_config.json` defines provider order and enablement. The bot picks the first enabled
provider, and if it hits a rate-limit (429/quota) it falls back to the next enabled one.
API keys are read only from environment variables named in `api_key_env`.

## Test Moderation Pipeline

- Workflow: `.github/workflows/cron-test.yml`
- Behavior (`TEST_MODERATION_MODE=true`):
  - bot fetches/scorers/rewrites as usual,
  - saves article to DB with `status=pending`,
  - sends moderation preview with inline buttons: `Publish`, `Edit`, `Reject`.
- Webhook endpoint: `api/webhook.go` (Vercel Go function)
  - `Publish` -> post to `TEST_CHANNEL_ID`, update DB status to `published`.
  - `Reject` -> update DB status to `rejected`.
  - `Edit` -> mark status as `needs_edit`.

## Як змінити стиль

Відредагуй `pkg/ai/prompt.go` — константу `styleGuide`.  
Всі AI провайдери автоматично підхоплять зміни.

## Ліміти AI

| Провайдер | Модель | RPM | RPD |
|---|---|---|---|
| Gemini (primary) | `gemini-2.5-flash` | 10 | 500 |
| OpenRouter (fallback) | `:free` моделі | 20 | 200 |

