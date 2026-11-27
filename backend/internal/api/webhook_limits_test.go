package api

import (
	"context"
	"testing"
	"time"

	"github.com/ecstasoy/LGTM/backend/internal/store"
)

func TestWebhookLimiter_NilAllowsEverything(t *testing.T) {
	var l *WebhookLimiter
	release, ok := l.TryAcquire()
	if !ok {
		t.Fatal("nil limiter refused a slot")
	}
	release()
	if !l.Charge(context.Background(), store.NewMemoryCache(time.Minute), 1, "o", "r") {
		t.Error("nil limiter refused a review")
	}
}

func TestWebhookLimiter_ConcurrencyCap(t *testing.T) {
	l := NewWebhookLimiter(WebhookLimits{MaxConcurrent: 2})
	r1, ok1 := l.TryAcquire()
	_, ok2 := l.TryAcquire()
	_, ok3 := l.TryAcquire()
	if !ok1 || !ok2 || ok3 {
		t.Fatalf("acquire results %v %v %v, want true true false", ok1, ok2, ok3)
	}
	r1()
	if _, ok := l.TryAcquire(); !ok {
		t.Error("a released slot should be reusable")
	}
}

func TestWebhookLimiter_InstallationQuota(t *testing.T) {
	l := NewWebhookLimiter(WebhookLimits{PerInstallationPerHour: 2})
	c := store.NewMemoryCache(time.Minute)
	ctx := context.Background()
	got := []bool{
		l.Charge(ctx, c, 7, "o", "a"),
		l.Charge(ctx, c, 7, "o", "b"),
		l.Charge(ctx, c, 7, "o", "c"),
		l.Charge(ctx, c, 8, "p", "a"),
	}
	want := []bool{true, true, false, true}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("charge %d = %v, want %v (all: %v)", i, got[i], want[i], got)
		}
	}
}

func TestWebhookLimiter_RepoQuotaIgnoresNameCase(t *testing.T) {
	l := NewWebhookLimiter(WebhookLimits{PerRepoPerHour: 1})
	c := store.NewMemoryCache(time.Minute)
	ctx := context.Background()
	if !l.Charge(ctx, c, 1, "Org", "Repo") {
		t.Fatal("first review refused")
	}
	if l.Charge(ctx, c, 2, "org", "repo") {
		t.Error("same repo with different case must share the quota")
	}
	if !l.Charge(ctx, c, 1, "org", "other") {
		t.Error("another repo has its own quota")
	}
}

func TestWebhookLimiter_QuotaResetsNextHour(t *testing.T) {
	l := NewWebhookLimiter(WebhookLimits{PerInstallationPerHour: 1})
	now := time.Date(2026, 11, 26, 10, 59, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	c := store.NewMemoryCache(time.Minute)
	ctx := context.Background()
	l.Charge(ctx, c, 1, "o", "r")
	if l.Charge(ctx, c, 1, "o", "r") {
		t.Fatal("second review in the same hour should be refused")
	}
	now = now.Add(2 * time.Minute)
	if !l.Charge(ctx, c, 1, "o", "r") {
		t.Error("a new hour should start a fresh quota")
	}
}

func TestWebhookLimiter_NoCacheMeansNoQuota(t *testing.T) {
	l := NewWebhookLimiter(WebhookLimits{PerInstallationPerHour: 1, PerRepoPerHour: 1})
	for i := range 3 {
		if !l.Charge(context.Background(), nil, 1, "o", "r") {
			t.Errorf("charge %d refused without a cache", i)
		}
	}
}
