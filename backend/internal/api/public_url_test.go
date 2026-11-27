package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/config"
	"github.com/ecstasoy/LGTM/backend/internal/review"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

const testBase = "https://lgtm.example.com"

func TestBotLinks_UsePublicBaseURL(t *testing.T) {
	body := buildBotReviewBody("sum", "01REV", 1, "opened", testBase)
	if !strings.Contains(body, testBase+"/review/01REV") || strings.Contains(body, "lgtm-alpha") {
		t.Errorf("review link should use the configured base:\n%s", body)
	}
	sg := buildSuggestionCommentBody(review.Suggestion{Type: "bug", Title: "t", Body: "b"}, testBase)
	if !strings.Contains(sg, "[LGTM]("+testBase+")") || strings.Contains(sg, "lgtm-alpha") {
		t.Errorf("footer should link the configured base:\n%s", sg)
	}
}

func TestDepsBaseURL_FallsBackToDefault(t *testing.T) {
	if got := (Deps{}).baseURL(); got != config.DefaultPublicBaseURL {
		t.Errorf("zero Deps baseURL = %q", got)
	}
	if got := (Deps{PublicBaseURL: testBase}).baseURL(); got != testBase {
		t.Errorf("baseURL = %q", got)
	}
}

func TestSlashReview_AlreadyReviewedLinkUsesBase(t *testing.T) {
	api := &fakeGitHubAPI{}
	d, s := slashDeps(t, api, &countingProvider{})
	d.PublicBaseURL = testBase
	rec := &store.Record{ID: "01SEEN", Owner: "o", Repo: "r", PRNumber: 7, HeadSHA: "abc1234def5678", Locale: "en", Payload: json.RawMessage(`{}`)}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatal(err)
	}

	runSlashReview(d, slashArgs())

	comments, _ := api.snapshot()
	if len(comments) != 1 || !strings.Contains(comments[0], testBase+"/review/01SEEN") {
		t.Errorf("reply should link the configured base, got %q", comments)
	}
}
