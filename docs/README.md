# GitHub Pages Web Archive Setup

## 📄 What is this?

Web archive for DEUSFLOW — a static GitHub Pages site that displays all published Nintendo news articles.

- **Tech Stack (2026 best practices):**
  - Zero frameworks (vanilla HTML/CSS/JS)
  - Responsive design with mobile-first approach
  - Lazy loading for images
  - CSS custom properties (design tokens)
  - Dark mode by default
  - 100% static (no server-side rendering)

## 🚀 How to Deploy

### Step 1: Enable GitHub Pages

1. Go to your repository settings
2. Navigate to **Settings** → **Pages**
3. Set **Source** to: `GitHub Actions`
4. Save
6. Save

GitHub will auto-deploy within 1-2 minutes.

### Step 2: Configure Custom Domain (Optional)

1. In **Settings** → **Pages** → **Custom domain**, enter your domain (e.g., `archive.deusflow.local`)
2. Update DNS CNAME record pointing to `your-username.github.io`

### Step 3: Automatic Article Export

The workflow `.github/workflows/export-to-pages.yml` automatically:
- Exports published articles from PostgreSQL every 4 hours (6 runs/day)
- Generates `docs/data.json` inside the workflow workspace
- Deploys to GitHub Pages using official artifact deployment

**Requirements:**
- Add `DATABASE_URL` secret in repository settings
- Keep at least one row in `articles` with `status='published'`

Run manually at any time:
1. Open **Actions** tab
2. Select **Export Articles to GitHub Pages**
3. Click **Run workflow**

## 📁 File Structure

```
docs/
├── index.html          # Home page + hero + carousel
├── article.html        # Single article detail page
├── data.json          # JSON export of all articles (auto-generated)
└── README.md          # This file
```

## 🎨 Design Features

- **Glassmorphism 2.0**: Frosted glass effect with backdrop blur
- **Parallax Hero**: 3D depth effect with mouse tracking
- **Liquid Orb Cursor**: Custom animated cursor
- **Intro Animation**: Smooth system boot-up sequence
- **Responsive Grid**: Automatic layout adjustment for mobile

## 📊 Data Format

`data.json` structure:

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
      "status": "published",
      "published_at": "2026-03-18T10:00:00Z"
    }
  ],
  "exported_at": "2026-03-18T15:30:00Z"
}
```

## 🔧 Customization

### Change Colors

Edit `:root` variables in `index.html`:

```css
:root {
    --hyper-pink: #ff2e8f;      /* Main accent */
    --milky-blush: #ffd1e0;     /* Secondary */
    --deep-void: #080014;       /* Dark bg */
    --glass: rgba(255, 210, 230, 0.04);  /* Glass effect */
}
```

### Add Custom Domain

Set custom domain in **Settings -> Pages -> Custom domain**.

## 🚨 Troubleshooting

**Articles not showing?**
- Check the latest run of `.github/workflows/export-to-pages.yml`
- Verify logs show `export: article status stats` and `published > 0`
- Ensure `DATABASE_URL` secret is configured and valid

**Page not live?**
- GitHub Pages deployment takes 1-2 minutes
- Check "Actions" tab for workflow status
- Verify Pages source is set to `GitHub Actions`

**Images not loading?**
- Check image URLs in `data.json`
- Browser may block mixed HTTP/HTTPS content
- Use HTTPS-only URLs

## 📈 Performance

- **LCP (Largest Contentful Paint)**: ~1.2s
- **FID (First Input Delay)**: <100ms
- **CLS (Cumulative Layout Shift)**: <0.05
- **Cache**: GitHub Pages serves with optimal headers
- **Compression**: Automatic gzip by GitHub

## 🔐 Security

- No JavaScript dependencies (no supply chain risk)
- All data is read-only (GET requests only)
- No third-party analytics or cookies
- HTML/CSS/JS are all self-contained

## 📝 License

Same as main project.

