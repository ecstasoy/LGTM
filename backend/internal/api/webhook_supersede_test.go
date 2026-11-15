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
	err := postBotReview(context.Background(), botClient(t, api), 99, reviewedPR, "rid", "sum", json.RawMessage(`[]`), "synchronize")
	if !errors.Is(err, errStaleHead) {
		t.Fatalf("want errStaleHead, got %v", err)
	}
	if _, reviews := api.snapshot(); reviews != 0 {
		t.Errorf("posted %d reviews for a stale commit", reviews)
	}
}

func TestPostBotReview_PostsWhenStillHead(t *testing.T) {
	api := &fakeGitHubAPI{head: "old0000"}
	if err := postBotReview(context.Background(), botClient(t, api), 99, reviewedPR, "rid", "sum", json.RawMessage(`[]`), "synchronize"); err != nil {
		t.Fatal(err)
	}
	if _, reviews := api.snapshot(); reviews != 1 {
		t.Errorf("want 1 review, got %d", reviews)
	}
}

// If GitHub cannot tell us the head, posting beats silently dropping a finished review.
func TestPostBotReview_PostsWhenHeadUnknown(t *testing.T) {
	api := &fakeGitHubAPI{}
	if err := postBotReview(context.Background(), botClient(t, api), 99, reviewedPR, "rid", "sum", json.RawMessage(`[]`), "synchronize"); err != nil {
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
