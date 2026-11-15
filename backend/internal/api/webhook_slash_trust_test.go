package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// postSlashComment delivers a signed issue_comment webhook carrying body from a commenter with the given association.
func postSlashComment(t *testing.T, r *gin.Engine, secret, body, association string, pr int) *httptest.ResponseRecorder {
	t.Helper()
	payload := fmt.Appendf(nil, `{"action":"created",`+
		`"issue":{"number":%d,"title":"t","user":{"login":"author"},"pull_request":{"html_url":"https://github.com/o/r/pull/%d"}},`+
		`"comment":{"id":1,"body":%q,"user":{"login":"commenter"},"author_association":%q},`+
		`"repository":{"owner":{"login":"o"},"name":"r"},"installation":{"id":0},"sender":{"login":"commenter"}}`,
		pr, pr, body, association)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func slashRouter(d Deps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/webhook/github", WebhookGitHub(d, "s"))
	return r
}

func TestWebhook_SlashCommand_OnlyTrustedCommentersQueueWork(t *testing.T) {
	cases := []struct {
		association string
		queued      bool
	}{
		{"OWNER", true},
		{"MEMBER", true},
		{"COLLABORATOR", true},
		{"CONTRIBUTOR", false},
		{"FIRST_TIME_CONTRIBUTOR", false},
		{"FIRST_TIMER", false},
		{"NONE", false},
		{"", false},
	}
	for _, cmd := range []string{"/lgtm review", "/lgtm help", "/lgtm bogus"} {
		for i, tc := range cases {
			t.Run(cmd+"/"+tc.association, func(t *testing.T) {
				r := slashRouter(Deps{Fetcher: errFetcher{}})
				w := postSlashComment(t, r, "s", cmd, tc.association, 100+i)
				queued := w.Code == http.StatusAccepted
				if queued != tc.queued {
					t.Errorf("status=%d body=%s, want queued=%v", w.Code, w.Body.String(), tc.queued)
				}
				if !tc.queued && (w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("untrusted commenter"))) {
					t.Errorf("an ignored command should say why; status=%d body=%s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestWebhook_SlashReview_CooldownPerPR(t *testing.T) {
	r := slashRouter(Deps{Fetcher: errFetcher{}, Cache: store.NewMemoryCache(time.Minute)})

	if w := postSlashComment(t, r, "s", "/lgtm review", "OWNER", 7); w.Code != http.StatusAccepted {
		t.Fatalf("first command: status=%d body=%s, want 202", w.Code, w.Body.String())
	}
	w := postSlashComment(t, r, "s", "/lgtm review", "OWNER", 7)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("cooldown")) {
		t.Errorf("repeat on the same PR: status=%d body=%s, want 200 cooldown", w.Code, w.Body.String())
	}
	if w := postSlashComment(t, r, "s", "/lgtm review", "OWNER", 8); w.Code != http.StatusAccepted {
		t.Errorf("another PR: status=%d body=%s, want 202", w.Code, w.Body.String())
	}
}
