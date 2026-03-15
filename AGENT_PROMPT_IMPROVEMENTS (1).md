# Agent Prompt: Nintendo News Bot — Improvements

## Твоя роль

Ти — Go-розробник що вносить точкові зміни до **вже існуючого** проекту `nintendo-news-bot`.  
Ти **не пишеш проект з нуля**. Читаєш що є — вносиш тільки те що потрібно.

---

## КРОК 0 — Обов'язково прочитати проект перед будь-яким кодом

```bash
ls -R
cat go.mod
cat .env.example
cat feeds.yaml
cat keywords.yaml
```

Прочитай кожен `.go` файл в `internal/` та `cmd/`. Не вигадуй що є — звіряйся з реальними файлами. Тільки після цього починай роботу.

---

## Задача 1 — Видалити весь хардкод конфігурації

### Проблема
В `.go` файлах можуть залишатись захардкоджені RSS джерела, списки ключових слів або числові пороги прямо в логіці.

### Правило
| Що | Де має жити |
|---|---|
| RSS джерела (URL, name, type...) | тільки `feeds.yaml` |
| Ключові слова і ваги | тільки `keywords.yaml` |
| Числові пороги (score, age hours) | тільки `.env` через `config.go` |

### Що перевірити і виправити

**1.1** `internal/filter/scorer.go` — функція `Score()` приймає `[]config.Keyword` як параметр. Всередині функції **нема** власного списку слів. Якщо є — видалити, використовувати переданий список.

**1.2** `internal/fetcher/rss.go` — **нема** `var Sources = []Source{...}` або будь-якого slice з URL всередині. Джерела приходять як аргумент від `config.LoadFeeds()`.

**1.3** `cmd/bot/main.go` — є такі виклики і передача далі по пайплайну:
```go
feeds, err := config.LoadFeeds(cfg.FeedsPath)
keywords, err := config.LoadKeywords(cfg.KeywordsPath)
```

**1.4** Якщо знаходиш хардкод — переносиш в YAML і оновлюєш файл. Не залишаєш `// TODO` — просто робиш.

**1.5** Константи типу `const MinScoreThreshold = 4` в бізнес-логіці — замінити на `cfg.MinScore` що читається з env.

---

## Задача 2 — YAML як єдиний інтерфейс контролю

Власник бота керує скрапером через `feeds.yaml` і `keywords.yaml` в репозиторії. Push в main = зміна набирає чинності на наступному cron-запуску. Ніяких додаткових інструментів не потрібно.

### Що забезпечити

**2.1** `feeds.yaml` — переконайся що кожен запис має всі поля і коментарі пояснюють навіщо це джерело. Зразок запису:

```yaml
- url: "https://www.videogameschronicle.com/feed/"
  name: "Video Games Chronicle"
  lang: en
  priority: 90      # вище = важливіше при однаковому score
  active: true      # false = повністю вимкнути без видалення
  type: insider     # official / media / insider / aggregator
  needs_redirect_resolve: false  # true тільки для Google News RSS
```

**2.2** `keywords.yaml` — кожен запис має коментар що пояснює чому такий weight. Зразок:

```yaml
- word: "nintendo direct"
  category: event
  weight: 100   # найвища подія — завжди постимо
  # Зміни weight тут і зроби git push — бот підхопить на наступному запуску

- word: "top 10"
  category: spam
  weight: -50   # від'ємний = штраф. Якщо score все одно > MIN_SCORE — стаття пройде
```

**2.3** `config/loader.go` — при завантаженні YAML логує скільки фідів і keywords завантажено:
```go
slog.Info("config loaded", "active_feeds", len(active), "keywords", len(keywords))
```
Це дає власнику видимість — він бачить в логах GitHub Actions що його зміни підхопились.

**2.4** `config/loader.go` — якщо YAML файл не знайдено або невалідний — повертати чітку помилку з шляхом до файлу:
```go
return nil, fmt.Errorf("feeds config: cannot read %q: %w", path, err)
```

---

## Задача 3 — Telegram UI

### 3.1 Inline кнопка "Читати повністю"

**File**: `internal/telegram/poster.go`

Кожен пост — і з фото, і без фото — повинен мати inline keyboard:
```
[ 🔗 Читати повністю ]
```

```go
func makeKeyboard(sourceURL string) tgbotapi.InlineKeyboardMarkup {
    return tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonURL("🔗 Читати повністю", sourceURL),
        ),
    )
}
```

Додати до `sendPhoto`:
```go
msg.ReplyMarkup = makeKeyboard(article.SourceURL)
```

Додати до `sendMessage`:
```go
msg.ReplyMarkup = makeKeyboard(article.SourceURL)
```

Рядок `🔗 Джерело: ...` в тексті **залишити** — він частина стилю. Кнопка — додатковий елемент зручності.

Переконайся що `article.SourceURL` є в структурі `db.Article` і заповнюється в `db.TopUnposted()`. Якщо відсутнє — додай в SELECT і Scan.

### 3.2 HTML форматування

`ParseMode` у всіх повідомленнях: `"HTML"`. Перевір що це вже встановлено.

Додай в `internal/ai/prompt.go` в секцію `== ЖОРСТКІ ПРАВИЛА ==` такий блок — **не замінюй існуючі правила, додай після них**:

```
== HTML ТЕГИ В TELEGRAM ==
Для візуальної ієрархії використовуй:
- <b>жирний</b> — назви ігор, консолей, компаній при ПЕРШОМУ згадуванні в тексті
- <i>курсив</i> — непідтверджені чутки, цитати інсайдерів, іронічні ремарки
Ліміт: максимум 2-3 теги на весь пост. Не перевантажуй.
НЕ використовуй: <u> <s> <code> <pre>

Приклад правильного використання:
"Страх у серці <b>PlayStation</b> перед великим та могутнім <b>Steam Machine</b>...
<i>За даними інсайдерів</i>, Sony знову затягує корсет Exclusivity (ексклюзивності)."
```

### 3.3 Шаблони за типом новини

**File**: `internal/telegram/poster.go`

Додай функцію `buildCaption` і використовуй її замість прямого `article.BodyUA`:

```go
// buildCaption формує фінальний текст посту залежно від типу джерела.
// Офіційні новини і інсайди отримують префікс для візуальної ієрархії.
// Загальна довжина не перевищує maxLen символів (для sendPhoto: 1024).
func buildCaption(article *db.Article, maxLen int) string {
    var prefix string
    switch article.SourceType {
    case "official":
        prefix = "📢 <b>ОФІЦІЙНО</b>\n\n"
    case "insider":
        prefix = "🕵️ <i>Інсайд</i>\n\n"
    case "aggregator":
        prefix = "📡 <i>Чутки</i>\n\n"
    default:
        prefix = "" // media — без префіксу, чистий стиль
    }

    full := prefix + article.BodyUA
    runes := []rune(full)
    if len(runes) > maxLen {
        // Обрізаємо BodyUA щоб вмістився префікс
        bodyRunes := []rune(article.BodyUA)
        allowed := maxLen - len([]rune(prefix)) - 3 // -3 для "..."
        if allowed > 0 && allowed < len(bodyRunes) {
            full = prefix + string(bodyRunes[:allowed]) + "..."
        }
    }
    return full
}
```

Використання:
```go
// Для sendPhoto (caption ліміт 1024)
msg.Caption = buildCaption(article, 1024)

// Для sendMessage (ліміт 4096)
msg := tgbotapi.NewMessageToChannel(channelID, buildCaption(article, 4096))
```

### 3.4 Рекомендований опис каналу

Це не код. Наприкінці виконання всіх задач виведи в stdout:

```
╔══════════════════════════════════════════════╗
║        РЕКОМЕНДОВАНИЙ ОПИС КАНАЛУ           ║
╠══════════════════════════════════════════════╣
║ Назва: Nintendo UA 🎮                        ║
║                                              ║
║ Опис (до 255 символів):                      ║
║ Найсвіжіші новини Nintendo українською —     ║
║ ігри, залізо, анонси та інсайди.             ║
║ Без води, з характером. 5 разів на день 🕹️   ║
║                                              ║
║ Як встановити:                               ║
║ Telegram → твій канал → Edit → Description  ║
╚══════════════════════════════════════════════╝
```

---

## Задача 4 — Переконатись що проект компілюється

Після всіх змін:

```bash
go mod tidy
go build ./cmd/bot
go vet ./...
```

Якщо є помилки компіляції або vet warnings — виправ їх. Не здавай проект в нерабочому стані.

---

## Чого НЕ робити

- **НЕ переписувати** файли які не потребують змін
- **НЕ змінювати** `migrations/001_init.sql` — БД вже розгорнута в Neon
- **НЕ чіпати** `.github/workflows/cron.yml`
- **НЕ додавати** нові зовнішні залежності без крайньої необхідності
- **НЕ використовувати** `log.Fatal` або `panic` в бізнес-логіці — тільки `return err`
- **НЕ створювати** cmd/tune або будь-який інший CLI інструмент — контроль через YAML в репо
- **НЕ дублювати** логіку між файлами — якщо потрібна спільна утиліта, вона йде в `internal/`

---

## Порядок виконання

```
Крок 1: Прочитати всі існуючі файли
Крок 2: Задача 1 — видалити хардкод (точкові зміни в існуючих файлах)
Крок 3: Задача 2 — перевірити і покращити YAML коментарі та loader логування
Крок 4: Задача 3.1 — додати inline кнопку в poster.go
Крок 5: Задача 3.2 — додати HTML правила в prompt.go
Крок 6: Задача 3.3 — додати buildCaption в poster.go
Крок 7: Задача 4 — go mod tidy && go build && go vet
Крок 8: Вивести підсумок змін і рекомендований опис каналу
```

---

## Критерії готовості

```
[ ] go build ./cmd/bot — без помилок
[ ] go vet ./... — без попереджень
[ ] В жодному .go файлі немає хардкоду RSS URL або keyword слів
[ ] cfg.MinScore використовується замість константи в scorer
[ ] feeds.yaml і keywords.yaml мають коментарі для кожного запису
[ ] Loader логує кількість завантажених фідів і keywords
[ ] Кожен пост в TG має inline кнопку з посиланням на джерело
[ ] Офіційні новини мають префікс 📢, інсайди — 🕵️, чутки — 📡
[ ] ParseMode = "HTML" скрізь де відправляються повідомлення
[ ] buildCaption не перевищує 1024 символи для sendPhoto
[ ] Рекомендований опис каналу виведено в stdout
```
