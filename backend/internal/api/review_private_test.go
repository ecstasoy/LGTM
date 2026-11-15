package api

import (
	"context"
	"strings"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

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
