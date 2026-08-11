package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/oauth"
	"github.com/ecstasoy/LGTM/backend/internal/session"
)

const (
	// stateCookieName CSRF 状态 cookie；短 TTL 只在 OAuth 跳转期间存在
	stateCookieName = "lgtm_oauth_state"
	// nextCookieName 登录前的返回 URL；callback 后跳回去
	nextCookieName = "lgtm_oauth_next"
	// stateCookieTTL OAuth 状态 cookie 寿命；5 分钟足够用户在 github.com 同意
	stateCookieTTL = 5 * time.Minute
)

// safeRedirectPath 防开放重定向：仅允许相对路径
// "https://evil.com" → "/"；"/review/abc" → 原样返
func safeRedirectPath(next string) string {
	if next == "" {
		return "/"
	}
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
		return next
	}
	return "/"
}

// AuthLogin GET /api/auth/github/login?next=/path
// 生成 state cookie + 重定向到 GitHub 授权页
func AuthLogin(oa *oauth.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if oa == nil || oa.ClientID == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GitHub OAuth 未配置（缺 GITHUB_OAUTH_CLIENT_ID）"})
			return
		}
		state, err := randomState()
		if err != nil {
			slog.Error("oauth login: gen state", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "state gen failed"})
			return
		}
		// state 走 short-lived HttpOnly cookie；SameSite=Lax 允许 GitHub 302 回来时带上
		setCookie(c, stateCookieName, state, int(stateCookieTTL.Seconds()))
		// 把 next 也存 cookie（避免塞到 state 里）
		next := safeRedirectPath(c.Query("next"))
		setCookie(c, nextCookieName, next, int(stateCookieTTL.Seconds()))

		c.Redirect(http.StatusFound, oa.AuthorizeURL(state))
	}
}

// AuthCallback GET /api/auth/github/callback?code=&state=
// 校验 state → 换 token → 拿用户 → 建 session → 写 cookie → 跳回 next
func AuthCallback(oa *oauth.Client, sm *session.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if oa == nil || sm == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OAuth not configured"})
			return
		}

		// 验 state（防 CSRF）
		gotState := c.Query("state")
		wantState, _ := c.Cookie(stateCookieName)
		clearCookie(c, stateCookieName)
		if gotState == "" || wantState == "" || gotState != wantState {
			slog.Warn("oauth callback: state mismatch", "got_len", len(gotState), "want_len", len(wantState))
			c.JSON(http.StatusBadRequest, gin.H{"error": "state mismatch"})
			return
		}

		code := c.Query("code")
		if code == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing code"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()

		// code → access_token
		tok, err := oa.ExchangeCode(ctx, code)
		if err != nil {
			slog.Error("oauth callback: exchange", "err", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "token exchange failed", "detail": err.Error()})
			return
		}

		// access_token → user
		u, err := oa.FetchUser(ctx, tok.AccessToken)
		if err != nil {
			slog.Error("oauth callback: fetch user", "err", err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "fetch user failed", "detail": err.Error()})
			return
		}

		// 建 session
		sid, err := sm.Create(ctx, session.Session{
			UserID:      u.ID,
			Login:       u.Login,
			AvatarURL:   u.AvatarURL,
			Name:        u.Name,
			AccessToken: tok.AccessToken,
		})
		if err != nil {
			slog.Error("oauth callback: create session", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "session create failed"})
			return
		}

		// 写 session cookie
		setCookie(c, session.CookieName, sid, int(sm.TTL().Seconds()))

		// 取回 next，清掉 cookie
		next, _ := c.Cookie(nextCookieName)
		clearCookie(c, nextCookieName)
		next = safeRedirectPath(next)

		slog.Info("oauth: user signed in", "login", u.Login, "user_id", u.ID, "next", next)
		c.Redirect(http.StatusFound, next)
	}
}

// AuthLogout POST /api/auth/logout
// 删 session + 清 cookie；幂等
func AuthLogout(sm *session.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		sid, _ := c.Cookie(session.CookieName)
		if sm != nil && sid != "" {
			if err := sm.Delete(c.Request.Context(), sid); err != nil {
				slog.Warn("logout: session delete failed", "err", err)
			}
		}
		clearCookie(c, session.CookieName)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// randomState OAuth state CSRF token；20 byte base64
func randomState() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// setCookie 统一 cookie 写入：HttpOnly + SameSite=Lax + Secure（生产）
// Path=/ 让前端任何路径都能带上
// Domain 留空：浏览器自动绑当前域（Vercel rewrite 后 = vercel.app）
func setCookie(c *gin.Context, name, value string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, value, maxAge, "/", "", isSecure(c), true)
}

// clearCookie max-age=-1 立刻过期
func clearCookie(c *gin.Context, name string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, "", -1, "/", "", isSecure(c), true)
}

// isSecure 判断当前请求是 HTTPS（生产）或 HTTP（本地 dev）
// 走反代时看 X-Forwarded-Proto；TrustedProxies 已经设过让 c.Request.TLS 不可靠
// 因此显式读 Forwarded-Proto
func isSecure(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	proto := c.GetHeader("X-Forwarded-Proto")
	if proto == "" {
		// 解析 Forwarded 头（RFC 7239）作 fallback
		fwd := c.GetHeader("Forwarded")
		if strings.Contains(strings.ToLower(fwd), "proto=https") {
			return true
		}
	}
	return strings.EqualFold(proto, "https")
}

// CurrentSession 从 gin.Context 取当前 session；中间件设置；handler 用
// 未登录返 nil
func CurrentSession(c *gin.Context) *session.Session {
	v, ok := c.Get(sessionCtxKey)
	if !ok {
		return nil
	}
	s, _ := v.(*session.Session)
	return s
}

// sessionCtxKey gin Context 里存 *session.Session 的 key
// 与 middleware.AuthCtx 约定相同字符串（避免循环 import 直接 reference）
const sessionCtxKey = "_lgtm_session"
