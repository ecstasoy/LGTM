// Package store 持久化评审：按 head SHA 缓存避免重复花钱，同时撑 /history 页。
package store

import (
	"context"
	"encoding/json"
	"time"
)

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
	Locale    string          // review output language ("zh" | "en"); empty is stored as DefaultLocale
	Payload   json.RawMessage // 序列化的 review.Result(JSON)
	CreatedAt time.Time
}

// Store 缓存 + 历史，统一在一个接口里
type Store interface {
	// Get 按 (owner, repo, pr, headSHA, locale) 查缓存；locale 是缓存身份的一部分，
	// 空串按 DefaultLocale 处理。
	Get(ctx context.Context, owner, repo string, pr int, headSHA, locale string) (*Record, error)
	Put(ctx context.Context, r *Record) error
	List(ctx context.Context, userID *string, limit int) ([]*Record, error)
	GetByID(ctx context.Context, id string) (*Record, error)
	// Delete 按 ID 硬删；幂等（找不到不报错）
	Delete(ctx context.Context, id string) error
	// Ping 健康检查；ctx 带 timeout。SQLite 走 db.PingContext，Postgres 同理。
	Ping(ctx context.Context) error
}
