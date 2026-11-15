package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/session"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// tokenFetchers records which tokens the handler asked a fetcher for and serves one canned result for all of them.
type tokenFetchers struct {
	mu     sync.Mutex
	tokens []string
	pr     gh.PullRequest
	err    error
}

func (f *tokenFetchers) For(token string) gh.Fetcher {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, token)
	return fakeFetcher{pr: f.pr, err: f.err}
}

func postReviewAs(t *testing.T, srv *httptest.Server, sid string) (*http.Response, string) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"url": "https://github.com/golang/go/pull/42"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/review", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: session.CookieName, Value: sid})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer res.Body.Close()
	buf, _ := io.ReadAll(res.Body)
	return res, string(buf)
}

func privatePR() gh.PullRequest {
	pr := samplePR()
	pr.Private = true
	return pr
}

func TestPostReview_AnonymousPrivateRepo_RejectedBeforeAnythingLeaks(t *testing.T) {
	s := newTestStore(t)
	idx := &stubIndexer{}
	prov := &countingProvider{inner: llm.NewMockProvider()}
	pr := samplePR()
	pr.Private = true
	srv := startTestServer(t, Deps{Fetcher: fakeFetcher{pr: pr}, Provider: prov, Store: s, Indexer: idx})

	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})

	if res.StatusCode != 404 || !strings.Contains(body, CodePRNotFound) {
		t.Fatalf("status=%d body=%s, want 404 %s", res.StatusCode, body, CodePRNotFound)
	}
	if strings.Contains(body, "event: ") {
		t.Errorf("no SSE frame may be streamed for a private PR; body=%s", body)
	}
	if prov.calls.Load() != 0 {
		t.Errorf("provider called %d times, want 0", prov.calls.Load())
	}
	if idx.calls != 0 {
		t.Errorf("indexer called %d times, want 0", idx.calls)
	}
	if rec, _ := s.Get(context.Background(), "golang", "go", 42, "deadbeef", store.DefaultLocale); rec != nil {
		t.Errorf("private PR review was stored as public record %s", rec.ID)
	}
}

func TestPostReview_SignedIn_FetchesWithOwnTokenAndOwnsTheRecord(t *testing.T) {
	s := newTestStore(t)
	sm := session.New(nil, 0)
	user := &tokenFetchers{pr: privatePR()}
	srv := startTestServer(t, Deps{
		Fetcher:    fakeFetcher{err: gh.ErrPRNotFound}, // the server token cannot see the repo
		FetcherFor: user.For,
		Provider:   dualMockProvider{textReply: "ok", jsonReply: `{"risks":[],"suggestions":[]}`},
		Store:      s,
		Sessions:   sm,
	})

	res, body := postReviewAs(t, srv, loginAs(t, sm, "alice"))

	if res.StatusCode != 200 || !strings.Contains(body, "event: review_id") {
		t.Fatalf("status=%d body=%s, want a completed review", res.StatusCode, body)
	}
	if len(user.tokens) != 1 || user.tokens[0] != "tok" {
		t.Errorf("fetched with tokens %v, want [tok]", user.tokens)
	}
	if rec, _ := s.Get(context.Background(), "golang", "go", 42, "deadbeef", store.DefaultLocale); rec != nil {
		t.Errorf("private review stored as public record %s", rec.ID)
	}
	alice := "alice"
	if recs, _ := s.List(context.Background(), &alice, 10); len(recs) != 1 {
		t.Errorf("alice owns %d records, want 1", len(recs))
	}
}

func TestPostReview_SignedInWithoutAccess_SameNotFoundAsMissingPR(t *testing.T) {
	sm := session.New(nil, 0)
	srv := startTestServer(t, Deps{
		Fetcher:    fakeFetcher{pr: privatePR()}, // a wide server token must not be a back door
		FetcherFor: (&tokenFetchers{err: gh.ErrPRNotFound}).For,
		Provider:   llm.NewMockProvider(),
		Sessions:   sm,
	})

	res, body := postReviewAs(t, srv, loginAs(t, sm, "bob"))

	if res.StatusCode != 404 || !strings.Contains(body, CodePRNotFound) {
		t.Errorf("status=%d body=%s, want 404 %s", res.StatusCode, body, CodePRNotFound)
	}
}

func TestPostReview_SignedInTokenRejected_PublicRepoFallsBackToServerToken(t *testing.T) {
	sm := session.New(nil, 0)
	srv := startTestServer(t, Deps{
		Fetcher:    fakeFetcher{pr: samplePR()},
		FetcherFor: (&tokenFetchers{err: gh.ErrAccessDenied}).For, // e.g. an expired user token
		Provider:   dualMockProvider{textReply: "ok", jsonReply: `{"risks":[],"suggestions":[]}`},
		Sessions:   sm,
	})

	res, body := postReviewAs(t, srv, loginAs(t, sm, "carol"))

	if res.StatusCode != 200 || !strings.Contains(body, "event: done") {
		t.Errorf("status=%d body=%s, want the public PR reviewed", res.StatusCode, body)
	}
}
