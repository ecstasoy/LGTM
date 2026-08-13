// Package store 持久化评审：按 head SHA 缓存避免重复花钱，同时撑 /history 页。
package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// reviewIndexNames are the indexes both engines guarantee on the reviews table.
var (
	reviewUniqueIndexNames = []string{"idx_reviews_public_unique", "idx_reviews_user_unique"}
	reviewListIndexName    = "idx_reviews_user"
)

// reviewIndexesCarryLocale reports whether the live index definitions already key on locale.
// defs maps index name to its CREATE statement, as reported by sqlite_master.sql / pg_indexes.indexdef.
// False means the index DDL has to be replayed; true means a startup can skip it and touch no schema at all.
func reviewIndexesCarryLocale(defs map[string]string) bool {
	for _, name := range reviewUniqueIndexNames {
		ddl, ok := defs[name]
		if !ok || !strings.Contains(ddl, "locale") {
			return false
		}
	}
	_, ok := defs[reviewListIndexName]
	return ok
}

// DefaultLocale is the locale assumed for reviews written before i18n; every one of them was Chinese.
const DefaultLocale = "zh"

// normalizeLocale maps an empty locale to the pre-i18n default so old callers keep their behaviour.
func normalizeLocale(locale string) string {
	if locale == "" {
		return DefaultLocale
	}
	return locale
}

// Record 一条缓存评审
type Record struct {
	ID        string  // ulid
	UserID    *string // v1 永远 nil；v2 OAuth 后填
	Owner     string
	Repo      string
	PRNumber  int
	HeadSHA   string
	Locale    string          // review output language; the "zh" | "en" domain is enforced upstream at the request boundary, empty is stored as DefaultLocale
	Payload   json.RawMessage // 序列化的 review.Result(JSON)
	CreatedAt time.Time
}

// Store 缓存 + 历史，统一在一个接口里
type Store interface {
	// Get looks the cache up by (owner, repo, pr, headSHA, locale); an empty locale means DefaultLocale.
	Get(ctx context.Context, owner, repo string, pr int, headSHA, locale string) (*Record, error)
	Put(ctx context.Context, r *Record) error
	List(ctx context.Context, userID *string, limit int) ([]*Record, error)
	GetByID(ctx context.Context, id string) (*Record, error)
	// Delete 按 ID 硬删；幂等（找不到不报错）
	Delete(ctx context.Context, id string) error
	// Ping 健康检查；ctx 带 timeout。SQLite 走 db.PingContext，Postgres 同理。
	Ping(ctx context.Context) error
}
