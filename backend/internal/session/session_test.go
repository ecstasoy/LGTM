package session

import (
	"context"
	"testing"
	"time"

	"github.com/ecstasoy/LGTM/backend/internal/store"
)

func TestNew_FallsBackToMemoryWhenCacheNil(t *testing.T) {
	m := New(nil, 0)
	if m.TTL() != DefaultTTL {
		t.Errorf("ttl=%v, want default", m.TTL())
	}
	id, err := m.Create(context.Background(), Session{UserID: 1, Login: "u", AccessToken: "t"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := m.Get(context.Background(), id)
	if err != nil || got == nil || got.Login != "u" {
		t.Fatalf("get: %+v err=%v", got, err)
	}
}

func TestCreate_RejectsEmptyToken(t *testing.T) {
	m := New(nil, 0)
	if _, err := m.Create(context.Background(), Session{UserID: 1, Login: "u"}); err == nil {
		t.Fatal("expected err when access_token empty")
	}
}

func TestCreate_RejectsEmptyUserID(t *testing.T) {
	m := New(nil, 0)
	if _, err := m.Create(context.Background(), Session{Login: "u", AccessToken: "t"}); err == nil {
		t.Fatal("expected err when user_id zero")
	}
}

func TestGet_MissReturnsNilNotError(t *testing.T) {
	m := New(nil, 0)
	got, err := m.Get(context.Background(), "nonexistent")
	if err != nil || got != nil {
		t.Errorf("miss should return (nil, nil); got %+v err=%v", got, err)
	}
}

func TestGet_EmptyIDReturnsNilNotError(t *testing.T) {
	m := New(nil, 0)
	got, err := m.Get(context.Background(), "")
	if err != nil || got != nil {
		t.Errorf("empty id should return (nil, nil); got %+v err=%v", got, err)
	}
}

func TestDelete_IsIdempotent(t *testing.T) {
	m := New(nil, 0)
	if err := m.Delete(context.Background(), "never-existed"); err != nil {
		t.Errorf("delete missing should not err; got %v", err)
	}
}

func TestCreate_Delete_Roundtrip_WithCache(t *testing.T) {
	cache := store.NewMemoryCache(time.Minute)
	defer cache.Close()
	m := New(cache, time.Hour)

	id, err := m.Create(context.Background(), Session{UserID: 7, Login: "alice", AccessToken: "ghu_xxx"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := m.Get(context.Background(), id)
	if err != nil || got == nil {
		t.Fatalf("get after create: %+v err=%v", got, err)
	}
	if got.AccessToken != "ghu_xxx" {
		t.Errorf("access_token round-trip lost: %+v", got)
	}
	// Created* timestamp 应被自动填
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should auto-populate")
	}

	// Delete + Get → miss
	if err := m.Delete(context.Background(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got2, _ := m.Get(context.Background(), id)
	if got2 != nil {
		t.Errorf("expected miss after delete; got %+v", got2)
	}
}

func TestNewID_UniquenessAndLength(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id, err := newID()
		if err != nil {
			t.Fatalf("newID: %v", err)
		}
		if len(id) < 40 {
			t.Errorf("id too short: %s", id)
		}
		if seen[id] {
			t.Errorf("collision after %d: %s", i, id)
		}
		seen[id] = true
	}
}
