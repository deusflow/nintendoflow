# 🔄 Real-Time Data Export Setup Guide

## Ситуация

✅ Сайт запустился на GitHub Pages  
✅ `DATABASE_URL` уже в GitHub Actions secrets  
✅ Таблица `articles` со статусом `'published'` уже существует  
⚠️ Но `docs/data.json` содержит sample data (заглушки)

**Проблема:** GitHub Actions workflow ещё не запустился или пытается подключиться к БД и зависает на timeout.

---

## 📋 Что нужно сделать

### **Option 1: Дождаться автоматического экспорта (рекомендуется)**

Workflow запустится по расписанию:
```
06:00, 10:00, 14:00, 18:00, 22:00, 02:00 UTC
```

**Или** запустите вручную:
1. Откройте ваш GitHub репо
2. Перейдите в **Actions** tab
3. Найдите workflow **"Export Articles to GitHub Pages"**
4. Нажмите **"Run workflow"** → **Run workflow** (green button)
5. Дождитесь завершения (~2-3 минуты)
6. Проверьте что `docs/data.json` обновился в main ветке

---

### **Option 2: Запустить экспорт локально и закоммитить (быстрее)**

На вашей машине:

```bash
cd /Users/deuswork/GolandProjects/nintendoflow

# Убедитесь что DATABASE_URL в .env
source .env

# Запустите экспорт
go run ./cmd/export/main.go

# Проверьте что файл обновился
cat docs/data.json | head -30

# Коммитьте и пушьте
git add docs/data.json
git commit -m "docs: populate articles from production database"
git push origin main
```

GitHub Pages автоматически переразворачивает при каждом push в `/docs/`.

---

## 🐛 Если экспорт зависает

**Проблема:** Neon DB может быть медленным или требует более длинный timeout.

**Решение:** Модифицируйте `cmd/export/main.go` временно для диагностики:

```go
// Увеличьте timeout с 30s на 60s
ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
defer cancel()
```

Или добавьте переменную окружения перед экспортом:

```bash
PGCONNECT_TIMEOUT=30 go run ./cmd/export/main.go
```

---

## 📊 Структура data.json

После экспорта, `docs/data.json` должна выглядеть так:

```json
{
  "articles": [
    {
      "id": 123,
      "title_raw": "Оригинальный заголовок",
      "title_ua": "Українська версія",
      "body_ua": "Повний текст статті...",
      "image_url": "https://...",
      "source_name": "VGC",
      "source_url": "https://...",
      "score": 92,
      "ai_provider": "gemini",
      "status": "published",
      "published_at": "2026-03-18T10:00:00Z"
    }
  ],
  "exported_at": "2026-03-18T15:30:00Z"
}
```

**Важно:** 
- Только статьи со `status='published'` попадают в экспорт
- `title_ua` и `body_ua` должны быть заполнены (если нет — используется `title_raw`)
- `image_url` может быть пуст (fallback placeholder в HTML)

---

## 🔍 Как проверить что экспорт сработал

### **На GitHub**

1. Settings → Pages → Deployments
2. Должен быть последний deployment с timestamp
3. Откройте сайт

### **В коде**

```bash
# Локально
cd /Users/deuswork/GolandProjects/nintendoflow
cat docs/data.json | python -m json.tool | head -50

# Проверьте что статей больше чем 1
cat docs/data.json | grep '"id"' | wc -l
```

### **На сайте**

1. Откройте http://yoursite/
2. Откройте DevTools (F12) → Console
3. Введите: `fetch('./data.json').then(r => r.json()).then(d => console.log(`Loaded ${d.articles.length} articles`))`
4. Должно вывести: `Loaded 42 articles` (или сколько у вас есть)

---

## 🚀 Автоматизация на будущее

### **Workflow запускается в эти времена (UTC):**
```
Пн-Вс: 02:00, 06:00, 10:00, 14:00, 18:00, 22:00
```

Все 6 запусков в день.

### **Или запустите вручную:**

GitHub Actions → Export Articles to GitHub Pages → Run workflow

---

## 🛠️ Если всё ещё не работает

### **Checklist**

- [ ] DATABASE_URL secret добавлен в GitHub Settings → Secrets and variables
- [ ] DATABASE_URL значение — это полный `postgres://...` URL
- [ ] Таблица `articles` существует в БД
- [ ] Есть хотя бы одна статья со `status='published'`
- [ ] `/docs/` папка залита в репо
- [ ] GitHub Pages Settings → Source: `/docs` folder

### **Логи для диагностики**

1. Откройте GitHub Actions tab
2. Кликните на последний run экспорта
3. Кликните на "Export articles to JSON" step
4. Посмотрите логи — там будут ошибки подключения или query

---

## 📝 Что произойдёт после экспорта

```
1. cmd/export/main.go запускается
   ↓
2. Подключается к PostgreSQL
   ↓
3. Читает: SELECT * FROM articles WHERE status='published'
   ↓
4. Генерирует JSON файл
   ↓
5. Пишет в docs/data.json
   ↓
6. Коммитит и пушит в main ветку
   ↓
7. GitHub Pages автоматически переразворачивает
   ↓
8. index.html и article.html загружают данные из data.json
   ↓
9. Статьи отображаются на сайте
```

---

## 🎯 Практический пример

Если у вас в БД есть 5 статей:

```
articles table:
├─ id=1, title_ua="Zelda Release", status='published'
├─ id=2, title_ua="Switch 2 Specs", status='published'
├─ id=3, title_ua="Mario News", status='published'
├─ id=4, title_ua="Draft Article", status='pending'        ← НЕ экспортируется
└─ id=5, title_ua="Rejected", status='rejected'           ← НЕ экспортируется
```

**data.json будет содержать только 3 статьи** (с id 1, 2, 3)

---

## 💡 Tips

- Если экспорт медленный, увеличьте `context.WithTimeout` до 60 сек
- Если не может подключиться, проверьте что DATABASE_URL не истёк (Neon DB может требовать refresh)
- Если workflow always fails, добавьте  `PGCONNECT_TIMEOUT=30` в workflow
- Можно запустить `go run ./cmd/export/main.go` локально для быстрого теста

---

**Решение:** Запустите workflow вручную в GitHub Actions или выполните команду локально и закоммитьте.

Оба варианта работают!

