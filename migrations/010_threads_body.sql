-- +migrate Up
ALTER TABLE articles ADD COLUMN IF NOT EXISTS body_threads TEXT NOT NULL DEFAULT '';

-- +migrate Down
ALTER TABLE articles DROP COLUMN IF EXISTS body_threads;
