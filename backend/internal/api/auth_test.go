package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/oauth"
	"github.com/ecstasoy/LGTM/backend/internal/session"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// startAuthTestServer 起一个迷你 server 含 /api/auth/* + /api/me 路由
// oauthSrv 是 mocked GitHub host，用 rewriteHost transport 把 OAuth client 流量转到这里
func startAuthTestServer(t *testing.T, oa *oauth.Client, sm *session.Manager) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api")
	g.GET("/auth/github/login", AuthLogin(oa))
	g.GET("/auth/github/callback", AuthCallback(oa, sm))
	g.POST("/auth/logout", AuthLogout(sm))
	// /me 需要 middleware 注入 session；这里手动模拟最简版
	g.GET("/me", func(c *gin.Context) {
		sid, _ := c.Cookie(session.CookieName)
		if sid != "" && sm != nil {
			if s, _ := sm.Get(c.Request.Context(), sid); s != nil {
				c.Set(sessionCtxKey, s)
			}
		}
		GetMe()(c)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestAuthLogin_RedirectsToGithubWithStateCookie(t *testing.T) {
	oa := &oauth.Client{ClientID: "Iv1.test", ClientSecret: "secret", RedirectURI: "https://x/cb"}
	sm := session.New(nil, 0)
	srv := startAuthTestServer(t, oa, sm)

	// 不跟随 302
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(srv.URL + "/api/auth/github/login?next=/review/abc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusFound {
		t.Fatalf("status=%d want 302", res.StatusCode)
	}
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://github.com/login/oauth/authorize?") {
		t.Errorf("Location=%s, want GitHub authorize URL", loc)
	}
	// state 在 query + cookie 都得有，且相等
	u, _ := url.Parse(loc)
	stateInQuery := u.Query().Get("state")
	if stateInQuery == "" {
		t.Error("state missing in redirect URL")
	}
	var stateCookie, nextCookie *http.Cookie
	for _, c := range res.Cookies() {
		switch c.Name {
		case stateCookieName:
			stateCookie = c
		case nextCookieName:
			nextCookie = c
		}
	}
	// gin.Context.SetCookie 会对 value 做 url.QueryEscape，比对前先 unescape
	if stateCookie == nil {
		t.Fatal("state cookie missing")
	}
	stateCookieDecoded, _ := url.QueryUnescape(stateCookie.Value)
	if stateCookieDecoded != stateInQuery {
		t.Errorf("state cookie mismatched (cookie=%q decoded=%q query=%q)", stateCookie.Value, stateCookieDecoded, stateInQuery)
	}
	if !stateCookie.HttpOnly {
		t.Error("state cookie must be HttpOnly")
	}
	if nextCookie == nil {
		t.Fatal("next cookie missing")
	}
	nextDecoded, _ := url.QueryUnescape(nextCookie.Value)
	if nextDecoded != "/review/abc" {
		t.Errorf("next cookie = %q (decoded %q), want /review/abc", nextCookie.Value, nextDecoded)
	}
}

func TestAuthLogin_503WhenOAuthNotConfigured(t *testing.T) {
	srv := startAuthTestServer(t, nil, nil)
	res, err := http.Get(srv.URL + "/api/auth/github/login")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status=%d want 503", res.StatusCode)
	}
}

func TestAuthLogin_NextSanitizes(t *testing.T) {
	oa := &oauth.Client{ClientID: "x", ClientSecret: "y", RedirectURI: "https://z/cb"}
	srv := startAuthTestServer(t, oa, session.New(nil, 0))
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// 试图开放重定向到外部域，必须被消毒为 /
	res, err := client.Get(srv.URL + "/api/auth/github/login?next=https://evil.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	for _, c := range res.Cookies() {
		if c.Name == nextCookieName {
			decoded, _ := url.QueryUnescape(c.Value)
			if decoded != "/" {
				t.Errorf("evil next not sanitized: cookie=%q decoded=%q", c.Value, decoded)
			}
		}
	}
}

func TestAuthCallback_FullFlow(t *testing.T) {
	// mock GitHub：处理 /login/oauth/access_token + /user
	githubSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(oauth.TokenResponse{AccessToken: "ghu_test", TokenType: "bearer"})
		case "/user":
			if r.Header.Get("Authorization") != "Bearer ghu_test" {
				t.Errorf("bad auth header: %s", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":42,"login":"octocat","avatar_url":"https://avatar","name":"Mona"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer githubSrv.Close()

	oa := &oauth.Client{
		ClientID: "cid", ClientSecret: "csec", RedirectURI: "https://x/cb",
		HTTPClient: &http.Client{Transport: rewriteHost{base: githubSrv.URL}, Timeout: 5 * time.Second},
	}
	cache := store.NewMemoryCache(time.Minute)
	defer cache.Close()
	sm := session.New(cache, 0)
	srv := startAuthTestServer(t, oa, sm)

	// 模拟 GitHub 302 回来：必须带 state cookie + 同 state query
	client := &http.Client{
		Jar:           newCookieJar(t),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	// 1) 先 /login 拿 state cookie
	res1, err := client.Get(srv.URL + "/api/auth/github/login?next=/review/x")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	res1.Body.Close()
	var state string
	for _, c := range client.Jar.Cookies(mustURL(srv.URL)) {
		if c.Name == stateCookieName {
			state = c.Value
		}
	}
	if state == "" {
		t.Fatal("state cookie not set by /login")
	}

	// 2) 模拟 /callback?code=abc&state=<same>
	res2, err := client.Get(srv.URL + "/api/auth/github/callback?code=abc&state=" + state)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(res2.Body)
		t.Fatalf("callback status=%d body=%s want 302", res2.StatusCode, body)
	}
	if loc := res2.Header.Get("Location"); loc != "/review/x" {
		t.Errorf("callback Location=%q want /review/x", loc)
	}
	// session cookie 应被设
	var sessCookie *http.Cookie
	for _, c := range client.Jar.Cookies(mustURL(srv.URL)) {
		if c.Name == session.CookieName {
			sessCookie = c
		}
	}
	if sessCookie == nil {
		t.Fatal("session cookie not set after callback")
	}

	// 3) /me 应返回登录信息
	res3, err := client.Get(srv.URL + "/api/me")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	defer res3.Body.Close()
	var me MeResponse
	_ = json.NewDecoder(res3.Body).Decode(&me)
	if !me.Authenticated || me.Login != "octocat" || me.UserID != 42 {
		t.Errorf("me = %+v, want authenticated octocat id=42", me)
	}
}

func TestAuthCallback_RejectsStateMismatch(t *testing.T) {
	oa := &oauth.Client{ClientID: "x", ClientSecret: "y", RedirectURI: "https://z/cb"}
	sm := session.New(nil, 0)
	srv := startAuthTestServer(t, oa, sm)

	// 不预先 /login → state cookie 缺；callback 必须拒
	res, err := http.Get(srv.URL + "/api/auth/github/callback?code=abc&state=wrong")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400 on missing state cookie", res.StatusCode)
	}
}

func TestAuthLogout_ClearsCookieAndSession(t *testing.T) {
	sm := session.New(nil, 0)
	sid, _ := sm.Create(t.Context(), session.Session{UserID: 1, Login: "u", AccessToken: "t"})
	srv := startAuthTestServer(t, &oauth.Client{ClientID: "x"}, sm)

	jar := newCookieJar(t)
	jar.SetCookies(mustURL(srv.URL), []*http.Cookie{{Name: session.CookieName, Value: sid, Path: "/"}})
	client := &http.Client{Jar: jar}

	res, err := client.Post(srv.URL+"/api/auth/logout", "", nil)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("status=%d want 200", res.StatusCode)
	}
	// session 应被删
	got, _ := sm.Get(t.Context(), sid)
	if got != nil {
		t.Error("session should be deleted after logout")
	}
}

func TestGetMe_UnauthenticatedReturnsFalseField(t *testing.T) {
	srv := startAuthTestServer(t, &oauth.Client{ClientID: "x"}, session.New(nil, 0))
	res, err := http.Get(srv.URL + "/api/me")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	var me MeResponse
	_ = json.NewDecoder(res.Body).Decode(&me)
	if me.Authenticated {
		t.Errorf("expected authenticated=false; got %+v", me)
	}
}

// --- 工具 ---

func newCookieJar(t *testing.T) *cookiejar.Jar {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new jar: %v", err)
	}
	return jar
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// rewriteHost 复用 oauth/github_test.go 的私有 type；这里独立一份避免跨包导出
type rewriteHost struct {
	base string
}

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.base)
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host
	return http.DefaultTransport.RoundTrip(req)
}
