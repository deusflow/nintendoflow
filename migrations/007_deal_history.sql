CREATE TABLE IF NOT EXISTS deal_history (
    id           SERIAL PRIMARY KEY,
    deal_id      TEXT NOT NULL,
    title        TEXT NOT NULL,
    old_price    NUMERIC(10,2),
    new_price    NUMERIC(10,2),
    currency     TEXT,
    cut          INT NOT NULL DEFAULT 0,
    metacritic   INT NOT NULL DEFAULT 0,
    url          TEXT,
    source       TEXT NOT NULL DEFAULT 'CheapShark',
    reddit_quote TEXT,
    posted_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_deal_history_deal_id ON deal_history(deal_id);
CREATE INDEX IF NOT EXISTS idx_deal_history_posted_at ON deal_history(posted_at);
