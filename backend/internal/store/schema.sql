-- v1 schema; adding users / comments tables in v2 will not break this.
-- Tables only: indexes live in indexes.sql because they must run after the locale backfill.

CREATE TABLE IF NOT EXISTS reviews (
    id          TEXT PRIMARY KEY,
    user_id     TEXT,                       -- nullable in v1; filled in after OAuth in v2
    owner       TEXT NOT NULL,
    repo        TEXT NOT NULL,
    pr_number   INTEGER NOT NULL,
    head_sha    TEXT NOT NULL,
    locale      TEXT NOT NULL DEFAULT 'zh', -- review output language, part of the cache identity
    payload     BLOB NOT NULL,              -- serialized review.Result bytes
    created_at  INTEGER NOT NULL            -- Unix timestamp (nanoseconds)
);
