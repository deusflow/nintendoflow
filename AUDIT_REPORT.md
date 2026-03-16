# 🔍 AUDIT REPORT — nintendoflow
**Date:** 2026-03-15 | **Auditor:** Senior Software Engineer (AI)
**Scope:** All `.go`, `.yaml`, `.sql`, workflow files

---

## 🟢 WHAT WORKS

| # | Component | Details |
|---|-----------|---------|
| 1 | **Core pipeline** | Full sequence is wired: config → DB retry-connect → cleaner → AI init → parallel RSS fetch → score/filter → AI selector → OG image → AI rewrite → DB insert → Telegram post → mark posted |
| 2 | **Config loading** | `godotenv` picks up `.env` for local dev, silently ignores missing in CI. All required fields are validated with clear error messages |
| 3 | **DB retry on cold start** | `postgres.go` retries 3× with back-off — correct for Neon serverless (cold-wake latency) |
| 4 | **Inline migration** | `RunMigration()` is idempotent (`CREATE IF NOT EXISTS`, `ALTER ADD COLUMN IF NOT EXISTS`). Safe to re-run on every bot start |
| 5 | **Per-domain RSS throttle** | Feeds are grouped by hostname, serialised with 2s inter-domain delay, different domains run in parallel goroutines |
| 6 | **Two-layer hash dedup** | URL hash + title hash loaded from DB into memory maps; duplicates are skipped in O(1) per item. In-run dedup prevents the same article winning twice |
| 7 | **Scorer with topic priorities** | `effective_weight = keyword.Weight × topic.Priority / 100`; supports anchor/comparison/signal/tech/reject roles; disabled topics are skipped |
| 8 | **AI budget manager** | Hard cap of 2 AI calls per run; 20s inter-call delay; per-provider 429 retry with parsed `retryDelay`; cascade fallback to OpenRouter |
| 9 | **OG image lazy-fetch** | `og:image` is fetched only for the single winning article, with a 10s context timeout and graceful fallback to text-only post |
| 10 | **DRY_RUN flag** | `DRY_RUN=true` prevents the Telegram API call and logs intent |
| 11 | **Telegram post** | Correctly uses photo+caption (1024 char cap) with HTML parse mode; falls back to text-only if photo send fails |
| 12 | **Unit tests** | `scorer_test.go` (3 cases) and `og_image_test.go` (3 cases) exist and cover meaningful logic |
| 13 | **Prompt injection guard** | Prompt explicitly instructs the model to ignore hidden instructions in article body |

---

## 🔴 WHAT IS BROKEN

### BUG-1 · `cfg.GeminiModel` is silently ignored
**File:** `internal/ai/gemini.go:26`, `internal/config/config.go:76`, `cmd/bot/main.go:95`
`NewGeminiProvider` hardcodes the model string `"gemini-2.5-flash"` inside the constructor. The `cfg.GeminiModel` env-var is loaded and validated but **never passed** to the provider. Setting `GEMINI_MODEL` in env does nothing.

---

### BUG-2 · DRY_RUN writes real data to the production database
**File:** `cmd/bot/main.go:258–278`
`db.InsertArticle` and `db.UpdateBodyUA` are both called **before** the `cfg.DryRun` gate. Every dry run permanently inserts an article and marks it with a generated `body_ua`. The DB is not clean after a dry run.
```
Line 258: articleID, err := db.InsertArticle(...)   ← writes to DB
Line 267: db.UpdateBodyUA(...)                        ← writes to DB
Line 270: if cfg.DryRun { ... return }               ← too late
```

---

### BUG-3 · `cfg.RecentTitlesHours` is always ignored
**File:** `cmd/bot/main.go:130, 136`
The constant `48` is hardcoded in both `FetchRecentURLHashes` and `FetchRecentTitleHashes` calls. `cfg.RecentTitlesHours` (default 24, configurable via env) is loaded but never referenced.

---

### BUG-4 · `buildSelectorPrompt` hardcodes "5 headlines" regardless of actual count
**File:** `cmd/bot/main.go:290`
The prompt text says "Here are 5 news headlines" even when `topN < 5`. This misleads the AI and risks it returning an index that is out of range.

---

### BUG-5 · `go 1.26` in `go.mod` — version does not exist
**File:** `go.mod:3`
Go 1.26 has not been released. This is likely a typo for `1.23`. Will fail any strict CI toolchain check.

---

## 🟡 RISKS & BOTTLENECKS

### RISK-1 · All DB queries run without a context (no timeout)
`InsertArticle`, `UpdateBodyUA`, `MarkPosted`, `Cleanup`, `RunMigration` use `*sql.DB` without `context.Context`. If Neon stalls after cold wake, these calls hang forever.

### RISK-2 · OpenRouter uses `"openrouter/auto"` — no quality guarantee
`openrouter/auto` lets OpenRouter pick any free model. The chosen model may not speak Ukrainian, may output Markdown instead of HTML, or ignore the prompt format entirely.

### RISK-3 · `ResolveRedirect` creates a new `http.Client` per article
A new `http.Client` is created per redirect resolution. Google News feeds can return 20–30 items — 20–30 uncached TCP connections per run with no pool reuse.

### RISK-4 · `fetchSource` creates a new `http.Client` per feed
Same pattern. Minor GC pressure, inconsistent with Go best practice of a shared, reused client.

### RISK-5 · No GitHub Actions CI/CD at all
`.github/` directory does not exist. No automated tests, no linting, no scheduled cron trigger.

### RISK-6 · `InsertArticle` ON CONFLICT silently mutates already-posted articles
On duplicate `source_url`, the ON CONFLICT updates only `score`. But `UpdateBodyUA` immediately after unconditionally overwrites `body_ua` on that same row — even if the article was already posted to Telegram.

### RISK-7 · Failed selector still consumes AI budget
`manager.callsUsed++` fires at the top of `Generate()` before any network call. A failed selector leaves only 1 slot for the rewrite with no warning log that budget was spent on a failed call.

---

## 🗑️ DEAD CODE

| # | Symbol | File | Notes |
|---|--------|------|-------|
| 1 | `Config.SleepBetweenAICalls` | `config/config.go:25` | Set to `8s`, never read. Manager uses hardcoded `aiCallDelay = 20s` |
| 2 | `Config.MaxPostsPerRun` | `config/config.go:21` | Set to `3`, never read. Bot always posts 1 article |
| 3 | `Config.RecentTitlesHours` | `config/config.go:23` | Set to `24`, never passed anywhere (see BUG-3) |
| 4 | `db.RecentTitles()` | `db/queries.go:52` | Implemented, never called |
| 5 | `db.URLExists()` | `db/queries.go:68` | Implemented, never called |
| 6 | `db.HashExists()` | `db/queries.go:75` | Implemented, never called |
| 7 | `db.TopUnposted()` | `db/queries.go:136` | Implemented, never called |
| 8 | `dedup.IsDuplicate()` | `dedup/dedup.go:23` | Layer-2 Jaccard dedup fully built, never invoked in pipeline |
| 9 | `GeminiProvider.Rewrite()` | `ai/gemini.go:39` | Not part of `AIProvider` interface, never called |
| 10 | `OpenRouterProvider.Rewrite()` | `ai/openrouter.go:41` | Not part of `AIProvider` interface, never called |
| 11 | `SourceType*` constants | `fetcher/sources.go` | Defined but never imported; `poster.go` uses raw string literals |
| 12 | `migrations/001_init.sql` | `migrations/001_init.sql` | Not executed by app — live migration is inline in `queries.go`. Stale documentation only |

---

## 🔵 ACTION PLAN (prioritized)

| Priority | File | Line(s) | Action |
|----------|------|---------|--------|
| 🔴 P1 | `cmd/bot/main.go` | 258–278 | Move DRY_RUN check **before** `InsertArticle` to stop DB pollution on dry runs |
| 🔴 P1 | `internal/ai/gemini.go` | 26 | Accept `model string` param in `NewGeminiProvider`; pass `cfg.GeminiModel` from `main.go:95` |
| 🔴 P1 | `cmd/bot/main.go` | 290 | Replace hardcoded `"5"` in selector prompt with dynamic `len(candidates)` |
| 🟡 P2 | `cmd/bot/main.go` | 130, 136 | Replace hardcoded `48` with `cfg.RecentTitlesHours` |
| 🟡 P2 | `internal/ai/openrouter.go` | 16 | Replace `"openrouter/auto"` with a specific, reliable free model |
| 🟡 P2 | `internal/db/queries.go` | multiple | Add `context.Context` to `InsertArticle`, `UpdateBodyUA`, `MarkPosted`, `Cleanup` |
| 🟡 P2 | `go.mod` | 3 | Fix `go 1.26` → `go 1.23` |
| 🟡 P3 | `.github/workflows/` | — | Create `bot.yml` with scheduled cron trigger + `go test ./...` |
| 🟢 P4 | `internal/config/config.go` | 21–25 | Remove `MaxPostsPerRun`, `SleepBetweenAICalls` (dead); wire `RecentTitlesHours` |
| 🟢 P4 | `internal/db/queries.go` | 52–85, 136–161 | Remove `RecentTitles`, `URLExists`, `HashExists`, `TopUnposted` |
| 🟢 P4 | `internal/ai/gemini.go` | 39–50 | Remove unused `Rewrite()` method |
| 🟢 P4 | `internal/ai/openrouter.go` | 41–51 | Remove unused `Rewrite()` method |
| 🟢 P4 | `internal/fetcher/sources.go` | all | Use constants in `poster.go` or delete the file |
| 🟢 P5 | `internal/fetcher/rss.go` | 96, 163 | Extract shared package-level `http.Client`; reuse across feeds and redirects |
