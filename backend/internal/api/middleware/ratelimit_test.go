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
	c := store.NewMemoryCache(time.Hour) // no background sweep needed for the duration of a test
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// use a tiny Max to prove the fixed window really blocks
func TestRateLimit_AllowsBurstThenBlocks(t *testing.T) {
	r := gin.New()
	cache := newTestCache(t)
	r.Use(RateLimit(cache, RateLimitConfig{Name: "test", Window: time.Hour, Max: 2}))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	srv := httptest.NewServer(r)
	defer srv.Close()

	// two from the same IP should pass; the third should be 429
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

// different IPs should count independently
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
	// the first request from a different IP should still pass
	if got := doReq("10.0.0.2"); got != 200 {
		t.Fatalf("ip B first call should be 200, got %d (isolation broken)", got)
	}
}

// different Names should count independently too (one IP hitting two endpoints must not interfere)
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
	// the same IP on /b should still pass (a different name is a separate window)
	if got := doReq("/b"); got != 200 {
		t.Fatalf("first /b should pass (different name), got %d", got)
	}
}

// with cache=nil it degrades to pass-through and blocks nothing
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

	// all 10 should pass (rate limiting is disabled)
	for i := range 10 {
		if got := doReq(); got != 200 {
			t.Errorf("nil cache should pass-through; req #%d got %d", i, got)
		}
	}
}
