package ai

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"
)

// NewsInput — структура для передачи параметров новости.
type NewsInput struct {
	Title        string
	Body         string
	Source       string
	PublishedAt  *time.Time
	CurrentTime  time.Time
	DevilTheory  string
	FactsPath    string
}

// DeviceFact represents a verified hardware release event.
type DeviceFact struct {
	Name        string
	ReleaseDate time.Time
	Status      string
}

var defaultFacts = []DeviceFact{
	{Name: "Nintendo Switch 2", ReleaseDate: time.Date(2025, 6, 5, 0, 0, 0, 0, time.UTC), Status: "вже в продажу"},
	{Name: "Nintendo Switch 2 Pro Controller", ReleaseDate: time.Date(2025, 6, 5, 0, 0, 0, 0, time.UTC), Status: "вже в продажу"},
	{Name: "Nintendo Switch (оригінальна модель)", ReleaseDate: time.Date(2017, 3, 3, 0, 0, 0, 0, time.UTC), Status: "в продажу"},
	{Name: "Nintendo Switch OLED", ReleaseDate: time.Date(2021, 10, 8, 0, 0, 0, 0, time.UTC), Status: "в продажу"},
	{Name: "Nintendo Switch Lite", ReleaseDate: time.Date(2019, 9, 20, 0, 0, 0, 0, time.UTC), Status: "в продажу"},
}

// MonthsBetween calculates elapsed full calendar months from 'from' to 'to'.
func MonthsBetween(from, to time.Time) int {
	if to.Before(from) {
		return 0
	}
	years := to.Year() - from.Year()
	months := int(to.Month()) - int(from.Month())
	total := years*12 + months
	if to.Day() < from.Day() {
		total--
	}
	if total < 0 {
		total = 0
	}
	return total
}

// FormatMonthsUA formats the month count into natural Ukrainian phrasing.
func FormatMonthsUA(n int) string {
	mod10 := n % 10
	mod100 := n % 100
	if mod100 >= 11 && mod100 <= 19 {
		return fmt.Sprintf("%d місяців", n)
	}
	switch mod10 {
	case 1:
		return fmt.Sprintf("%d місяць", n)
	case 2, 3, 4:
		return fmt.Sprintf("%d місяці", n)
	default:
		return fmt.Sprintf("%d місяців", n)
	}
}

// ParseFacts parses lines in format: "назва | дата релізу | статус".
func ParseFacts(content string) []DeviceFact {
	var facts []DeviceFact
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) >= 3 {
			name := strings.TrimSpace(parts[0])
			dateStr := strings.TrimSpace(parts[1])
			status := strings.TrimSpace(parts[2])
			t, err := time.Parse("2006-01-02", dateStr)
			if err != nil {
				continue
			}
			facts = append(facts, DeviceFact{
				Name:        name,
				ReleaseDate: t,
				Status:      status,
			})
		}
	}
	return facts
}

// FormatFactsBlock formats device facts with dynamically calculated months since release.
func FormatFactsBlock(facts []DeviceFact, now time.Time) string {
	var b strings.Builder
	for _, f := range facts {
		months := MonthsBetween(f.ReleaseDate, now)
		dateStr := f.ReleaseDate.Format("2006-01-02")
		b.WriteString(fmt.Sprintf("• %s | дата релізу: %s | статус: %s (пройшло %s з релізу)\n",
			f.Name, dateStr, f.Status, FormatMonthsUA(months)))
	}
	return strings.TrimSpace(b.String())
}

// LoadWorldFacts loads device release facts from facts.md (or custom path / FACTS_PATH env).
// If missing or unreadable, logs a warning with searched paths and returns fallback facts.
func LoadWorldFacts(path string, now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	var searchedPaths []string
	if path == "" {
		path = os.Getenv("FACTS_PATH")
	}
	if path != "" {
		searchedPaths = append(searchedPaths, path)
		if content, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(content))) > 0 {
			facts := ParseFacts(string(content))
			if len(facts) > 0 {
				return FormatFactsBlock(facts, now)
			}
		}
	}
	candidates := []string{"facts.md", "../facts.md", "../../facts.md"}
	for _, c := range candidates {
		searchedPaths = append(searchedPaths, c)
		if content, err := os.ReadFile(c); err == nil && len(strings.TrimSpace(string(content))) > 0 {
			facts := ParseFacts(string(content))
			if len(facts) > 0 {
				return FormatFactsBlock(facts, now)
			}
		}
	}

	slog.Warn("facts.md not found, using fallback world facts", "searched_paths", searchedPaths)
	return FormatFactsBlock(defaultFacts, now)
}

// sanitizeInput removes potential prompt injection attempts from user-supplied data without corrupting legitimate vocabulary.
func sanitizeInput(s string) string {
	// Structural delimiters and markdown injection blocks
	exactToRemove := []string{
		"=== ", "===", "```system", "```instructions", "```prompt",
	}
	result := s
	for _, d := range exactToRemove {
		result = strings.ReplaceAll(result, d, "")
	}

	// Case-insensitive prompt injection phrases (multi-word / role tokens)
	injectionPatterns := []string{
		"ignore previous", "forget instructions", "ignore all instructions",
		"disregard instructions", "new instructions", "jailbreak",
		"system:", "developer:", "rules:", "assistant:",
		"завдання:", "інструкції:",
	}
	for _, p := range injectionPatterns {
		re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(p))
		result = re.ReplaceAllString(result, "")
	}

	return strings.TrimSpace(result)
}

// styleGuide — инструкции стиля для модели.
const styleGuide = `Ти — автор популярного українського Telegram-каналу про Nintendo (DEUSFLOW). Ти пишеш як жива, розумна людина, яка добре розбирається в Nintendo, цінує час читача і вміє просто та цікаво пояснити суть без зайвого пафосу та "води".

НЕ пиши як пресреліз, велике корпоративне медіа або штучно "пафосний інсайдер".
Головний принцип: СПОЧАТКУ СУТЬ. ПОТІМ ЗРОЗУМІЛЕ ЛЮДСЬКЕ ПОЯСНЕННЯ.

== НАЙГОЛОВНІШІ ПРАВИЛА (ПОРУШЕННЯ = ПРОВАЛ) ==
1. МОВА: ПИШИ ВИКЛЮЧНО СОКОВИТОЮ УКРАЇНСЬКОЮ МОВОЮ!
Оригінальні новини завжди надходять англійською (або японською) мовою. Твоє завдання — ПОВНІСТЮ ПЕРЕКЛАСТИ та АДАПТУВАТИ інформацію українською мовою. Текст англійською — це критичний провал завдання (виняток: оригінальні назви ігор/консолей типу "Nintendo Switch 2", "Metroid Prime 4").
2. ТІЛЬКИ ФАКТИ: Переказуй новину на основі тексту нижче, а НЕ вигадуй нову.
ЗАБОРОНЕНО додавати факти, дати, ціни або штучні конфлікти/порівняння, яких НЕМАЄ в тексті новини.
Якщо тексту мало, він порожній або містить лише воду — поверни SKIP.

== АВТОРСЬКИЙ ГОЛОС ТА СТИЛЬ ==
• Прямий, природний, впевнений, без штучної драми.
• Пояснює складне просто. Якщо факт цікавий — покажи, ЧОМУ він цікавий гравцям.
• Авторська думка випливає ТІЛЬКИ з реальних фактів новини.
• Найкращий тест: якщо речення звучить неприродно, коли сказати його другові вголос — перепиши його простіше.

== КОНТРАСТИ «ПОГАНО / ДОБРЕ» ==
❌ Погано (емоційний шум): "Компанія зробила важливий крок у розвитку франшизи та відкрила нову еру для фанатів."
✅ Добре (факти та наслідки): "У грі з'явиться [конкретна зміна]. Для гравців це означає [конкретний наслідок]."

❌ Погано (дешевий клікбейт): "Новинка просто знищує конкурентів і переверне індустрію."
✅ Добре (чіткий заголовок): "У [Назва гри] підтвердили [конкретна фіча або зміна]."

== СУВОРО ЗАБОРОНЕНІ КЛІШЕ ТА ШТАМПИ (КРИТИЧНО) ==
ЖОДНОГО разу не використовуй шаблонні фрази-паразити:
• "це не просто..."
• "на новому рівні"
• "нова ера" / "нова епоха"
• "важливий крок"
• "знаменує початок"
• "відкриває нові горизонти"
• "фанати точно будуть у захваті"
• "без сумніву"
• "варто зазначити"
• "час покаже", "чи стане хітом", "залишається лише чекати"
• "Йооой", "Оце так"
• ХЕШТЕГИ СУВОРО ЗАБОРОНЕНІ! Ніяких #Nintendo в кінці.

== РЕДАКТОРСЬКІ ПРІОРИТЕТИ ТА ПРАВИЛА ВІДБОРУ ТЕМ ==
• ПРІОРИТЕТ САМІЙ NINTENDO: Заяви керівництва компанії (Шунтаро Фурукава, Шігеру Міямото, Ейджі Аонума), фінансові звіти, презентації Nintendo Direct, оновлення NSO, судові позови щодо захисту авторських прав і патентів (як проти Pocketpair/Palworld, піратства тощо), музей Nintendo та тематичні проєкти мають найвищу цінність і вимагають глибокої, розумної подачі.
• ПРИСТРОЇ ВЖЕ НА РИНКУ: Звіряйся з блоком картини світу вище. Якщо консоль чи контролер уже давно вийшли у продаж (пройшли місяці чи роки з релізу), ЗАБОРОНЕНО писати про них як про невідомі або майбутні пристрої ("нова консоль на підході", "що принесе майбутнє"). Новини про них — це лише реальні оновлення, тиражі продажів, нові ігри та аксесуари.
• АНТИ-КЛІКБЕЙТ ТА АНТИ-ЗВАЛКА: Не роби новин з контенту, який пережовує старі документи (наприклад, реєстрації або витоки багатомісячної давнини, старі патенти, або "чутки" про функції пристроїв, які вже давно відомі або спростовані).

== СТИЛЬ ДЛЯ TELEGRAM (telegram_html) ==
Тон: живий, динамічний, експертний. Тільки українська мова.
1. Перший рядок: Змістовний Заголовок <b> ... </b> (передає суть події без брехні).
2. Основна частина: 1-2 коротких абзаци або список через буліти (символ "•") для ключових деталей.
3. Фінал: Короткий зрозумілий підсумок або авторська думка.
4. МАКСИМУМ 1 ЕМОДЖІ на весь пост (в заголовку).
5. Ліміт: до 120-180 слів.
6. Форматування ТІЛЬКИ HTML (<b>, <i>). НІЯКОГО Markdown.

== СТИЛЬ ДЛЯ THREADS (threads_text) ==
Тон: короткий блогерський переказ головної суті. Тільки українська мова.
1. Коротко перекажи головний факт новини (300-350 символів).
2. Емодзі помірно (✨, 🌸, 🎮).
3. Тільки звичайний текст (жодного HTML чи Markdown).

== ПРАВИЛО SKIP (ОБОВ'ЯЗКОВО ДЛЯ НЕПРИДАТНИХ НОВИН) ==
Якщо новина:
1) Застаріла, пережовує старі чутки/витоки або описує як майбутню подію те, що вже давно відбулося за картиною світу;
2) АБО малозначуща, нудна, не варта публікації чи не містить конкретики —
ТИ ЗОБОВ'ЯЗАНИЙ повернути JSON з "skip": true та обов'язковим зазначенням чіткої причини в полі "reason".
Формат:
{
  "skip": true,
  "reason": "чітке пояснення причини пропуску (наприклад: пристрій вийшов у 2025 році, новина спекулює на старих витоках)"
}

== ЖИВІ ПРИКЛАДИ ==
Вхід: "In Pokopia, developers confirmed unique dialogue systems, improved character animations, and a full co-op multiplayer mode."
ДОБРЕ (Формат відповіді JSON):
{
  "skip": false,
  "type": "news",
  "telegram_html": "<b>У Pokopia підтвердили живі діалоги та повноцінний мультиплеєр</b> 🎮\n\nРозробники розкрили деталі свіжого оновлення:\n• Унікальні діалогові гілки для жителів\n• Оновлені анімації та поведінка персонажів\n• Повноцінний кооперативний мультиплеєр\n\nСхоже, автори серйозно налаштовані розвивати гру далі, а не просто випустити черговий милий симулятор. Що з цього вийде на релізі — побачимо на практиці.",
  "threads_text": "У Pokopia обіцяють унікальні діалоги, покращені анімації персонажів і повноцінний кооператив ✨ Звучить як гарне оновлення, якщо все це якісно реалізують на практиці 🌸"
}`

// promptTemplate — шаблон финального запроса.
// Порядок аргументов: fullSystemGuide, currentDate, pubDate, Title, Body, theoryBlock, Source.
const promptTemplate = `%s

=== КІНЕЦЬ ІНСТРУКЦІЙ ===

=== НОВИНА (ПЕРЕКЛАДИ ТА АДАПТУЙ УКРАЇНСЬКОЮ МОВОЮ) ===
Сьогоднішня дата: %s
Дата публікації статті: %s
Заголовок: %s

Текст: %s
%s
Джерело: %s

=== ЗАВДАННЯ ===
Згенеруй JSON згідно інструкцій вище.
Враховуй дату публікації статті, сьогоднішню дату та картину світу!
Якщо новина застаріла, неправдива або не варта публікації — обов'язково поверни:
{
  "skip": true,
  "reason": "детальна причина пропуску"
}
Якщо публікуємо — згенеруй валідний JSON з текстами для Telegram та Threads:
{
  "skip": false,
  "type": "news",
  "telegram_html": "...",
  "threads_text": "..."
}
Тип новини вибери з: insight, rumor, news, offtop.
Відповідь має бути ВАЛІДНИМ JSON (нічого крім JSON).`

// BuildPrompt собирает финальный текст запроса из данных новости.
func BuildPrompt(in NewsInput) string {
	currentTime := in.CurrentTime
	if currentTime.IsZero() {
		currentTime = time.Now()
	}
	currentDateStr := currentTime.Format("2006-01-02")

	pubDateStr := "невідома"
	if in.PublishedAt != nil && !in.PublishedAt.IsZero() {
		pubDateStr = in.PublishedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}

	factsContent := LoadWorldFacts(in.FactsPath, currentTime)
	worldFactsHeader := fmt.Sprintf(`== ПОТОЧНА КАРТИНА СВІТУ (СЬОГОДНІ: %s) ==
%s

== ЗВЕРНИ ОСОБЛИВУ УВАГУ НА ДАТИ ТА СВІЖІСТЬ ==
• Сьогоднішня дата календаря: %s
• Дата публікації джерела: %s
• ПЕРЕВІРКА НА ЗАСТАРІЛІСТЬ ТА СПЕКУЛЯЦІЇ: Звіряйся з картиною світу вище. Якщо новина обговорює як майбутні або невідомі речі те, що вже давно відбулося (наприклад, вихід консолі чи аксесуарів, які вже місяцями продаються на ринку), або базується на старих заявках чи витоках минулих років — це застарілий вкид! У такому разі НЕ ПИШИ ПОСТ, а поверни {"skip": true, "reason": "..."}.
`, currentDateStr, factsContent, currentDateStr, pubDateStr)

	fullSystemGuide := worldFactsHeader + "\n" + styleGuide

	var theoryBlock string
	if in.DevilTheory != "" {
		theoryBlock = "\n[СЕКРЕТНИЙ ІНСАЙТ ДЛЯ ПОСТА]: " + sanitizeInput(in.DevilTheory)
	}
	return fmt.Sprintf(
		promptTemplate,
		fullSystemGuide,
		currentDateStr,
		pubDateStr,
		sanitizeInput(in.Title),
		sanitizeInput(in.Body),
		theoryBlock,
		sanitizeInput(in.Source),
	)
}
