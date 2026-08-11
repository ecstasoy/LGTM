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

// Notification 一条「PR 自动评完了」的 in-app 通知
// Webhook 触发评审完成后塞进 user 的 cache 列表；前端轮询 /api/notifications 拉取
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
	// notifCacheKeyPrefix Cache 里通知列表的 key 前缀
	notifCacheKeyPrefix = "notif:"
	// notifTTL 通知保留 7 天；超过即删（用户没看就过期）
	notifTTL = 7 * 24 * time.Hour
	// notifMaxPerUser 单用户最多保留 50 条；新来的把老的挤掉
	notifMaxPerUser = 50
)

// notifKey 按用户 login 隔离 cache key
func notifKey(login string) string { return notifCacheKeyPrefix + login }

// PushNotification 往 user 的通知列表里追加一条；满 50 条丢最早的
// cache 故障仅 warn —— 通知是 nice-to-have 不该阻塞业务
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
		// 继续，按 fresh 处理
	}
	var existing []Notification
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &existing)
	}
	// 新的 push 到前面（按时间倒序）
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
// 返当前登录用户的通知列表；since 非空时只返比这条更新的（按时间倒序后取前面）
// since 让前端 poll 时只拿增量，避免重复弹 toast
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
		// since 过滤
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

// getCache 从 gin context 拿 store.Cache（main 接线塞进 Deps；中间件不能直接拿因 deps 不在 ctx）
// 解法：handler 用 router 注入；这里走 closure 而非 ctx
// 实际上 GetNotifications 应该用 closure，重写：
// （此函数留作 placeholder，被下面 GetNotificationsHandler 替代）
func getCache(_ *gin.Context) store.Cache { return nil }

// GetNotificationsHandler 把 cache 通过 closure 注入，handler 真能用
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
		// since 过滤
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
