package middleware

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/session"
)

// AuthCtx 读 session cookie → 加载 *session.Session 到 gin.Context
// 未登录 / cookie 无效时不报错，handler 自己判断是否要求登录
// sm=nil 时降级为占位（兼容老 main 接线）
//
// userID 同时也塞 ctx 让既有 Store.Put 拿到（v1 永远 nil；v2 OAuth 后填）
func AuthCtx(sm *session.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var userID *string
		if sm != nil {
			sid, _ := c.Cookie(session.CookieName)
			if sid != "" {
				s, err := sm.Get(c.Request.Context(), sid)
				if err != nil {
					slog.Warn("session get failed", "err", err)
				}
				if s != nil {
					c.Set("_lgtm_session", s) // 同 api.sessionCtxKey；避免循环 import 不直接 ref
					login := s.Login
					userID = &login
				}
			}
		}
		c.Set("userID", userID)
		c.Next()
	}
}
