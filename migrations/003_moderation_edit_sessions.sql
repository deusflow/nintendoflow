CREATE TABLE IF NOT EXISTS moderation_edit_sessions (
    chat_id            BIGINT NOT NULL,
    user_id            BIGINT NOT NULL,
    article_id         INT NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
    preview_message_id INT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (chat_id, user_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_moderation_edit_sessions_article_id
    ON moderation_edit_sessions(article_id);

