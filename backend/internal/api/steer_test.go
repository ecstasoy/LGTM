package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// seedSteerReview 写一条 cached review 给 steer 测试用：files 非空 + 完整 meta
func seedSteerReview(t *testing.T, s store.Store) string {
	t.Helper()
	payload, _ := json.Marshal(cachedPayload{
		Title:   "fix race",
		BaseRef: "main", HeadRef: "fix/race",
		Stats:   gh.Stats{Files: 1, Additions: 5, Deletions: 2},
		Files: []gh.File{
			{Path: "main.go", Status: "modified", Patch: "@@ -1,3 +1,5 @@\n+x := 1\n", Additions: 5, Deletions: 2},
		},
		Summary:     "s",
		Risks:       json.RawMessage(`[]`),
		Suggestions: json.RawMessage(`[]`),
	})
	id := store.NewID()
	rec := &store.Record{
		ID: id, Owner: "o", Repo: "r", PRNumber: 1, HeadSHA: "sha",
		Payload: payload, CreatedAt: time.Unix(1000, 0),
	}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatalf("put: %v", err)
	}
	return id
}

func TestSteer_NoStore_503(t *testing.T) {
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: nil})
	res, _ := postJSON(t, srv, "/api/review/anything/steer", map[string]string{"text": "重点看并发"})
	if res.StatusCode != 503 {
		t.Errorf("want 503 got %d", res.StatusCode)
	}
}

func TestSteer_MissingText_400(t *testing.T) {
	s := newTestStore(t)
	id := seedSteerReview(t, s)
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := postJSON(t, srv, "/api/review/"+id+"/steer", map[string]string{"text": "  "})
	if res.StatusCode != 400 || !strings.Contains(body, "text is required") {
		t.Errorf("want 400 text required, got %d %s", res.StatusCode, body)
	}
}

func TestSteer_InvalidStage_400(t *testing.T) {
	s := newTestStore(t)
	id := seedSteerReview(t, s)
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := postJSON(t, srv, "/api/review/"+id+"/steer",
		map[string]string{"text": "x", "stage": "summary"})
	if res.StatusCode != 400 || !strings.Contains(body, "stage must be one of") {
		t.Errorf("want 400 invalid stage, got %d %s", res.StatusCode, body)
	}
}

func TestSteer_NotFound_404(t *testing.T) {
	s := newTestStore(t)
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, _ := postJSON(t, srv, "/api/review/missing/steer", map[string]string{"text": "x"})
	if res.StatusCode != 404 {
		t.Errorf("want 404 got %d", res.StatusCode)
	}
}

func TestSteer_RisksDefault_EmitsSteeredRisksDone(t *testing.T) {
	s := newTestStore(t)
	id := seedSteerReview(t, s)
	p := llm.NewMockProvider()
	p.Reply = `{"risks":[{"file":"main.go","line":3,"severity":"high","category":"concurrency","confidence":0.92,"reason":"并发写无锁"}]}`
	srv := startTestServer(t, Deps{Provider: p, Store: s})
	// 不指定 stage → 默认 risks
	res, body := postJSON(t, srv, "/api/review/"+id+"/steer", map[string]string{"text": "重点看并发安全"})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	frames := parseSSE(body)
	var sawInfo, sawSteered, sawDone bool
	for _, f := range frames {
		switch f.Type {
		case "info":
			sawInfo = true
		case "steered_risks_done":
			sawSteered = true
		case "done":
			sawDone = true
		}
	}
	if !sawInfo || !sawSteered || !sawDone {
		t.Errorf("missing frames: info=%v steered_risks_done=%v done=%v\nbody=%s",
			sawInfo, sawSteered, sawDone, body)
	}
}

func TestSteer_Suggestions_EmitsSteeredSuggestionsDone(t *testing.T) {
	s := newTestStore(t)
	id := seedSteerReview(t, s)
	p := llm.NewMockProvider()
	p.Reply = `{"suggestions":[{"file":"main.go","line":3,"type":"concurrency","title":"加锁","body":"对共享 map 加 sync.Mutex"}]}`
	srv := startTestServer(t, Deps{Provider: p, Store: s})
	res, body := postJSON(t, srv, "/api/review/"+id+"/steer",
		map[string]string{"text": "建议把锁改细", "stage": "suggestions"})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	if !strings.Contains(body, "steered_suggestions_done") {
		t.Errorf("expected steered_suggestions_done in body, got %s", body)
	}
}
