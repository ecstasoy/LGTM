package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
)

// errFetcher always errors; lets handler tests verify the synchronous response
// without the goroutine panicking on nil Fetcher
type errFetcher struct{}

func (errFetcher) Fetch(_ context.Context, _ string) (gh.PullRequest, error) {
	return gh.PullRequest{}, errors.New("stub")
}

func TestVerifyHMAC_Matches(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	secret := []byte("supersecret")
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !verifyHMAC(sig, body, secret) {
		t.Fatal("expected match")
	}
}

func TestVerifyHMAC_Mismatch(t *testing.T) {
	body := []byte("payload")
	if verifyHMAC("sha256=deadbeef", body, []byte("secret")) {
		t.Fatal("expected mismatch")
	}
}

func TestVerifyHMAC_MissingPrefix(t *testing.T) {
	if verifyHMAC("md5=xyz", []byte("p"), []byte("s")) {
		t.Fatal("non-sha256 prefix should fail")
	}
	if verifyHMAC("", []byte("p"), []byte("s")) {
		t.Fatal("empty sig should fail")
	}
}

func TestWebhook_RejectsBadSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/webhook/github", WebhookGitHub(Deps{}, "secret"))
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/github", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", "sha256=00")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", w.Code)
	}
}

func TestWebhook_503WhenSecretMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/webhook/github", WebhookGitHub(Deps{}, ""))
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/github", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-GitHub-Event", "pull_request")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d want 503", w.Code)
	}
}

func TestWebhook_IgnoresNonPullRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	secret := "s"
	r.POST("/api/webhook/github", WebhookGitHub(Deps{}, secret))
	body := []byte(`{"zen":"hello"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status=%d want 200", w.Code)
	}
}

func TestWebhook_IgnoresNonOpenedAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	secret := "s"
	r.POST("/api/webhook/github", WebhookGitHub(Deps{}, secret))
	body := []byte(`{"action":"closed","number":1,"pull_request":{"html_url":"x"},"repository":{"owner":{"login":"o"},"name":"r"}}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("ignored_action")) {
		t.Errorf("body should explain ignored_action; got %s", w.Body.String())
	}
}

func TestWebhook_AcceptsSynchronizeAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	secret := "s"
	// Deps 没 Fetcher → goroutine 里 fetch 会 nil pointer，但 handler 立刻 202 返回前已成功
	r.POST("/api/webhook/github", WebhookGitHub(Deps{Fetcher: errFetcher{}}, secret))
	body := []byte(`{"action":"synchronize","number":42,"pull_request":{"html_url":"https://github.com/o/r/pull/42","title":"x"},"repository":{"owner":{"login":"o"},"name":"r"},"installation":{"id":1},"sender":{"login":"u"}}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	// 防 goroutine fetch nil panic 影响测试退出：recover via defer at handler level not possible here
	// 但 202 response 在 spawn goroutine 之前发；以 status 为准
	defer func() { _ = recover() }()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Errorf("status=%d want 202; body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"action":"synchronize"`)) {
		t.Errorf("body should echo synchronize action; got %s", w.Body.String())
	}
}

func TestWebhook_IssueCommentNonPRIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	secret := "s"
	r.POST("/api/webhook/github", WebhookGitHub(Deps{}, secret))
	// issue.pull_request 字段缺失 → 不是 PR 评论
	body := []byte(`{"action":"created","issue":{"number":1},"comment":{"body":"/lgtm review"},"repository":{"owner":{"login":"o"},"name":"r"},"sender":{"login":"u"}}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("non-PR")) {
		t.Errorf("body should explain non-PR ignore; got %s", w.Body.String())
	}
}

func TestWebhook_IssueCommentBotSenderIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	secret := "s"
	r.POST("/api/webhook/github", WebhookGitHub(Deps{}, secret))
	// sender 是 bot → 防自我循环
	body := []byte(`{"action":"created","issue":{"number":1,"pull_request":{"html_url":"x"}},"comment":{"body":"/lgtm review"},"repository":{"owner":{"login":"o"},"name":"r"},"sender":{"login":"lgtm-ai-reviewer[bot]"}}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("bot sender")) {
		t.Errorf("bot sender should be filtered; status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestParseSlashCommand(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"plain review", "/lgtm review", "review"},
		{"with leading text", "thanks!\n/lgtm review\nplease", "review"},
		{"help", "/lgtm help", "help"},
		{"bare lgtm defaults to review", "/lgtm", "review"},
		{"uppercase", "/lgtm REVIEW", "review"},
		{"with extra args", "/lgtm review now please", "review"},
		{"indented", "  /lgtm review", "review"},
		{"not slash command", "lgtm review", ""},
		{"missing slash lgtm", "/foo review", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := parseSlashCommand(c.body); got != c.want {
			t.Errorf("%s: parseSlashCommand(%q) = %q, want %q", c.name, c.body, got, c.want)
		}
	}
}
