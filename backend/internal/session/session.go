// Package session per-user 登录态：HttpOnly cookie 存 session ID，详情存 Cache（Redis）
//
// 设计：
//   - Session ID 32 byte 随机 → base64 → 放 cookie（不可猜 + 透明给 handler）
//   - Cache 存 Session struct (JSON)，TTL 默认 30 天
//   - 用户登出 → Cache.Delete + cookie 清空
//   - 没 Cache（test / 误配）→ Manager 直接退化 in-memory（仅单实例 dev 用）
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ecstasoy/LGTM/backend/internal/store"
)

const (
	// DefaultTTL 默认 session 寿命 30 天
	DefaultTTL = 30 * 24 * time.Hour

	// CookieName 跨前后端约定；前端不直读，只浏览器存
	CookieName = "lgtm_session"

	// keyPrefix Cache key 前缀，避免和 rate limit 等冲突
	keyPrefix = "session:"
)

// Session 一次登录持有的数据；JSON 序列化进 Cache
// AccessToken 是 GitHub user-to-server token，权限 = GitHub App permissions
// 不暴露给前端；只在后端调 GitHub API 时取出
type Session struct {
	UserID      int64     `json:"user_id"`
	Login       string    `json:"login"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
	Name        string    `json:"name,omitempty"`
	AccessToken string    `json:"access_token"`
	CreatedAt   time.Time `json:"created_at"`
}

// Manager session 读写；线程安全
// Cache 故障时所有操作返 err，handler 应当拒绝登录但允许已登录用户继续（fail-soft 由 caller 决定）
type Manager struct {
	cache store.Cache
	ttl   time.Duration

	// inMemoryFallback 仅在 cache==nil 时启用；单实例 dev / 单测用
	mu      sync.RWMutex
	mem     map[string]Session
}

// New 构造 Manager；cache=nil 自动 fallback 内存（带 warn 用法见 main 接线）
// ttl=0 用 DefaultTTL
func New(cache store.Cache, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	m := &Manager{cache: cache, ttl: ttl}
	if cache == nil {
		m.mem = make(map[string]Session)
	}
	return m
}

// TTL 返回当前 session TTL；handler 设 cookie max-age 用
func (m *Manager) TTL() time.Duration { return m.ttl }

// Create 生成新 session ID + 写入；返回 ID 让 handler 塞 cookie
func (m *Manager) Create(ctx context.Context, s Session) (string, error) {
	if s.AccessToken == "" {
		return "", errors.New("session: empty access token")
	}
	if s.UserID == 0 {
		return "", errors.New("session: empty user id")
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	if err := m.put(ctx, id, s); err != nil {
		return "", err
	}
	return id, nil
}

// Get 按 ID 取；找不到返 (nil, nil)，仅底层故障返 err
func (m *Manager) Get(ctx context.Context, id string) (*Session, error) {
	if id == "" {
		return nil, nil
	}
	if m.cache == nil {
		m.mu.RLock()
		defer m.mu.RUnlock()
		s, ok := m.mem[id]
		if !ok {
			return nil, nil
		}
		return &s, nil
	}
	raw, ok, err := m.cache.Get(ctx, keyPrefix+id)
	if err != nil {
		return nil, fmt.Errorf("session get: %w", err)
	}
	if !ok {
		return nil, nil
	}
	var s Session
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("session unmarshal: %w", err)
	}
	return &s, nil
}

// Delete 注销；幂等，不存在也不报错
func (m *Manager) Delete(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	if m.cache == nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.mem, id)
		return nil
	}
	return m.cache.Delete(ctx, keyPrefix+id)
}

func (m *Manager) put(ctx context.Context, id string, s Session) error {
	if m.cache == nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.mem[id] = s
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("session marshal: %w", err)
	}
	return m.cache.Set(ctx, keyPrefix+id, raw, m.ttl)
}

// newID 32 byte 随机 → url-safe base64；可读性 ~43 字符
func newID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session: rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
