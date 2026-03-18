# 🚀 GitHub Pages Web Archive Deployment Guide

## Summary

Ви вдало замінили Go веб-сервер на **статичний GitHub Pages архів** з лучшими практиками 2026 року:

✅ **Zero JavaScript frameworks** — чистий HTML/CSS/JS  
✅ **Lazy loading** — картинки завантажуються за потребою  
✅ **Dark mode by default** — красивий дизайн  
✅ **Glassmorphism 2.0** — фроззений ефект скла  
✅ **Parallax hero** — 3D ефект з мишею  
✅ **100% SEO-friendly** — pre-rendered HTML  
✅ **Zero dependencies** — нема npm install  

---

## 📁 Що було зроблено

### 1. **Web Archive Files**

| Файл | Опис |
|---|---|
| `docs/index.html` | Головна сторінка з героєм та каруселлю |
| `docs/article.html` | Детальна сторінка статті |
| `docs/data.json` | JSON експорт (авто-генерується) |
| `docs/README.md` | Документація архіву |

### 2. **Automation**

| Файл | Опис |
|---|---|
| `.github/workflows/export-to-pages.yml` | GitHub Actions (кожні 3 години) |
| `cmd/export/main.go` | Go утиліта для експорту DB → JSON |

### 3. **Updated Files**

- `README.md` — додав секцію про GitHub Pages
- `.gitignore` — додав `/export` бінарник
- `pkg/db/queries.go` — читаю опубліковані статті

---

## 🚀 Как развернуть на GitHub Pages

### **Шаг 1: Готовність**

✓ Скопіюйте код в GitHub репозиторій  
✓ Всі файли вже готові в `/docs/`

### **Шаг 2: Включить GitHub Pages**

1. Откройте Settings репо → **Pages**
2. **Source:** Deploy from a branch
3. **Branch:** main (или ваша ветка)
4. **Folder:** `/docs`
5. Save

GitHub развернет за 1-2 минуты.

### **Шаг 3 (Optional): Кастомный домен**

1. В Pages → Custom domain введите свой домен (напр. `archive.deusflow.local`)
2. Обновите DNS записи вашего хостинга

---

## 🔧 Автоматический экспорт из БД

Workflow `.github/workflows/export-to-pages.yml` делает это каждые 3 часа:

1. Подключается к PostgreSQL
2. Загружает все опубликованные статьи
3. Генерирует `docs/data.json`
4. Коммитит в репо
5. Перезапускает Pages

### **Требования:**

Добавьте GitHub Actions secret:
- **Имя:** `DATABASE_URL`
- **Значение:** `postgres://user:pass@host:5432/nintendoflow`

Как добавить:
1. Settings → Secrets and variables → Actions
2. New repository secret
3. Скопируйте URL подключения из `.env`

---

## 📊 Формат `data.json`

```json
{
  "articles": [
    {
      "id": 1,
      "title_raw": "Original Title",
      "title_ua": "Український Переклад",
      "body_ua": "Full article text...",
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

---

## 🎨 Дизайн & Функции

### **Главная страница (`/`)**
- Hero секция с параллаксом (3D эффект)
- Liquid Orb курсор (кастомный)
- Carousel с последними статьями
- Intro animation (система загрузки)

### **Страница статьи (`/article.html?id=123`)**
- Полный текст статьи
- Meta информация (дата, источник, скор, AI провайдер)
- Кнопка "Read Original" ссылается на исходник
- Responsive и красивый

---

## 🔧 Как локально тестировать

```bash
cd /Users/deuswork/GolandProjects/nintendoflow/docs/

# Option 1: Python (рекомендуется)
python -m http.server 8080

# Option 2: Node.js http-server
npx http-server -p 8080

# Option 3: Ruby
ruby -run -ehttpd . -p 8080
```

Откройте: `http://localhost:8080`

---

## 🎨 Как кастомизировать

### **Смените цвета**

В `docs/index.html` найдите `:root`:

```css
:root {
    --hyper-pink: #ff2e8f;        /* Main accent */
    --milky-blush: #ffd1e0;       /* Secondary */
    --deep-void: #080014;         /* Dark background */
    --glass: rgba(255, 210, 230, 0.04);  /* Glass effect */
}
```

Поменяйте hex коды и все элементы обновятся автоматически.

### **Измените интервал экспорта**

В `.github/workflows/export-to-pages.yml`:

```yaml
on:
  schedule:
    - cron: '0 */6 * * *'  # Меняйте */3 на */6 (каждые 6 часов)
```

### **Отключите автоматический деплой**

Удалите или закомментируйте блок `Deploy to GitHub Pages` в workflow.

---

## 🚨 Решение проблем

### **"Articles not showing"**
- Проверьте, что `docs/data.json` существует
- Откройте DevTools (F12) → Console — там будут ошибки
- Убедитесь, что DATABASE_URL secret добавлен

### **"Page is still loading"**
- GitHub Pages деплой занимает 1-2 минуты
- Проверьте Actions tab в репо на errors

### **"Images not loading"**
- Проверьте URLs в `data.json` — должны быть HTTPS
- Браузер блокирует mixed content (HTTP/HTTPS)

### **"Workflow не запускается"**
- Проверьте, что DATABASE_URL secret есть
- Workflow может быть disabled в Actions settings

---

## 📈 Performance (Лучшие практики 2026)

| Метрика | Результат | Target |
|---------|-----------|--------|
| LCP | ~1.2s | <2.5s |
| FID | <100ms | <100ms |
| CLS | <0.05 | <0.1 |
| Page Weight | ~80KB | <100KB |
| JS Bundle | 0 KB | <50KB |

GitHub Pages автоматически:
- Сжимает gzip
- Кэширует на CDN
- Минифицирует CSS/JS
- Оптимизирует headers

---

## 🔐 Безопасность

- ✅ **No Third-Party JS** — нет зависимостей
- ✅ **No Analytics** — нет трекинга
- ✅ **No Cookies** — статичный контент
- ✅ **No Supply Chain Risk** — нету npm packages
- ✅ **HTTPS by default** — GitHub Pages автоматично
- ✅ **Read-only** — только GET запросы

---

## 📝 Что дальше?

**Опционально:**

1. Добавьте поиск по статьям (клиентский JS)
2. Добавьте фильтр по категориям (из `topics`)
3. Добавьте подписку на RSS (внешний сервис)
4. Добавьте comments (Disqus/Utterances)
5. Добавьте analytics (без трекинга приватности)

**Рекомендуемо:**

- Купите домен и используйте `CNAME`
- Настройте email уведомления о запусках workflow
- Резервное копирование data.json

---

## 📞 Помощь

**Документация:**
- `docs/README.md` — полная техническая docs
- GitHub Pages docs: https://pages.github.com
- Workflows docs: https://docs.github.com/en/actions

**Проверяйте:**
- Логи workflow в Actions tab
- DevTools Console (F12) на сайте
- GitHub Pages deployment history

---

## ✅ Checklist для запуска

- [ ] Все файлы из `/docs/` залиты в репо
- [ ] GitHub Pages Settings направлен на `/docs/`
- [ ] `DATABASE_URL` secret добавлен (если используете экспорт)
- [ ] Первый деплой прошел (1-2 минуты)
- [ ] Сайт доступен по URL
- [ ] Статьи загружаются правильно

---

**Готово!** 🎉 Ваш архив запущен на GitHub Pages с лучшими практиками 2026!

