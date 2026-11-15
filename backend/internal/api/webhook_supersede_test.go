package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/oauth"
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
