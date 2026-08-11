package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ecstasoy/LGTM/backend/internal/store"
)

func newStore(t *testing.T, maxTurns int) *CacheSessionStore {
	t.Helper()
	c := store.NewMemoryCache(time.Minute)
	t.Cleanup(func() { _ = c.Close() })
	return NewCacheSessionStore(c, maxTurns, time.Hour)
}

func TestAppendThenGet_RoundTrip(t *testing.T) {
	s := newStore(t, 10)
	ctx := context.Background()

	if err := s.Append(ctx, "rev1", Turn{UserText: "hi", AgentText: "hello", CreatedAt: time.Unix(1, 0)}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Append(ctx, "rev1", Turn{UserText: "where?", AgentText: "main.go:42", CreatedAt: time.Unix(2, 0), Steps: 3}); err != nil {
		t.Fatalf("append: %v", err)
	}

	turns, err := s.Get(ctx, "rev1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("want 2 turns, got %d", len(turns))
	}
	if turns[0].UserText != "hi" || turns[1].UserText != "where?" {
		t.Errorf("turn ordering wrong: %+v", turns)
	}
	if turns[1].Steps != 3 {
		t.Errorf("steps not preserved: %+v", turns[1])
	}
}

func TestGet_NonExistent_ReturnsNil(t *testing.T) {
	s := newStore(t, 10)
	turns, err := s.Get(context.Background(), "nope")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if turns != nil {
		t.Errorf("want nil, got %v", turns)
	}
}

func TestSlidingWindow_KeepsRecentN(t *testing.T) {
	s := newStore(t, 3)
	ctx := context.Background()
	for i := range 5 {
		if err := s.Append(ctx, "rev1", Turn{UserText: pad(i, "u"), AgentText: pad(i, "a")}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	turns, _ := s.Get(ctx, "rev1")
	if len(turns) != 3 {
		t.Fatalf("want cap=3, got %d turns", len(turns))
	}
	// 应该保留最后 3 条（i=2,3,4）
	if turns[0].UserText != pad(2, "u") || turns[2].UserText != pad(4, "u") {
		t.Errorf("dropped wrong turns: first=%s last=%s", turns[0].UserText, turns[2].UserText)
	}
}

func TestReset_DeletesAll(t *testing.T) {
	s := newStore(t, 10)
	ctx := context.Background()
	_ = s.Append(ctx, "rev1", Turn{UserText: "x", AgentText: "y"})
	if err := s.Reset(ctx, "rev1"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	turns, _ := s.Get(ctx, "rev1")
	if turns != nil {
		t.Errorf("after reset should be nil, got %v", turns)
	}
}

func TestNilReceiver_AllOpsNoop(t *testing.T) {
	var s *CacheSessionStore // 显式 nil
	ctx := context.Background()
	if err := s.Append(ctx, "rev1", Turn{}); err != nil {
		t.Errorf("nil Append should noop, got %v", err)
	}
	turns, err := s.Get(ctx, "rev1")
	if err != nil || turns != nil {
		t.Errorf("nil Get want (nil,nil), got (%v,%v)", turns, err)
	}
	if err := s.Reset(ctx, "rev1"); err != nil {
		t.Errorf("nil Reset should noop, got %v", err)
	}
}

func TestEmptyReviewID_Noop(t *testing.T) {
	s := newStore(t, 10)
	if err := s.Append(context.Background(), "", Turn{UserText: "x"}); err != nil {
		t.Errorf("empty id Append should noop, got %v", err)
	}
	turns, _ := s.Get(context.Background(), "")
	if turns != nil {
		t.Errorf("empty id Get should be nil, got %v", turns)
	}
}

func TestPerReviewIsolation(t *testing.T) {
	s := newStore(t, 10)
	ctx := context.Background()
	_ = s.Append(ctx, "rev1", Turn{UserText: "1", AgentText: "a"})
	_ = s.Append(ctx, "rev2", Turn{UserText: "2", AgentText: "b"})
	t1, _ := s.Get(ctx, "rev1")
	t2, _ := s.Get(ctx, "rev2")
	if len(t1) != 1 || t1[0].UserText != "1" {
		t.Errorf("rev1 leaked or missing: %v", t1)
	}
	if len(t2) != 1 || t2[0].UserText != "2" {
		t.Errorf("rev2 leaked or missing: %v", t2)
	}
}

func TestTTLRefresh_AppendExtendsTTL(t *testing.T) {
	// 用很短 TTL：250ms；Append 间隔 100ms（小于 TTL）；最后 Get 该仍存在
	c := store.NewMemoryCache(time.Millisecond * 50)
	t.Cleanup(func() { _ = c.Close() })
	s := NewCacheSessionStore(c, 10, 250*time.Millisecond)
	ctx := context.Background()

	for i := range 4 {
		_ = s.Append(ctx, "rev1", Turn{UserText: pad(i, "u")})
		time.Sleep(100 * time.Millisecond)
	}
	// 总流逝 400ms > 单次 TTL 250ms，但每次 Append 都刷 TTL，应仍存活
	turns, _ := s.Get(ctx, "rev1")
	if len(turns) == 0 {
		t.Errorf("TTL should be refreshed on each Append, but turns expired")
	}
}

func TestCorruptedPayload_FailsSoft(t *testing.T) {
	// 直接往 cache 塞非 JSON；Get 应返 nil 而非 err
	c := store.NewMemoryCache(time.Minute)
	t.Cleanup(func() { _ = c.Close() })
	_ = c.Set(context.Background(), keyPrefix+"rev1", []byte("not valid json"), time.Hour)

	s := NewCacheSessionStore(c, 10, time.Hour)
	turns, err := s.Get(context.Background(), "rev1")
	if err != nil {
		t.Errorf("corrupted payload should fail-soft, got err=%v", err)
	}
	if turns != nil {
		t.Errorf("corrupted payload should yield nil, got %v", turns)
	}
}

func TestCacheError_PropagatesOnAppend(t *testing.T) {
	// 模拟 cache.Set 失败
	s := NewCacheSessionStore(&errCache{setErr: errors.New("redis down")}, 10, time.Hour)
	err := s.Append(context.Background(), "rev1", Turn{UserText: "x"})
	if err == nil {
		t.Errorf("want set error to propagate, got nil")
	}
}

// errCache 测试用：可注错的 Cache stub
type errCache struct {
	setErr error
	getErr error
}

func (e *errCache) Get(_ context.Context, _ string) ([]byte, bool, error) {
	return nil, false, e.getErr
}
func (e *errCache) Set(_ context.Context, _ string, _ []byte, _ time.Duration) error {
	return e.setErr
}
func (e *errCache) Incr(_ context.Context, _ string, _ time.Duration) (int64, error) {
	return 0, nil
}
func (e *errCache) Delete(_ context.Context, _ string) error { return nil }

func pad(i int, prefix string) string {
	return prefix + string(rune('0'+i))
}
