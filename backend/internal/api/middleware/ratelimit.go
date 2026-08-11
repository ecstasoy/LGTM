package middleware

import (
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// RateLimitConfig 单端点固定窗口限流参数。
//
// 模型从 PR-AL 的 token bucket 切到 fixed window：
//   - Window 时间窗口内允许至多 Max 次请求
//   - 用 Cache.Incr(key, Window) 计数，TTL 到自然清零
//   - 不是 token bucket 那么平滑（边界效应可能允许 2x 流量），但能跨实例共享
//
// 切到 cache 接口后，main 接 RedisCache 时限流计数自动跨实例共享；
// dev 走 MemoryCache 行为与原 sync.Map 等价。
type RateLimitConfig struct {
	// Name 作为 cache key 前缀，让不同路由组独立计数
	Name string
	// Window 时间窗口
	Window time.Duration
	// Max 窗口内最大请求数
	Max int64
}

// 默认配置：对昂贵端点（/review、/steer 烧 LLM）保守限频，
// 对读路径（/reviews / /reviews/:id）放宽，/health 不限。
// 这些 fixed-window 默认值仅对齐平均吞吐，不与旧 token-bucket 的突发恢复语义等价。
// 真部署阶段可从 env 注入；切 Redis 后跨实例共享。
var (
	// ExpensiveDefault 评审 + 引导：25 秒内最多 5 次（≈ 0.2 RPS）
	ExpensiveDefault = RateLimitConfig{Name: "exp", Window: 25 * time.Second, Max: 5}
	// ReadDefault 列表 + 详情：4 秒内最多 20 次（≈ 5 RPS）
	ReadDefault = RateLimitConfig{Name: "read", Window: 4 * time.Second, Max: 20}
)

// RateLimit 返回一个 per-IP fixed-window 限流中间件。
// 超限返 429 + Retry-After header（秒）。
//
// cache 参数：
//   - 非 nil：用 Cache.Incr 计数；切 Redis 后跨实例共享
//   - nil：返回 pass-through middleware + 启动 warn（测试 / 临时禁用限流场景）
//
// ClientIP 走 gin 的解析：优先 X-Forwarded-For 链中的客户端 IP，
// 必须配合 r.SetTrustedProxies 正确配置；裸跑时取 RemoteAddr。
func RateLimit(cache store.Cache, cfg RateLimitConfig) gin.HandlerFunc {
	if cfg.Window <= 0 {
		panic("RateLimit: Window must be > 0")
	}
	if cfg.Max <= 0 {
		panic("RateLimit: Max must be > 0")
	}
	if cache == nil {
		warnNoCache.Do(func() {
			slog.Warn("RateLimit: cache is nil, returning pass-through middleware (no enforcement)")
		})
		return func(c *gin.Context) { c.Next() }
	}
	retryAfter := max(int((cfg.Window+time.Second-1)/time.Second), 1)
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if ip == "" {
			c.Next()
			return
		}
		key := "ratelimit:" + cfg.Name + ":" + ip
		count, err := cache.Incr(c.Request.Context(), key, cfg.Window)
		if err != nil {
			// 缓存故障时 fail open（让请求过）：限流是软保护，不应因为 Redis 抖动把服务打挂
			slog.Warn("rate limit cache Incr failed; bypassing limit", "key", key, "err", err)
			c.Next()
			return
		}
		if count > cfg.Max {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "请求过于频繁，请稍后再试",
				"retry_after": retryAfter,
			})
			return
		}
		c.Next()
	}
}

// warnNoCache 进程内只 warn 一次，避免每次请求都打日志
var warnNoCache sync.Once
