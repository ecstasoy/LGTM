package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
)

// blockingFetcher holds every fetch until unblock closes, so a test can keep review slots occupied.
type blockingFetcher struct {
	started chan struct{}
	unblock chan struct{}
}

func (f blockingFetcher) Fetch(ctx context.Context, _ string) (gh.PullRequest, error) {
	f.started <- struct{}{}
	select {
	case <-f.unblock:
	case <-ctx.Done():
	}
	return gh.PullRequest{}, errors.New("stub")
}

func postPREvent(t *testing.T, r *gin.Engine, number int) *httptest.ResponseRecorder {
	t.Helper()
	payload := fmt.Appendf(nil, `{"action":"opened","number":%d,`+
		`"pull_request":{"html_url":"https://github.com/o/r/pull/%d","title":"t","user":{"login":"author"}},`+
		`"repository":{"owner":{"login":"o"},"name":"r"},"installation":{"id":0},"sender":{"login":"author"}}`,
		number, number)
	mac := hmac.New(sha256.New, []byte("s"))
	mac.Write(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestWebhook_ConcurrencyCap_DropsWorkWhenBusy(t *testing.T) {
	f := blockingFetcher{started: make(chan struct{}, 4), unblock: make(chan struct{})}
	l := NewWebhookLimiter(WebhookLimits{MaxConcurrent: 1})
	r := slashRouter(Deps{Fetcher: f, WebhookLimiter: l})

	if w := postPREvent(t, r, 1); w.Code != http.StatusAccepted {
		t.Fatalf("first event: status=%d body=%s, want 202", w.Code, w.Body.String())
	}
	<-f.started // the first review now holds the only slot

	if w := postPREvent(t, r, 2); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("busy")) {
		t.Errorf("pull_request while busy: status=%d body=%s, want 200 busy", w.Code, w.Body.String())
	}
	if w := postSlashComment(t, r, "s", "/lgtm review", "OWNER", 3); w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("busy")) {
		t.Errorf("/lgtm review while busy: status=%d body=%s, want 200 busy", w.Code, w.Body.String())
	}

	close(f.unblock)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if release, ok := l.TryAcquire(); ok {
			release()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the finished review never gave its slot back")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if w := postPREvent(t, r, 4); w.Code != http.StatusAccepted {
		t.Errorf("after the slot frees: status=%d body=%s, want 202", w.Code, w.Body.String())
	}
}
