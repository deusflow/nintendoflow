-- +migrate Up
ALTER TABLE articles ADD COLUMN posted_threads BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE articles ADD COLUMN tg_message_id INTEGER NOT NULL DEFAULT 0;

-- +migrate Down
ALTER TABLE articles DROP COLUMN posted_threads;
ALTER TABLE articles DROP COLUMN tg_message_id;
