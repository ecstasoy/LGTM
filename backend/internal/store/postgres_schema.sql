-- Postgres schema -- same shape as SQLite; differences: BYTEA / BIGINT.
-- v1 reviews table; adding users / comments / vector chunks in v2 will not break this.
-- Tables only: indexes live in postgres_indexes.sql because they must run after the locale backfill.

CREATE TABLE IF NOT EXISTS reviews (
    id          TEXT PRIMARY KEY,
    user_id     TEXT,                       -- nullable in v1; filled in after OAuth in v2
    owner       TEXT NOT NULL,
    repo        TEXT NOT NULL,
    pr_number   BIGINT NOT NULL,
    head_sha    TEXT NOT NULL,
    locale      TEXT NOT NULL DEFAULT 'zh', -- review output language, part of the cache identity
    payload     BYTEA NOT NULL,             -- serialized review.Result bytes
    created_at  BIGINT NOT NULL             -- Unix timestamp (nanoseconds)
);

-- Backfill for databases created before i18n; every pre-i18n review was Chinese.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS locale TEXT NOT NULL DEFAULT 'zh';
