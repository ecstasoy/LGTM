package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/i18n"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/oauth"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// fakeGitHubAPI answers the App token exchange and records every issue comment the bot posts.
type fakeGitHubAPI struct {
	mu       sync.Mutex
	comments []string
	reviews  int
	head     string // what GET /pulls/{n} reports as the head commit; empty answers 404
}

func (f *fakeGitHubAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body := `{}`
	status := http.StatusCreated
	switch {
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/"):
		status, body = http.StatusOK, `{"head":{"sha":"`+f.head+`"}}`
		if f.head == "" {
			status, body = http.StatusNotFound, `{}`
		}
	case strings.HasSuffix(r.URL.Path, "/access_tokens"):
		body = `{"token":"inst-token"}`
	case strings.HasSuffix(r.URL.Path, "/comments"):
		var p struct {
			Body string `json:"body"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &p)
		f.comments = append(f.comments, p.Body)
		body = `{"id":1}`
	case strings.HasSuffix(r.URL.Path, "/reviews"):
		f.reviews++
		status, body = http.StatusOK, `{"id":1}`
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

func (f *fakeGitHubAPI) snapshot() ([]string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.comments...), f.reviews
}

func testAppKey(t *testing.T) []byte {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

func slashDeps(t *testing.T, api *fakeGitHubAPI, p llm.Provider) (Deps, store.Store) {
	t.Helper()
	s, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	pr := gh.PullRequest{
		Owner: "o", Repo: "r", Number: 7, HeadSHA: "abc1234def5678", Title: "t",
		Files: []gh.File{{Path: "a.go", Status: "modified", Patch: "@@ -1 +1 @@\n-a\n+b", Additions: 1, Deletions: 1}},
	}
	return Deps{
		Fetcher:       fakeFetcher{pr: pr},
		Provider:      p,
		Store:         s,
		Cache:         store.NewMemoryCache(time.Minute),
		DefaultLocale: i18n.EN,
		OAuthClient: &oauth.Client{
			AppID: 1, PrivateKeyPEM: testAppKey(t), HTTPClient: &http.Client{Transport: api},
		},
	}, s
}

func slashArgs() slashReviewArgs {
	return slashReviewArgs{
		PrURL: "https://github.com/o/r/pull/7", Owner: "o", Repo: "r", Number: 7,
		InstallationID: 99, SenderLogin: "alice", PRAuthorLogin: "alice",
	}
}

// /lgtm review on a commit that was already reviewed used to ack "reviewing now" and then post nothing.
func TestSlashReview_AlreadyReviewedCommit_RepliesWithLink(t *testing.T) {
	api := &fakeGitHubAPI{}
	prov := &countingProvider{inner: llm.NewMockProvider()}
	d, s := slashDeps(t, api, prov)
	rec := &store.Record{ID: "01REVIEWEDALREADY", Owner: "o", Repo: "r", PRNumber: 7, HeadSHA: "abc1234def5678", Locale: "en", Payload: json.RawMessage(`{}`)}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatal(err)
	}

	runSlashReview(d, slashArgs())

	comments, reviews := api.snapshot()
	if len(comments) != 1 {
		t.Fatalf("want exactly one reply, got %d: %q", len(comments), comments)
	}
	if strings.Contains(comments[0], "reviewing now") {
		t.Errorf("must not promise a review that will not come: %q", comments[0])
	}
	for _, want := range []string{"`abc1234`", "/review/01REVIEWEDALREADY", "Push a new commit"} {
		if !strings.Contains(comments[0], want) {
			t.Errorf("reply should mention %q: %q", want, comments[0])
		}
	}
	if reviews != 0 || prov.calls.Load() != 0 {
		t.Errorf("no new review expected: reviews=%d model calls=%d", reviews, prov.calls.Load())
	}
}

func TestSlashReview_NewCommit_AcksThenRunsReview(t *testing.T) {
	api := &fakeGitHubAPI{}
	prov := &countingProvider{inner: llm.NewMockProvider()}
	d, _ := slashDeps(t, api, prov)

	runSlashReview(d, slashArgs())

	comments, _ := api.snapshot()
	if len(comments) != 1 || !strings.Contains(comments[0], "reviewing now") {
		t.Errorf("want the reviewing-now ack, got %q", comments)
	}
	if prov.calls.Load() == 0 {
		t.Errorf("a new commit should run the review stages")
	}
}

// A fetch failure used to follow the "reviewing now" ack and then go silent.
func TestSlashReview_FetchFails_SaysSo(t *testing.T) {
	api := &fakeGitHubAPI{}
	prov := &countingProvider{inner: llm.NewMockProvider()}
	d, _ := slashDeps(t, api, prov)
	d.Fetcher = fakeFetcher{err: errors.New("github 502")}

	runSlashReview(d, slashArgs())

	comments, _ := api.snapshot()
	if len(comments) != 1 || strings.Contains(comments[0], "reviewing now") || !strings.Contains(comments[0], "couldn't load") {
		t.Errorf("want a single failure reply, got %q", comments)
	}
	if prov.calls.Load() != 0 {
		t.Errorf("no model calls expected")
	}
}
