# 🎮 Nintendo News Bot

Telegram-бот що автоматично збирає новини про Nintendo з RSS-стрічок, оцінює їх релевантність, переписує в авторському стилі через AI і публікує в Telegram-канал.

## Stack

| Компонент | Технологія |
|---|---|
| Мова | Go 1.26 |
| База даних | PostgreSQL (Neon) |
| AI (primary) | Gemini 2.5 Flash |
| AI (fallback) | OpenRouter free models |
| CI/CD | GitHub Actions |

## Структура

```
nintendoflow/
├── cmd/bot/main.go              # точка входу, оркестратор
├── internal/
│   ├── config/config.go         # завантаження конфігурації з env
│   ├── db/postgres.go           # підключення до Neon PostgreSQL
│   ├── db/queries.go            # всі SQL-запити
│   ├── fetcher/sources.go       # список RSS-джерел
│   ├── fetcher/rss.go           # паралельне завантаження RSS
│   ├── dedup/dedup.go           # дедуплікація (URL hash + Jaccard)
│   ├── scorer/scorer.go         # оцінка релевантності
│   ├── ai/prompt.go             # ← ЄДИНИЙ файл з промптом і стилем
│   ├── ai/provider.go           # AIProvider інтерфейс + помилки
│   ├── ai/gemini.go             # primary: Gemini 2.5 Flash
│   ├── ai/openrouter.go         # fallback: OpenRouter free models
│   ├── ai/chain.go              # fallback chain + sanitizeOutput
│   ├── telegram/poster.go       # публікація в Telegram
│   └── cleaner/cleaner.go       # очищення старих записів
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
| `OPENROUTER_API_KEY` | OpenRouter key (опціонально — fallback) |

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

## Як змінити стиль

Відредагуй `internal/ai/prompt.go` — константу `styleGuide`.  
Всі AI провайдери автоматично підхоплять зміни.

## Ліміти AI

| Провайдер | Модель | RPM | RPD |
|---|---|---|---|
| Gemini (primary) | `gemini-2.5-flash` | 10 | 500 |
| OpenRouter (fallback) | `:free` моделі | 20 | 200 |

