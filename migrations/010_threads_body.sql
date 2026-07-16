-- +migrate Up
ALTER TABLE articles ADD COLUMN IF NOT EXISTS body_threads TEXT NOT NULL DEFAULT '';
