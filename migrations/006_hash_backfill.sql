-- Backfill legacy nullable hash fields to reduce NULL-related edge cases.
UPDATE articles
SET url_hash = md5(source_url)
WHERE url_hash IS NULL OR url_hash = '';

UPDATE articles
SET title_hash = md5(COALESCE(title_raw, ''))
WHERE title_hash IS NULL OR title_hash = '';


