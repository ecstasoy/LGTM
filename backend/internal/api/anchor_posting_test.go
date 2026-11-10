package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/oauth"
	"github.com/ecstasoy/LGTM/backend/internal/review"
	"github.com/ecstasoy/LGTM/backend/internal/session"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

func TestSplitBotReviewComments(t *testing.T) {
	sgs := []review.Suggestion{
		{File: "store.go", StartLine: 13, Line: 15, Type: "bug", Title: "range", Body: "b",
			Patch: &review.Patch{Before: "a\nb\nc", After: "d"}},
		{File: "store.go", Line: 16, Type: "style", Title: "single", Body: "b"},
		{File: "store.go", Line: 30, OutsideDiff: true, Type: "bug", Title: "outside", Body: "b"},
		{File: "", Line: 0, Type: "bug", Title: "no anchor", Body: "b"},
	}
	inline, outside := splitBotReviewComments(sgs)
	if len(inline) != 2 {
		t.Fatalf("inline = %d, want 2", len(inline))
	}
	if inline[0].StartLine != 13 || inline[0].Line != 15 || inline[0].Path != "store.go" {
		t.Errorf("range comment = %+v, want store.go 13..15", inline[0])
	}
	if inline[1].StartLine != 0 || inline[1].Line != 16 {
		t.Errorf("single comment = %+v, want line 16 alone", inline[1])
	}
	if len(outside) != 2 || outside[0].Title != "outside" || outside[1].Title != "no anchor" {
		t.Errorf("outside = %+v, want the outside-diff and unanchored suggestions", outside)
	}
}

func TestBuildBotReviewBody_ListsOutsideDiffNotes(t *testing.T) {
	outside := []review.Suggestion{
		{File: "store.go", Line: 30, OutsideDiff: true, Type: "bug", Title: "Check the error", Body: "Handle err\nbefore use."},
	}
	got := buildBotReviewBody("summary", "rev123", 0, outside, "opened")
	for _, want := range []string{"Notes outside the diff", "`store.go:30`", "**Check the error**", "Handle err before use."} {
		if !strings.Contains(got, want) {
			t.Errorf("body missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "nothing stood out") {
		t.Errorf("body claims nothing stood out while listing notes:\n%s", got)
	}
	if containsCJK(got) {
		t.Errorf("body contains CJK:\n%s", got)
	}
}

// TestAdopt_OutsideDiffRejected keeps the web UI from posting a comment GitHub would reject with 422.
func TestAdopt_OutsideDiffRejected(t *testing.T) {
	st := newTestStore(t)
	sgs, _ := json.Marshal([]review.Suggestion{
		{File: "main.go", Line: 30, OutsideDiff: true, Type: "bug", Title: "t", Body: "b"},
	})
	payload, _ := json.Marshal(cachedPayload{Title: "t", Suggestions: sgs, Risks: json.RawMessage(`[]`)})
	id := store.NewID()
	if err := st.Put(context.Background(), &store.Record{
		ID: id, Owner: "o", Repo: "r", PRNumber: 1, HeadSHA: "sha", Payload: payload, CreatedAt: time.Unix(1000, 0),
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	gin.SetMode(gin.TestMode)
	d := Deps{Store: st, OAuthClient: &oauth.Client{}}
	withSession := func(h gin.HandlerFunc) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Set(sessionCtxKey, &session.Session{Login: "u", AccessToken: "tok"})
			h(c)
		}
	}
	r := gin.New()
	r.POST("/api/review/:id/comment/:idx", withSession(PostAdoptComment(d)))
	r.POST("/api/review/:id/commit/:idx", withSession(PostAdoptCommit(d)))

	for _, path := range []string{"/comment/0", "/commit/0"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/review/"+id+path, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body=%s", path, w.Code, w.Body.String())
			continue
		}
		if !strings.Contains(w.Body.String(), CodeSuggestionNoAnchor) {
			t.Errorf("%s: body missing %s: %s", path, CodeSuggestionNoAnchor, w.Body.String())
		}
	}
}
