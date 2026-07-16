-- migrations/011_moderation_edit_target.sql
ALTER TABLE moderation_edit_sessions
ADD COLUMN IF NOT EXISTS edit_target VARCHAR(10) NOT NULL DEFAULT 'tg';
