-- +migrate Up
ALTER TABLE articles ADD COLUMN body_threads TEXT NOT NULL DEFAULT '';

-- +migrate Down
ALTER TABLE articles DROP COLUMN body_threads;
