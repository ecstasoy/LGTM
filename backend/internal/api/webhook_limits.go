package api

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// WebhookLimits bounds webhook-triggered review work; a zero field leaves that dimension unlimited.
type WebhookLimits struct {
	MaxConcurrent          int // reviews running at once in this process
	PerInstallationPerHour int // reviews per App installation per clock hour
	PerRepoPerHour         int // reviews per repository per clock hour
}

// WebhookLimiter enforces WebhookLimits; a nil *WebhookLimiter allows everything.
// The concurrency cap is per process; the hourly quotas live in the shared cache so they hold across instances.
type WebhookLimiter struct {
	limits WebhookLimits
	slots  chan struct{} // nil when MaxConcurrent <= 0
	now    func() time.Time
}

// NewWebhookLimiter builds a limiter for l.
func NewWebhookLimiter(l WebhookLimits) *WebhookLimiter {
	w := &WebhookLimiter{limits: l, now: time.Now}
	if l.MaxConcurrent > 0 {
		w.slots = make(chan struct{}, l.MaxConcurrent)
	}
	return w
}

// TryAcquire takes a concurrency slot without waiting; ok=false means the process is at its cap.
func (w *WebhookLimiter) TryAcquire() (release func(), ok bool) {
	if w == nil || w.slots == nil {
		return func() {}, true
	}
	select {
	case w.slots <- struct{}{}:
		return func() { <-w.slots }, true
	default:
		return nil, false
	}
}

// Charge counts one review against the hourly installation and repo quotas and reports whether it fits.
// No cache, or a cache error, lets the review through: the concurrency cap still bounds spend.
func (w *WebhookLimiter) Charge(ctx context.Context, cache store.Cache, installationID int64, owner, repo string) bool {
	if w == nil || cache == nil {
		return true
	}
	hour := w.now().UTC().Format("2006010215")
	checks := []struct {
		key   string
		limit int
	}{
		{fmt.Sprintf("webhook-quota:inst:%d:%s", installationID, hour), w.limits.PerInstallationPerHour},
		{fmt.Sprintf("webhook-quota:repo:%s/%s:%s", strings.ToLower(owner), strings.ToLower(repo), hour), w.limits.PerRepoPerHour},
	}
	allowed := true
	for _, ch := range checks {
		if ch.limit <= 0 {
			continue
		}
		n, err := cache.Incr(ctx, ch.key, time.Hour)
		if err != nil {
			slog.Warn("webhook quota check failed; allowing", "key", ch.key, "err", err)
			continue
		}
		if n > int64(ch.limit) {
			allowed = false
		}
	}
	return allowed
}
