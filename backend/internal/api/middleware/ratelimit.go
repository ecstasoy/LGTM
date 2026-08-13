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

// CodeRateLimited is the stable machine-readable code for the 429 response below. It intentionally lives
// here rather than alongside api.errcode.go's constants: package api imports package middleware (router.go),
// so middleware importing api's codes back would be a cycle. Same code-alongside-error-string convention,
// separate list.
const CodeRateLimited = "rate_limited"

// RateLimitConfig is the fixed-window rate limit configuration for one endpoint group.
//
// The model moved from a token bucket to a fixed window:
// - at most Max requests are allowed within Window
// - counted via Cache.Incr(key, Window), reset naturally when the TTL expires
// - less smooth than a token bucket (boundary effects can allow 2x traffic), but it can be shared across instances
//
// Going through the cache interface means the counters are shared across instances as soon as main wires up RedisCache;
// in dev, MemoryCache behaves the same as the original sync.Map.
type RateLimitConfig struct {
	// Name is the cache key prefix, so route groups count independently
	Name string
	// Window is the time window
	Window time.Duration
	// Max is the maximum number of requests per window
	Max int64
}

// Defaults: conservative on expensive endpoints (/review and /steer burn LLM tokens),
// looser on read paths (/reviews / /reviews/:id), and /health is unlimited.
// These fixed-window defaults only match the old average throughput; they are not equivalent to the token bucket's burst-recovery semantics.
// For a real deployment they can come from env; on Redis they are shared across instances.
var (
	// ExpensiveDefault covers review + steer: at most 5 in 25 seconds (≈ 0.2 RPS)
	ExpensiveDefault = RateLimitConfig{Name: "exp", Window: 25 * time.Second, Max: 5}
	// ReadDefault covers list + detail: at most 20 in 4 seconds (≈ 5 RPS)
	ReadDefault = RateLimitConfig{Name: "read", Window: 4 * time.Second, Max: 20}
)

// RateLimit returns a per-IP fixed-window rate limit middleware.
// Over the limit it returns 429 + a Retry-After header (in seconds).
//
// The cache argument:
// - non-nil: count with Cache.Incr; shared across instances once on Redis
// - nil: return pass-through middleware and warn at startup (tests / temporarily disabling the limiter)
//
// ClientIP uses gin's parsing: it prefers the client IP from the X-Forwarded-For chain,
// which requires r.SetTrustedProxies to be configured correctly; bare, it takes RemoteAddr.
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
			// fail open on a cache failure (let the request through): rate limiting is soft protection and should not take the service down because Redis hiccuped
			slog.Warn("rate limit cache Incr failed; bypassing limit", "key", key, "err", err)
			c.Next()
			return
		}
		if count > cfg.Max {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "请求过于频繁，请稍后再试",
				"code":        CodeRateLimited,
				"retry_after": retryAfter,
			})
			return
		}
		c.Next()
	}
}

// warnNoCache warns once per process rather than logging on every request
var warnNoCache sync.Once
