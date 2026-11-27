package api

import (
	"testing"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
)

// webhookPrivatePR is a PR only the App installation can read; the server token gets 404.
func webhookPrivatePR() gh.PullRequest {
	return gh.PullRequest{
		Owner: "o", Repo: "r", Number: 7, HeadSHA: "abc1234def5678", Title: "t", Private: true,
		Files: []gh.File{{Path: "a.go", Status: "modified", Patch: "@@ -1 +1 @@\n-a\n+b", Additions: 1, Deletions: 1}},
	}
}

func webhookArgs() webhookReviewArgs {
	return webhookReviewArgs{
		PrURL: "https://github.com/o/r/pull/7", Owner: "o", Repo: "r", Number: 7,
		InstallationID: 99, SenderLogin: "alice", PRAuthorLogin: "alice", TriggerAction: "opened",
	}
}

func TestWebhookReview_FetchesWithInstallationToken(t *testing.T) {
	prov := &countingProvider{inner: llm.NewMockProvider()}
	d, _ := slashDeps(t, &fakeGitHubAPI{}, prov)
	d.Fetcher = fakeFetcher{err: gh.ErrPRNotFound}
	inst := &tokenFetchers{pr: webhookPrivatePR()}
	d.FetcherFor = inst.For

	runWebhookReview(d, webhookArgs())

	if len(inst.tokens) != 1 || inst.tokens[0] != "inst-token" {
		t.Errorf("fetched with tokens %v, want [inst-token]", inst.tokens)
	}
	if prov.calls.Load() == 0 {
		t.Error("the private PR should have been reviewed")
	}
}

func TestSlashReview_FetchesWithInstallationToken(t *testing.T) {
	api := &fakeGitHubAPI{}
	prov := &countingProvider{inner: llm.NewMockProvider()}
	d, _ := slashDeps(t, api, prov)
	d.Fetcher = fakeFetcher{err: gh.ErrPRNotFound}
	inst := &tokenFetchers{pr: webhookPrivatePR()}
	d.FetcherFor = inst.For

	runSlashReview(d, slashArgs())

	if len(inst.tokens) != 1 || inst.tokens[0] != "inst-token" {
		t.Errorf("fetched with tokens %v, want [inst-token]", inst.tokens)
	}
	if prov.calls.Load() == 0 {
		t.Error("the private PR should have been reviewed")
	}
}

func TestWebhookReview_AppNotConfigured_UsesServerToken(t *testing.T) {
	prov := &countingProvider{inner: llm.NewMockProvider()}
	d, _ := slashDeps(t, &fakeGitHubAPI{}, prov)
	d.OAuthClient = nil
	inst := &tokenFetchers{err: gh.ErrPRNotFound}
	d.FetcherFor = inst.For

	runWebhookReview(d, webhookArgs())

	if len(inst.tokens) != 0 {
		t.Errorf("no installation token is available, yet fetched with %v", inst.tokens)
	}
	if prov.calls.Load() == 0 {
		t.Error("the server-token fetch should still review the PR")
	}
}
