-- Applied after the locale column is guaranteed to exist.
-- locale is part of both unique keys so a zh review and an en review of the same PR can coexist.
-- The drops remove the pre-i18n four-column indexes; CREATE ... IF NOT EXISTS keeps a rerun cheap.
DROP INDEX IF EXISTS idx_reviews_public_unique;
DROP INDEX IF EXISTS idx_reviews_user_unique;

CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_public_unique
    ON reviews(owner, repo, pr_number, head_sha, locale)
    WHERE user_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_user_unique
    ON reviews(user_id, owner, repo, pr_number, head_sha, locale)
    WHERE user_id IS NOT NULL;

-- /history listing index: ordered by (user_id, created_at DESC)
CREATE INDEX IF NOT EXISTS idx_reviews_user
    ON reviews(user_id, created_at DESC);
