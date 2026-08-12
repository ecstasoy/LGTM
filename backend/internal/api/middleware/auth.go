package middleware

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/session"
)

// AuthCtx reads the session cookie and loads the *session.Session onto the gin.Context
// Not being logged in / an invalid cookie is not an error; each handler decides whether it requires login
// With sm=nil it degrades to a placeholder (kept for older main wiring)
//
// userID is also put on the ctx so the existing Store.Put can pick it up (always nil in v1; filled in after v2 OAuth)
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
					c.Set("_lgtm_session", s) // same as api.sessionCtxKey; not referenced directly to avoid an import cycle
					login := s.Login
					userID = &login
				}
			}
		}
		c.Set("userID", userID)
		c.Next()
	}
}
