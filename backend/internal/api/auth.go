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
	// stateCookieName is the CSRF state cookie; short TTL, exists only during the OAuth redirect
	stateCookieName = "lgtm_oauth_state"
	// nextCookieName is the pre-login return URL, redirected back to after the callback
	nextCookieName = "lgtm_oauth_next"
	// stateCookieTTL is the OAuth state cookie lifetime; 5 minutes is enough to consent on github.com
	stateCookieTTL = 5 * time.Minute
)

// safeRedirectPath blocks open redirects: relative paths only
// "https://evil.com" → "/"; "/review/abc" → returned as-is
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
// Issues the state cookie and redirects to GitHub's authorization page
func AuthLogin(oa *oauth.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if oa == nil || oa.ClientID == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GitHub OAuth 未配置（缺 GITHUB_OAUTH_CLIENT_ID）", "code": CodeOAuthNotConfigured})
			return
		}
		state, err := randomState()
		if err != nil {
			slog.Error("oauth login: gen state", "err", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "state gen failed"})
			return
		}
		// state travels in a short-lived HttpOnly cookie; SameSite=Lax lets it ride along on GitHub's 302 back
		setCookie(c, stateCookieName, state, int(stateCookieTTL.Seconds()))
		// next is stored in a cookie too (rather than stuffed into state)
		next := safeRedirectPath(c.Query("next"))
		setCookie(c, nextCookieName, next, int(stateCookieTTL.Seconds()))

		c.Redirect(http.StatusFound, oa.AuthorizeURL(state))
	}
}

// AuthCallback GET /api/auth/github/callback?code=&state=
// Verify state → exchange for a token → fetch the user → create a session → set the cookie → redirect to next
func AuthCallback(oa *oauth.Client, sm *session.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if oa == nil || sm == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OAuth not configured"})
			return
		}

		// verify state (CSRF guard)
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

		// create the session
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

		// set the session cookie
		setCookie(c, session.CookieName, sid, int(sm.TTL().Seconds()))

		// read next back, then clear the cookie
		next, _ := c.Cookie(nextCookieName)
		clearCookie(c, nextCookieName)
		next = safeRedirectPath(next)

		slog.Info("oauth: user signed in", "login", u.Login, "user_id", u.ID, "next", next)
		c.Redirect(http.StatusFound, next)
	}
}

// AuthLogout POST /api/auth/logout
// Deletes the session and clears the cookie; idempotent
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

// setCookie is the single place cookies are written: HttpOnly + SameSite=Lax + Secure (in production)
// Path=/ so the frontend sends it from any path
// Domain is left empty: the browser binds it to the current domain (after the Vercel rewrite that is vercel.app)
func setCookie(c *gin.Context, name, value string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, value, maxAge, "/", "", isSecure(c), true)
}

// clearCookie expires immediately via max-age=-1
func clearCookie(c *gin.Context, name string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(name, "", -1, "/", "", isSecure(c), true)
}

// isSecure reports whether this request is HTTPS (production) or HTTP (local dev)
// Behind a reverse proxy it reads X-Forwarded-Proto; TrustedProxies is already configured, which makes c.Request.TLS unreliable,
// so Forwarded-Proto is read explicitly
func isSecure(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	proto := c.GetHeader("X-Forwarded-Proto")
	if proto == "" {
		// parse the Forwarded header (RFC 7239) as a fallback
		fwd := c.GetHeader("Forwarded")
		if strings.Contains(strings.ToLower(fwd), "proto=https") {
			return true
		}
	}
	return strings.EqualFold(proto, "https")
}

// CurrentSession pulls the current session off the gin.Context; set by middleware, read by handlers
// Returns nil when not logged in
func CurrentSession(c *gin.Context) *session.Session {
	v, ok := c.Get(sessionCtxKey)
	if !ok {
		return nil
	}
	s, _ := v.(*session.Session)
	return s
}

// sessionCtxKey is the gin Context key holding a *session.Session
// The same literal string as middleware.AuthCtx (referencing it directly would create an import cycle)
const sessionCtxKey = "_lgtm_session"
