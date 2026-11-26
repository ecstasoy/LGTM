package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/i18n"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/oauth"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

func botClient(t *testing.T, api *fakeGitHubAPI) *oauth.Client {
	t.Helper()
	return &oauth.Client{AppID: 1, PrivateKeyPEM: testAppKey(t), HTTPClient: &http.Client{Transport: api}}
}

var reviewedPR = gh.PullRequest{Owner: "o", Repo: "r", Number: 7, HeadSHA: "old0000"}

// A review that finishes after a newer push must not land on the PR as if it were current.
func TestPostBotReview_SkipsWhenHeadMoved(t *testing.T) {
	api := &fakeGitHubAPI{head: "new1111"}
	err := postBotReview(context.Background(), botClient(t, api), 99, reviewedPR, "rid", "sum", json.RawMessage(`[]`), "synchronize", testBase)
	if !errors.Is(err, errStaleHead) {
		t.Fatalf("want errStaleHead, got %v", err)
	}
	if _, reviews := api.snapshot(); reviews != 0 {
		t.Errorf("posted %d reviews for a stale commit", reviews)
	}
}

func TestPostBotReview_PostsWhenStillHead(t *testing.T) {
	api := &fakeGitHubAPI{head: "old0000"}
	if err := postBotReview(context.Background(), botClient(t, api), 99, reviewedPR, "rid", "sum", json.RawMessage(`[]`), "synchronize", testBase); err != nil {
		t.Fatal(err)
	}
	if _, reviews := api.snapshot(); reviews != 1 {
		t.Errorf("want 1 review, got %d", reviews)
	}
}

// If GitHub cannot tell us the head, posting beats silently dropping a finished review.
func TestPostBotReview_PostsWhenHeadUnknown(t *testing.T) {
	api := &fakeGitHubAPI{}
	if err := postBotReview(context.Background(), botClient(t, api), 99, reviewedPR, "rid", "sum", json.RawMessage(`[]`), "synchronize", testBase); err != nil {
		t.Fatal(err)
	}
	if _, reviews := api.snapshot(); reviews != 1 {
		t.Errorf("want 1 review, got %d", reviews)
	}
}

func TestClaimReview_OneRunPerCommit(t *testing.T) {
	d := Deps{Cache: store.NewMemoryCache(time.Minute), DefaultLocale: i18n.EN}
	ctx := context.Background()

	release, ok := claimReview(ctx, d, reviewedPR)
	if !ok {
		t.Fatal("first claim should win")
	}
	if _, again := claimReview(ctx, d, reviewedPR); again {
		t.Fatal("second claim on the same commit must lose")
	}
	other := reviewedPR
	other.HeadSHA = "new1111"
	if _, ok := claimReview(ctx, d, other); !ok {
		t.Fatal("a different commit is a separate claim")
	}
	release()
	if _, ok := claimReview(ctx, d, reviewedPR); !ok {
		t.Fatal("released commit should be claimable again")
	}
}

func TestClaimReview_NoCacheAlwaysWins(t *testing.T) {
	if _, ok := claimReview(context.Background(), Deps{}, reviewedPR); !ok {
		t.Fatal("without a cache there is nothing to coordinate on")
	}
}

// A duplicate delivery or a push racing /lgtm review must not review the same commit twice.
func TestWebhookReview_SkipsWhileSameCommitClaimed(t *testing.T) {
	api := &fakeGitHubAPI{}
	prov := &countingProvider{inner: llm.NewMockProvider()}
	d, _ := slashDeps(t, api, prov)
	pr, _ := d.Fetcher.Fetch(context.Background(), "")
	if _, ok := claimReview(context.Background(), d, pr); !ok {
		t.Fatal("setup claim failed")
	}

	runWebhookReview(d, webhookReviewArgs{PrURL: "u", Owner: "o", Repo: "r", Number: 7, InstallationID: 99, TriggerAction: "synchronize"})

	if prov.calls.Load() != 0 {
		t.Errorf("model called %d times for a commit already being reviewed", prov.calls.Load())
	}
}

func TestSlashReview_CommitInProgress_SaysSo(t *testing.T) {
	api := &fakeGitHubAPI{}
	prov := &countingProvider{inner: llm.NewMockProvider()}
	d, _ := slashDeps(t, api, prov)
	pr, _ := d.Fetcher.Fetch(context.Background(), "")
	if _, ok := claimReview(context.Background(), d, pr); !ok {
		t.Fatal("setup claim failed")
	}

	runSlashReview(d, slashArgs())

	comments, _ := api.snapshot()
	if len(comments) != 1 || !strings.Contains(comments[0], "already running") || !strings.Contains(comments[0], "`abc1234`") {
		t.Errorf("want one in-progress reply, got %q", comments)
	}
	if prov.calls.Load() != 0 {
		t.Errorf("no model calls expected")
	}
}

func TestPRRuns_NewerCommitCancelsOlder(t *testing.T) {
	runs := newPRRuns()
	ctxA, doneA := runs.start(context.Background(), "o/r#7", "aaa")
	ctxB, doneB := runs.start(context.Background(), "o/r#7", "bbb")

	if !errors.Is(context.Cause(ctxA), errSuperseded) {
		t.Fatalf("older run should be cancelled as superseded, cause=%v", context.Cause(ctxA))
	}
	if ctxB.Err() != nil {
		t.Fatal("newer run must keep going")
	}
	doneA() // the old run finishing late must not deregister the new one
	ctxC, doneC := runs.start(context.Background(), "o/r#7", "ccc")
	if !errors.Is(context.Cause(ctxB), errSuperseded) {
		t.Fatal("B should still have been tracked, and now superseded by C")
	}
	doneB()
	doneC()
	if ctxC.Err() == nil {
		t.Fatal("done should release the run's context")
	}
	ctxD, doneD := runs.start(context.Background(), "o/r#8", "ddd")
	defer doneD()
	if _, doneE := runs.start(context.Background(), "o/r#9", "eee"); ctxD.Err() != nil {
		t.Fatal("another PR's run must not cancel this one")
	} else {
		doneE()
	}
}

// blockingProvider holds every stream open until its context ends.
type blockingProvider struct{ started chan struct{} }

func (p *blockingProvider) Stream(ctx context.Context, _ llm.Request) (<-chan llm.Chunk, error) {
	select {
	case p.started <- struct{}{}:
	default:
	}
	ch := make(chan llm.Chunk, 1)
	go func() {
		<-ctx.Done()
		ch <- llm.Chunk{Err: ctx.Err()}
		close(ch)
	}()
	return ch, nil
}

func TestWebhookReview_NewerPushCancelsOlderRun(t *testing.T) {
	api := &fakeGitHubAPI{}
	block := &blockingProvider{started: make(chan struct{}, 1)}
	dOld, s := slashDeps(t, api, block)
	dOld.Fetcher = fakeFetcher{pr: gh.PullRequest{Owner: "o", Repo: "r", Number: 70, HeadSHA: "old0000",
		Files: []gh.File{{Path: "a.go", Status: "modified", Patch: "@@ -1 +1 @@\n-a\n+b"}}}}
	args := webhookReviewArgs{PrURL: "u", Owner: "o", Repo: "r", Number: 70, InstallationID: 99, TriggerAction: "synchronize"}

	finished := make(chan struct{})
	go func() { runWebhookReview(dOld, args); close(finished) }()
	select {
	case <-block.started:
	case <-time.After(5 * time.Second):
		t.Fatal("old run never reached the model")
	}

	dNew := dOld
	dNew.Fetcher = fakeFetcher{pr: gh.PullRequest{Owner: "o", Repo: "r", Number: 70, HeadSHA: "new1111",
		Files: []gh.File{{Path: "a.go", Status: "modified", Patch: "@@ -1 +1 @@\n-a\n+c"}}}}
	dNew.Provider = llm.NewMockProvider()
	runWebhookReview(dNew, args)

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("old run was not cancelled by the newer push")
	}
	if rec, _ := s.Get(context.Background(), "o", "r", 70, "old0000", "en"); rec != nil {
		t.Error("a superseded run must not persist a review")
	}
}
