package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/oauth"
	"github.com/ecstasoy/LGTM/backend/internal/session"
)

// startPermsTestServer 起带 /api/perms 路由的迷你 server；middleware 简化版自己读 cookie
func startPermsTestServer(t *testing.T, oa *oauth.Client, sm *session.Manager) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api")
	g.GET("/perms", func(c *gin.Context) {
		sid, _ := c.Cookie(session.CookieName)
		if sid != "" && sm != nil {
			if s, _ := sm.Get(c.Request.Context(), sid); s != nil {
				c.Set(sessionCtxKey, s)
			}
		}
		GetPerms(oa)(c)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// permsAPISrv 返回固定 perm 的 mock GitHub API server
func permsAPISrv(t *testing.T, perm string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"x"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"permission":"` + perm + `","user":{"login":"u"}}`))
	}))
}

func TestPerms_Unauthenticated(t *testing.T) {
	srv := startPermsTestServer(t, &oauth.Client{}, session.New(nil, 0))
	res, err := http.Get(srv.URL + "/api/perms?owner=o&repo=r")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	var p PermsResponse
	_ = json.NewDecoder(res.Body).Decode(&p)
	if p.Authenticated || p.CanComment || p.CanCommit {
		t.Errorf("unauth should be all false: %+v", p)
	}
	if p.Reason == "" {
		t.Error("reason should explain")
	}
}

func TestPerms_MissingParams(t *testing.T) {
	srv := startPermsTestServer(t, &oauth.Client{}, session.New(nil, 0))
	res, err := http.Get(srv.URL + "/api/perms")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", res.StatusCode)
	}
}

func TestPerms_AuthedWithWritePermission(t *testing.T) {
	gh := permsAPISrv(t, "write", http.StatusOK)
	defer gh.Close()
	oa := &oauth.Client{HTTPClient: &http.Client{Transport: rewriteHost{base: gh.URL}, Timeout: 5 * time.Second}}
	sm := session.New(nil, 0)
	sid, _ := sm.Create(t.Context(), session.Session{UserID: 1, Login: "u", AccessToken: "tok"})
	srv := startPermsTestServer(t, oa, sm)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/perms?owner=o&repo=r", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: sid})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	var p PermsResponse
	_ = json.NewDecoder(res.Body).Decode(&p)
	if !p.Authenticated || !p.CanComment || !p.CanCommit {
		t.Errorf("write should grant both: %+v", p)
	}
	if p.Permission != "write" {
		t.Errorf("perm = %s", p.Permission)
	}
}

func TestPerms_AuthedWithTriageCanCommentNotCommit(t *testing.T) {
	gh := permsAPISrv(t, "triage", http.StatusOK)
	defer gh.Close()
	oa := &oauth.Client{HTTPClient: &http.Client{Transport: rewriteHost{base: gh.URL}, Timeout: 5 * time.Second}}
	sm := session.New(nil, 0)
	sid, _ := sm.Create(t.Context(), session.Session{UserID: 1, Login: "u", AccessToken: "tok"})
	srv := startPermsTestServer(t, oa, sm)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/perms?owner=o&repo=r", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: sid})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	var p PermsResponse
	_ = json.NewDecoder(res.Body).Decode(&p)
	if !p.CanComment {
		t.Error("triage should comment")
	}
	if p.CanCommit {
		t.Error("triage should NOT commit")
	}
	if p.Reason == "" {
		t.Error("disable reason should explain")
	}
}

func TestPerms_NotCollaboratorReturns404Asnone(t *testing.T) {
	gh := permsAPISrv(t, "", http.StatusNotFound)
	defer gh.Close()
	oa := &oauth.Client{HTTPClient: &http.Client{Transport: rewriteHost{base: gh.URL}, Timeout: 5 * time.Second}}
	sm := session.New(nil, 0)
	sid, _ := sm.Create(t.Context(), session.Session{UserID: 1, Login: "u", AccessToken: "tok"})
	srv := startPermsTestServer(t, oa, sm)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/perms?owner=o&repo=r", nil)
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: sid})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	var p PermsResponse
	_ = json.NewDecoder(res.Body).Decode(&p)
	if p.CanComment || p.CanCommit {
		t.Errorf("non-collaborator should have no perms: %+v", p)
	}
	if p.Permission != "none" {
		t.Errorf("perm = %s want none", p.Permission)
	}
}

// 防 url 包 import 警告（rewriteHost 用）
var _ = url.QueryEscape
