package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/store"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestCache(t *testing.T) store.Cache {
	t.Helper()
	c := store.NewMemoryCache(time.Hour) // 测试期内不需要后台扫
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// 用极小 Max 验证 fixed window 真的拦截
func TestRateLimit_AllowsBurstThenBlocks(t *testing.T) {
	r := gin.New()
	cache := newTestCache(t)
	r.Use(RateLimit(cache, RateLimitConfig{Name: "test", Window: time.Hour, Max: 2}))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	srv := httptest.NewServer(r)
	defer srv.Close()

	// 同一 IP 先打两个，应放行；第三个应 429
	for i := range 2 {
		res, err := http.Get(srv.URL + "/x")
		if err != nil {
			t.Fatalf("http.Get failed: %v", err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("burst #%d should pass, got %d", i, res.StatusCode)
		}
	}
	res, err := http.Get(srv.URL + "/x")
	if err != nil {
		t.Fatalf("http.Get failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 429 {
		t.Errorf("after burst should be 429, got %d", res.StatusCode)
	}
	if ra := res.Header.Get("Retry-After"); ra == "" {
		t.Errorf("missing Retry-After header")
	} else if n, _ := strconv.Atoi(ra); n < 1 {
		t.Errorf("Retry-After should be >=1, got %s", ra)
	}
}

// 不同 IP 应各自独立计数
func TestRateLimit_PerIPIsolation(t *testing.T) {
	r := gin.New()
	cache := newTestCache(t)
	r.Use(RateLimit(cache, RateLimitConfig{Name: "test", Window: time.Hour, Max: 1}))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	doReq := func(ip string) int {
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = ip + ":1234"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := doReq("10.0.0.1"); got != 200 {
		t.Fatalf("ip A first call should be 200, got %d", got)
	}
	if got := doReq("10.0.0.1"); got != 429 {
		t.Fatalf("ip A second call should be 429, got %d", got)
	}
	// 不同 IP 的第一次仍应放行
	if got := doReq("10.0.0.2"); got != 200 {
		t.Fatalf("ip B first call should be 200, got %d (isolation broken)", got)
	}
}

// 不同 Name 也应独立计数（同 IP 同时打两个端点不互相干扰）
func TestRateLimit_NamesAreIsolated(t *testing.T) {
	r := gin.New()
	cache := newTestCache(t)
	r.GET("/a", RateLimit(cache, RateLimitConfig{Name: "a", Window: time.Hour, Max: 1}),
		func(c *gin.Context) { c.Status(200) })
	r.GET("/b", RateLimit(cache, RateLimitConfig{Name: "b", Window: time.Hour, Max: 1}),
		func(c *gin.Context) { c.Status(200) })

	doReq := func(path string) int {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "10.0.0.99:1234"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := doReq("/a"); got != 200 {
		t.Fatalf("first /a should pass, got %d", got)
	}
	if got := doReq("/a"); got != 429 {
		t.Fatalf("second /a should 429, got %d", got)
	}
	// 同 IP 跑到 /b 上仍应放行（不同 name 独立窗口）
	if got := doReq("/b"); got != 200 {
		t.Fatalf("first /b should pass (different name), got %d", got)
	}
}

// cache=nil 时降级为 pass-through，不阻拦请求
func TestRateLimit_NilCachePassThrough(t *testing.T) {
	r := gin.New()
	r.Use(RateLimit(nil, RateLimitConfig{Name: "test", Window: time.Hour, Max: 1}))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	doReq := func() int {
		req := httptest.NewRequest("GET", "/x", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	// 10 次都应放行（限流被禁用）
	for i := range 10 {
		if got := doReq(); got != 200 {
			t.Errorf("nil cache should pass-through; req #%d got %d", i, got)
		}
	}
}
