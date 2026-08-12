package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// Notification is one in-app "the PR review finished" notification
// A webhook-triggered review drops one into the user's cache list on completion; the frontend polls /api/notifications for them
type Notification struct {
	ID        string `json:"id"`
	ReviewID  string `json:"review_id"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	PR        int    `json:"pr"`
	Title     string `json:"title,omitempty"`
	Source    string `json:"source"` // "webhook"
	CreatedAt string `json:"created_at"`
}

const (
	// notifCacheKeyPrefix is the key prefix for notification lists in Cache
	notifCacheKeyPrefix = "notif:"
	// notifTTL keeps notifications for 7 days, then drops them (unread ones expire too)
	notifTTL = 7 * 24 * time.Hour
	// notifMaxPerUser keeps at most 50 per user; new ones push out the old
	notifMaxPerUser = 50
)

// notifKey namespaces the cache key by user login
func notifKey(login string) string { return notifCacheKeyPrefix + login }

// PushNotification appends one to the user's list; at 50 the oldest is dropped
// A cache failure only warns — notifications are nice-to-have and should never block the main flow
func PushNotification(ctx context.Context, cache store.Cache, login string, n Notification) {
	if cache == nil || login == "" {
		return
	}
	if n.CreatedAt == "" {
		n.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	key := notifKey(login)
	raw, _, err := cache.Get(ctx, key)
	if err != nil {
		slog.Warn("notif get existing failed", "err", err, "login", login)
		// carry on and treat it as fresh
	}
	var existing []Notification
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &existing)
	}
	// new ones go to the front (newest first)
	existing = append([]Notification{n}, existing...)
	if len(existing) > notifMaxPerUser {
		existing = existing[:notifMaxPerUser]
	}
	out, _ := json.Marshal(existing)
	if err := cache.Set(ctx, key, out, notifTTL); err != nil {
		slog.Warn("notif set failed", "err", err, "login", login)
	}
}

// GetNotifications GET /api/notifications?since=<id>
// Returns the logged-in user's notifications; a non-empty since returns only the ones newer than it (taken from the front of the newest-first order)
// since lets the frontend poll for the delta only, so it does not re-toast the same notification
func GetNotifications() gin.HandlerFunc {
	return func(c *gin.Context) {
		s := CurrentSession(c)
		if s == nil {
			c.JSON(http.StatusOK, []Notification{})
			return
		}
		cache := getCache(c)
		if cache == nil {
			c.JSON(http.StatusOK, []Notification{})
			return
		}
		key := notifKey(s.Login)
		raw, _, err := cache.Get(c.Request.Context(), key)
		if err != nil {
			slog.Warn("notif fetch failed", "err", err)
			c.JSON(http.StatusOK, []Notification{})
			return
		}
		if len(raw) == 0 {
			c.JSON(http.StatusOK, []Notification{})
			return
		}
		var list []Notification
		if err := json.Unmarshal(raw, &list); err != nil {
			c.JSON(http.StatusOK, []Notification{})
			return
		}
		// since filter
		since := c.Query("since")
		if since != "" {
			cut := -1
			for i, n := range list {
				if n.ID == since {
					cut = i
					break
				}
			}
			if cut >= 0 {
				list = list[:cut]
			}
		}
		c.JSON(http.StatusOK, list)
	}
}

// getCache pulls store.Cache out of the gin context (main wires it into Deps; middleware cannot reach it because deps are not on the ctx)
// Workaround: the handler is injected by the router, so this goes through a closure rather than the ctx
// In practice GetNotifications should use the closure form, rewritten below:
// (this function stays as a placeholder, superseded by GetNotificationsHandler below)
func getCache(_ *gin.Context) store.Cache { return nil }

// GetNotificationsHandler injects cache through a closure, so the handler can actually use it
func GetNotificationsHandler(cache store.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		s := CurrentSession(c)
		if s == nil {
			c.JSON(http.StatusOK, []Notification{})
			return
		}
		if cache == nil {
			c.JSON(http.StatusOK, []Notification{})
			return
		}
		key := notifKey(s.Login)
		raw, _, err := cache.Get(c.Request.Context(), key)
		if err != nil {
			slog.Warn("notif fetch failed", "err", err)
			c.JSON(http.StatusOK, []Notification{})
			return
		}
		if len(raw) == 0 {
			c.JSON(http.StatusOK, []Notification{})
			return
		}
		var list []Notification
		if err := json.Unmarshal(raw, &list); err != nil {
			c.JSON(http.StatusOK, []Notification{})
			return
		}
		// since filter
		since := c.Query("since")
		if since != "" {
			cut := -1
			for i, n := range list {
				if n.ID == since {
					cut = i
					break
				}
			}
			if cut >= 0 {
				list = list[:cut]
			}
		}
		c.JSON(http.StatusOK, list)
	}
}
