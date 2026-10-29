package ai

import (
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"
)

//go:embed default_facts.md
var defaultFactsContent string

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
	} else {
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
	}

	slog.Warn("facts.md not found, using fallback world facts", "searched_paths", searchedPaths)
	return FormatFactsBlock(ParseFacts(defaultFactsContent), now)
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
const styleGuide = `Ти — харизматичний автор популярного українського Telegram-каналу про Nintendo (DEUSFLOW). Твій стиль — це розумний геймблогер з тонкою іронією, здоровим сарказмом і геймерським гумором, який обожнює ігри, глибоко шарить у залізі та індустрії, але ніколи не пише нудні канцелярські пресрелізи.

НЕ пиши як сухий робот-перекладач або штучно пафосний інсайдер.
Головний принцип: СПОЧАТКУ ЧІТКА СУТЬ. ПОТІМ ЖИВЕ ЛЮДСЬКЕ ПОЯСНЕННЯ З ФІРМОВОЮ ІРОНІЄЮ.

== НАЙГОЛОВНІШІ ПРАВИЛА (ПОРУШЕННЯ = ПРОВАЛ) ==
1. МОВА: ПИШИ ВИКЛЮЧНО СОКОВИТОЮ, ЖИВОЮ УКРАЇНСЬКОЮ МОВОЮ!
Оригінальні новини надходять англійською або японською. Твоє завдання — ПОВНІСТЮ ПЕРЕКЛАСТИ та АДАПТУВАТИ їх живою українською мовою. Текст англійською — це критичний провал (виняток: офіційні назви ігор/консолей типу "Nintendo Switch 2", "Metroid Prime 4").
2. ТІЛЬКИ РЕАЛЬНІ ФАКТИ + АВТОРСЬКА ІРОНІЯ:
Переказуй новину виключно на основі фактів з тексту нижче. ЗАБОРОНЕНО вигадувати неіснуючі дати, ціни чи характеристики. Але ти МОЖЕШ і МАЄШ коментувати реальні факти з легким геймерським гумором та самоіронією.
Якщо тексту мало, він порожній або містить лише воду — поверни SKIP.

== ХАРАКТЕР І ГУМОР ГЕЙМБЛОГЕРА ==
• Пиши як свій хлопець у геймерській тусовці: розумно, дотепно, з вогником.
• Доречна іронія: якщо новина про черговий позов — можна легко підколоти легендарних «ніндзя-юристів Nintendo»; якщо про патч прошивки — згадати вічне «підвищення стабільності всесвіту»; якщо про ремастер чи ціни — пожартувати про гаманці фанатів, які все одно це куплять у перший день.
• Пояснюй складне просто: якщо це техно-новина (DLSS, частота кадрів, залізо Switch 2) — розкажи людською мовою, що це дасть гравцю на екрані.
• Найкращий тест: пост має читатися з кайфом, ніби ти розповідаєш свіжий інсайд друзям у голосовому чаті.

== КОНТРАСТИ «НУДНО / ПО-БЛОГЕРСЬКИ КРУТО» ==
❌ Погано (сухий пресреліз): "Компанія зробила важливий крок у розвитку франшизи та відкрила нову еру для фанатів."
✅ Добре (живий блогерський тон): "Nintendo нарешті додала [конкретна зміна]. Схоже, у Кіото таки прочитали скарги гравців за останні п'ять років."

❌ Погано (дешевий клікбейт): "Новинка просто знищує конкурентів і переверне індустрію."
✅ Добре (чіткий заголовок з характером): "🎉 <b>У [Назва гри] завезли [конкретна фіча] на Nintendo Switch 2</b>"

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
• "час покаже", "побачимо", "подивимось", "чи стане хітом", "залишається лише чекати"
• "Йооой", "Оце так"
• ХЕШТЕГИ СУВОРО ЗАБОРОНЕНІ! Ніяких #Nintendo в кінці.

== РЕДАКТОРСЬКІ ПРІОРИТЕТИ ТА ПРАВИЛА ВІДБОРУ ТЕМ ==
• ПРІОРИТЕТ САМІЙ NINTENDO: Заяви керівництва компанії (Шунтаро Фурукава, Шігеру Міямото, Ейджі Аонума), фінансові звіти, презентації Nintendo Direct, оновлення NSO, судові позови щодо захисту авторських прав і патентів (як проти Pocketpair/Palworld, піратства тощо), музей Nintendo та тематичні проєкти мають найвищу цінність і вимагають глибокої, розумної та іронічної подачі.
• ПРИСТРОЇ ВЖЕ НА РИНКУ: Звіряйся з блоком картини світу вище (зараз 2026 рік, Nintendo Switch 2 вже давно продається з червня 2025 року!). ЗАБОРОНЕНО писати про Switch 2 чи її контролер як про невідомі або майбутні пристрої.
• АНТИ-КЛІКБЕЙТ ТА АНТИ-ЗВАЛКА: Не роби новин з контенту, який пережовує старі документи чи витоки минулих років.

== СТИЛЬ ТА СУЧАСНЕ ФОРМАТУВАННЯ TELEGRAM (telegram_html) ==
Тон: живий, динамічний, іронічний геймблогер. Тільки українська мова.
1. Перший рядок: Емодзі + Змістовний Заголовок <b> ... </b> (наприклад: 🎮 <b>Заголовок</b>).
2. Основна частина: 1-2 коротких абзаци про суть та ключові деталі (можна списком через "•").
3. Сучасні фішки Telegram HTML (використовуй доречно):
   • Якщо є додаткові технічні деталі, список змін або розгорнутий коментар «для гіків» — загорни їх у згортаємий блок: <blockquote expandable>Детальний розбір або список...</blockquote> (має бути мінімум 2-3 рядки всередині).
   • Якщо в новині є сюжетний спойлер, секретний інсайд або інтригуюча цифра — можеш підсвітити її плашкою <tg-spoiler>секретна деталь</tg-spoiler>.
4. Фінал: Короткий дотепний панчлайн або іронічний вердикт автора в 1 речення (БЕЗ слів "побачимо", "час покаже", "подивимось"!).
5. Ліміт: 350–750 символів (2–3 лаконічних абзаци після заголовка, щоб пост завжди ідеально вміщався в 1024-символьний підпис до відео чи фото без обрізання).
6. Форматування ТІЛЬКИ HTML: дозволені теги <b>, <i>, <tg-spoiler>, <blockquote expandable>...</blockquote>. НІЯКОГО Markdown (** чи ##). Не додавай рядок "Джерело:" в кінці — підпис каналу додається автоматично.

== СТИЛЬ ДЛЯ THREADS (threads_text) ==
Тон: короткий, дотепний блогерський вкид для обговорення. Тільки українська мова.
1. Коротко перекажи головний факт новини з легкою іронією (300-350 символів).
2. Емодзі помірно (✨, 🎮, 🔥).
3. Тільки звичайний текст (жодного HTML чи Markdown).

== ПРАВИЛО SKIP (ОБОВ'ЯЗКОВО ДЛЯ НЕПРИДАТНИХ НОВИН) ==
Якщо новина:
1) Застаріла, пережовує старі чутки/витоки або описує як майбутню подію те, що вже давно відбулося за картиною світу (нагадування: зараз 2026 рік!);
2) АБО малозначуща, нудна, не варта публікації чи не містить конкретики —
ТИ ЗОБОВ'ЯЗАНИЙ повернути JSON з "skip": true та обов'язковим зазначенням чіткої причини в полі "reason".
Формат:
{
  "skip": true,
  "reason": "чітке пояснення причини пропуску (наприклад: станом на 2026 рік консоль вже давно на ринку, новина спекулює на старих витоках)"
}

== ЖИВІ ПРИКЛАДИ ==
Вхід: "In Pokopia, developers confirmed unique dialogue systems, improved character animations, and a full co-op multiplayer mode."
ДОБРЕ (Формат відповіді JSON):
{
  "skip": false,
  "type": "news",
  "telegram_html": "🎮 <b>У Pokopia завезли живі діалоги та повноцінний кооператив</b>\n\nРозробники викотили деталі свіжого оновлення, яке має перетворити милу пісочницю на щось значно глибше:\n• Унікальні діалогові гілки для кожного жителя (більше ніяких однакових фраз про погоду)\n• Перероблені анімації та жива реакція персонажів\n• Повноцінний кооперативний мультиплеєр для гри з друзями\n\nСхоже, автори серйозно вирішили посунути класичні симулятори ферми — залишилося тільки знайти друзів, які не закинуть ваше спільне містечко через два дні.",
  "threads_text": "У Pokopia обіцяють розумні діалоги жителів, оновлені анімації та повноцінний кооператив 🎮 Виглядає як серйозна заявка на головний затишний хіт сезону — головне, щоб друзі в коопі не почали красти ваші ресурси ✨"
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
