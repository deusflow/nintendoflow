# ⚡ Quick Fix: Populate GitHub Pages with Real Data

## TL;DR

Ваш сайт работает, но показывает sample data. Нужно заполнить `docs/data.json` реальными статьями из БД.

---

## 🚀 Способ 1: Вручную через GitHub Actions (рекомендуется)

1. Откройте: https://github.com/YOUR_USERNAME/nintendoflow/actions
2. Левая панель → найдите **"Export Articles to GitHub Pages"**
3. Нажмите **"Run workflow"**  (dropdown in the right)
4. Выберите branch: **main**
5. Нажмите **"Run workflow"** (зелёная кнопка)
6. Дождитесь завершения (~2-3 минуты)
7. Обновите сайт (Ctrl+Shift+R)

**Готово!** Статьи загружены.

---

## 🔧 Способ 2: Локально (если выше не работает)

```bash
cd /Users/deuswork/GolandProjects/nintendoflow

# Убедитесь что .env содержит DATABASE_URL
cat .env | grep DATABASE_URL

# Запустите экспорт
go run ./cmd/export/main.go

# Проверьте результат
cat docs/data.json | head -20
```

Если подключение зависает, добавьте timeout:

```bash
PGCONNECT_TIMEOUT=30 go run ./cmd/export/main.go
```

После успеха:

```bash
git add docs/data.json
git commit -m "docs: populate articles from production database"
git push origin main
```

---

## 📊 Что будет после

**Сейчас:**
```json
{
  "articles": [
    { "id": 1, "title_ua": "Nintendo Switch 2 Офіційно Анонсована", ... },
    { "id": 2, "title_ua": "Elden Ring Port для Switch 2 Підтверджено", ... }
  ]
}
```

**Вместо этого:**
```json
{
  "articles": [
    { "id": 42, "title_ua": "Ваша реальная статья из БД", ... },
    { "id": 43, "title_ua": "Ещё одна ваша статья", ... }
  ]
}
```

---

## ✅ Как проверить что сработало

На сайте: https://yoursite.github.io/

1. Откройте DevTools (F12) → Console
2. Введите:
```javascript
fetch('./data.json').then(r => r.json()).then(d => console.log(`Articles: ${d.articles.length}`))
```

Должно вывести: `Articles: N` (где N — количество ваших статей)

---

## 🔄 Автоматизация на будущее

После первого экспорта, workflow будет запускаться автоматически:

- **По расписанию:** каждые 4 часа (6 раз в день)
- **Вручную:** любое время через GitHub Actions

`docs/data.json` будет обновляться автоматически при каждом запуске.

---

**Статус:** ✅ Сайт готов к работе с реальными данными!

Просто запустите экспорт одним из двух способов выше.

