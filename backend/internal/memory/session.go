// Package memory 会话记忆：按 review_id 维度保留 agent 追问历史。
// 设计：每个 review 一个对话室；只存 user/assistant 纯文本（不含 tool observation）；
// 滑动窗口（默认 cap=10 turn）；TTL（默认 7 天）；底层走 store.Cache 抽象不绑 Redis。
//
// 不存 tool observation 的原因：当 agent 需要时会重新调工具，cache miss 代价是多一次工具调用，
// 但内存成本下降 5-10 倍且 prompt 不易爆 context。
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ecstasoy/LGTM/backend/internal/store"
)

const (
	// DefaultMaxTurns 默认滑窗保留 turn 数；10 轮够 demo 多次追问且 token 占用 < 15K
	DefaultMaxTurns = 10
	// DefaultTTL 默认会话过期；覆盖 demo + review 周期，不会无限堆积
	DefaultTTL = 7 * 24 * time.Hour
	// keyPrefix store key 前缀；与 session.* / rate-limit.* 分开命名空间
	keyPrefix = "memory:session:"
)

// Turn 一轮对话：用户输入 + agent 最终文本输出。
// 不存 system prompt / tool calls / observations（每次重组，避免重复计 token）。
type Turn struct {
	UserText  string    `json:"u"`
	AgentText string    `json:"a"`
	CreatedAt time.Time `json:"t"`
	Steps     int       `json:"s,omitempty"` // agent 用了几步（debug 链路）
}

// SessionStore 会话记忆抽象；nil-receiver 调用方应在 Deps.Memory==nil 时跳过 load/save。
type SessionStore interface {
	// Append 追加一轮；超 maxTurns 时丢最旧的；同时刷新 TTL。
	// 任何错误（cache 抖动 / 序列化失败）只该 warn 不该挂请求——memory 是增强不是依赖。
	Append(ctx context.Context, reviewID string, turn Turn) error
	// Get 取整段历史，时间序（最旧在前）。空 / 不存在 / 解析失败 都返 nil, nil（fail-soft）。
	Get(ctx context.Context, reviewID string) ([]Turn, error)
	// Reset 立即清除该 review_id 的会话；删评审 / 用户主动重置时调。
	Reset(ctx context.Context, reviewID string) error
}

// CacheSessionStore 基于 store.Cache 的实现。
// 生产注入 RedisCache（跨实例 + 跨 Fly redeploy 持久），dev / test 注入 MemoryCache。
type CacheSessionStore struct {
	cache    store.Cache
	maxTurns int
	ttl      time.Duration
}

// NewCacheSessionStore maxTurns / ttl <= 0 时套默认。cache 必须非 nil。
func NewCacheSessionStore(cache store.Cache, maxTurns int, ttl time.Duration) *CacheSessionStore {
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &CacheSessionStore{cache: cache, maxTurns: maxTurns, ttl: ttl}
}

func key(reviewID string) string { return keyPrefix + reviewID }

// Get 解析失败时（旧版本数据 / 损坏）按空历史处理；不抛错给调用方避免拖累 agent 主路径。
func (s *CacheSessionStore) Get(ctx context.Context, reviewID string) ([]Turn, error) {
	if s == nil || s.cache == nil || reviewID == "" {
		return nil, nil
	}
	raw, ok, err := s.cache.Get(ctx, key(reviewID))
	if err != nil {
		return nil, fmt.Errorf("memory get: %w", err)
	}
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var turns []Turn
	if uerr := json.Unmarshal(raw, &turns); uerr != nil {
		// 旧数据损坏 → 当作空历史；下次 Append 会覆盖整个 key
		return nil, nil
	}
	return turns, nil
}

// Append 读-改-写：取现有 → append → 截前 maxTurns → 序列化 → Set + 刷 TTL。
// 并发追加在 cache 抽象层无原子保证；demo 体量并发低，可接受 last-write-wins 丢一轮。
// 真要避免需要切到 Redis Lua 脚本或在调用方加 review-id 粒度锁；本阶段不做。
func (s *CacheSessionStore) Append(ctx context.Context, reviewID string, turn Turn) error {
	if s == nil || s.cache == nil || reviewID == "" {
		return nil
	}
	existing, _ := s.Get(ctx, reviewID) // 错误已 fail-soft 为 nil
	updated := append(existing, turn)
	if len(updated) > s.maxTurns {
		updated = updated[len(updated)-s.maxTurns:]
	}
	raw, err := json.Marshal(updated)
	if err != nil {
		return fmt.Errorf("memory marshal: %w", err)
	}
	if err := s.cache.Set(ctx, key(reviewID), raw, s.ttl); err != nil {
		return fmt.Errorf("memory set: %w", err)
	}
	return nil
}

// Reset 删除该 review 的全部会话；DELETE /api/reviews/:id 时联动调用。
func (s *CacheSessionStore) Reset(ctx context.Context, reviewID string) error {
	if s == nil || s.cache == nil || reviewID == "" {
		return nil
	}
	if err := s.cache.Delete(ctx, key(reviewID)); err != nil {
		return fmt.Errorf("memory delete: %w", err)
	}
	return nil
}
