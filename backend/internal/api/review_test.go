package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// fakeFetcher 让测试注入预设的 PR 数据或错误。
type fakeFetcher struct {
	pr  gh.PullRequest
	err error
}

func (f fakeFetcher) Fetch(_ context.Context, _ string) (gh.PullRequest, error) {
	return f.pr, f.err
}

// startTestServer 起一个真实 httptest server；gin.c.Stream 在 ResponseRecorder 下会因 CloseNotify panic。
// 自动补默认 Builder，免每个测试都填。
func startTestServer(t *testing.T, deps Deps) *httptest.Server {
	t.Helper()
	if deps.Builder == nil {
		deps.Builder = prctx.NewLayeredBuilder()
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r, deps)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// countingProvider 包装一个 Provider 并记录 Stream 调用次数，验证缓存命中是否真的跳过 LLM
type countingProvider struct {
	inner llm.Provider
	calls atomic.Int32
}

func (p *countingProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	p.calls.Add(1)
	return p.inner.Stream(ctx, req)
}

func getJSON(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	res, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("do GET: %v", err)
	}
	defer res.Body.Close()
	buf, _ := io.ReadAll(res.Body)
	return res, string(buf)
}

func postJSON(t *testing.T, srv *httptest.Server, path string, body any) (*http.Response, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer res.Body.Close()
	buf, _ := io.ReadAll(res.Body)
	return res, string(buf)
}

type sseFrame struct {
	Type string
	Data string
}

func parseSSE(body string) []sseFrame {
	var frames []sseFrame
	for _, raw := range strings.Split(body, "\n\n") {
		if raw == "" {
			continue
		}
		var f sseFrame
		for _, line := range strings.Split(raw, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				f.Type = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				f.Data += strings.TrimPrefix(line, "data: ")
			}
		}
		if f.Type != "" {
			frames = append(frames, f)
		}
	}
	return frames
}

// dualMockProvider 按 req.JSONSchema 是否非 nil 切换 reply。
type dualMockProvider struct {
	textReply string
	jsonReply string
}

func (d dualMockProvider) Stream(_ context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	reply := d.textReply
	if req.JSONSchema != nil {
		reply = d.jsonReply
	}
	ch := make(chan llm.Chunk, 8)
	go func() {
		defer close(ch)
		for _, w := range strings.Fields(reply) {
			ch <- llm.Chunk{Text: w + " "}
		}
		ch <- llm.Chunk{Done: true}
	}()
	return ch, nil
}

func TestPostReview_Success(t *testing.T) {
	mock := llm.NewMockProvider()
	mock.Reply = "这是一个测试评审 总结"

	srv := startTestServer(t, Deps{
		Fetcher: fakeFetcher{
			pr: gh.PullRequest{
				Owner:   "golang",
				Repo:    "go",
				Number:  42,
				HeadSHA: "deadbeef",
				Title:   "fix race",
				Body:    "fixes #123",
				Files: []gh.File{
					{Path: "scanner.go", Status: "modified", Patch: "@@ -1 +1 @@\n-old\n+new"},
				},
			},
		},
		Provider: mock,
	})

	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})

	if res.StatusCode != 200 {
		t.Fatalf("status=%d, want 200; body=%s", res.StatusCode, body)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Errorf("Content-Type 应是 SSE，得到 %q", res.Header.Get("Content-Type"))
	}

	frames := parseSSE(body)
	if len(frames) == 0 {
		t.Fatal("SSE 没有任何帧")
	}

	if frames[0].Type != "pr" {
		t.Errorf("首帧应是 pr，得到 %q", frames[0].Type)
	}
	var prData map[string]any
	if err := json.Unmarshal([]byte(frames[0].Data), &prData); err != nil {
		t.Fatalf("解析 pr data: %v", err)
	}
	if prData["id"] != "deadbeef" {
		t.Errorf("pr.id=%v，期望 deadbeef", prData["id"])
	}
	if prData["title"] != "fix race" {
		t.Errorf("pr.title=%v", prData["title"])
	}

	var summary strings.Builder
	for _, f := range frames {
		if f.Type != "summary_delta" {
			continue
		}
		var p struct {
			Delta string `json:"delta"`
		}
		_ = json.Unmarshal([]byte(f.Data), &p)
		summary.WriteString(p.Delta)
	}
	if !strings.Contains(summary.String(), "测试") || !strings.Contains(summary.String(), "评审") {
		t.Errorf("summary 应含 mock 关键词，得到 %q", summary.String())
	}

	if frames[len(frames)-1].Type != "done" {
		t.Errorf("末帧应是 done，得到 %q", frames[len(frames)-1].Type)
	}
}

func TestPostReview_SuccessWithRisks(t *testing.T) {
	srv := startTestServer(t, Deps{
		Fetcher: fakeFetcher{
			pr: gh.PullRequest{
				Owner: "golang", Repo: "go", Number: 42, HeadSHA: "deadbeef", Title: "fix race",
				Files: []gh.File{{Path: "scanner.go", Status: "modified", Patch: "@@ -1 +1 @@"}},
			},
		},
		Provider: dualMockProvider{
			textReply: "这是一段总结",
			jsonReply: `{"risks":[{"file":"scanner.go","line":42,"severity":"high","category":"bug","confidence":0.92,"reason":"竞态"}]}`,
		},
	})

	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d, want 200; body=%s", res.StatusCode, body)
	}

	frames := parseSSE(body)

	var risksFrame *sseFrame
	for i := range frames {
		if frames[i].Type == "risks_done" {
			risksFrame = &frames[i]
			break
		}
	}
	if risksFrame == nil {
		t.Fatal("缺 risks_done 帧")
	}
	var risks []struct {
		File       string  `json:"file"`
		Severity   string  `json:"severity"`
		Confidence float32 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(risksFrame.Data), &risks); err != nil {
		t.Fatalf("解析 risks_done: %v", err)
	}
	if len(risks) != 1 || risks[0].Severity != "high" || risks[0].Confidence < 0.91 || risks[0].Confidence > 0.93 {
		t.Errorf("risks 字段错: %+v", risks)
	}
}

func TestPostReview_InvalidJSON(t *testing.T) {
	srv := startTestServer(t, Deps{Fetcher: fakeFetcher{}, Provider: llm.NewMockProvider()})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/review", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Errorf("status=%d, want 400", res.StatusCode)
	}
}

func TestPostReview_EmptyURL(t *testing.T) {
	srv := startTestServer(t, Deps{Fetcher: fakeFetcher{}, Provider: llm.NewMockProvider()})
	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": ""})
	if res.StatusCode != 400 {
		t.Errorf("status=%d, want 400; body=%s", res.StatusCode, body)
	}
}

func TestPostReview_InvalidURL(t *testing.T) {
	srv := startTestServer(t, Deps{
		Fetcher:  fakeFetcher{err: gh.ErrInvalidPRURL},
		Provider: llm.NewMockProvider(),
	})
	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://gitlab.com/foo/bar/pull/1"})
	if res.StatusCode != 400 {
		t.Errorf("status=%d, want 400; body=%s", res.StatusCode, body)
	}
}

func TestPostReview_FetcherError(t *testing.T) {
	srv := startTestServer(t, Deps{
		Fetcher:  fakeFetcher{err: errors.New("github 504")},
		Provider: llm.NewMockProvider(),
	})
	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/o/r/pull/1"})
	if res.StatusCode != 502 {
		t.Errorf("status=%d, want 502; body=%s", res.StatusCode, body)
	}
}

// newTestStore 起内存 SQLite 用于 cache 测试
func newTestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	s, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func samplePR() gh.PullRequest {
	return gh.PullRequest{
		Owner: "golang", Repo: "go", Number: 42, HeadSHA: "deadbeef", Title: "fix race",
		Files: []gh.File{{Path: "scanner.go", Status: "modified", Patch: "@@ -1 +1 @@"}},
	}
}

// samplePRWithMeta 在 samplePR 基础上填齐 A1+A2 引入的所有 PR meta 字段，供透字段测试用。
func samplePRWithMeta() gh.PullRequest {
	p := samplePR()
	p.Author = "lin-mei"
	p.AuthorRole = "CONTRIBUTOR"
	p.State = gh.StateOpen
	p.Labels = []string{"bug", "needs-review"}
	p.BaseRef = "main"
	p.HeadRef = "fix/scanner-race"
	p.CreatedAt = time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	p.Stats = gh.Stats{Files: 5, Additions: 96, Deletions: 41, Commits: 4, Comments: 7}
	p.CI = gh.CIStatusPassing
	p.Checks = []gh.Check{
		{Name: "build", Status: gh.CIStatusPassing, DurationMS: 24100, Note: "82.4% (-0.3%)"},
		{Name: "test", Status: gh.CIStatusPassing, DurationMS: 61300},
	}
	return p
}

func TestPostReview_CacheMiss_PersistsResult(t *testing.T) {
	s := newTestStore(t)
	prov := &countingProvider{inner: dualMockProvider{
		textReply: "缓存测试 摘要",
		jsonReply: `{"risks":[{"file":"a.go","line":1,"severity":"low","category":"style","confidence":0.5,"reason":"ok"}],"suggestions":[]}`,
	}}
	srv := startTestServer(t, Deps{Fetcher: fakeFetcher{pr: samplePR()}, Provider: prov, Store: s})

	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	if prov.calls.Load() == 0 {
		t.Error("cache miss 应跑 stage，调用 Provider")
	}

	rec, err := s.Get(context.Background(), "golang", "go", 42, "deadbeef")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if rec == nil {
		t.Fatal("缓存应被写入；得到 nil")
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Payload, &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if !strings.Contains(payload["summary"].(string), "缓存测试") {
		t.Errorf("summary 缺关键词: %v", payload["summary"])
	}
	if _, ok := payload["risks"]; !ok {
		t.Errorf("缓存缺 risks 字段: %v", payload)
	}
}

func TestPostReview_CacheHit_SkipsStages(t *testing.T) {
	s := newTestStore(t)
	// 预置一条缓存
	cached, _ := json.Marshal(map[string]any{
		"summary":     "回放的总结内容",
		"risks":       json.RawMessage(`[{"file":"x.go","line":1,"severity":"high","category":"bug","confidence":0.9,"reason":"cached"}]`),
		"suggestions": json.RawMessage(`[]`),
	})
	if err := s.Put(context.Background(), &store.Record{
		ID: store.NewID(), Owner: "golang", Repo: "go", PRNumber: 42, HeadSHA: "deadbeef",
		Payload: cached,
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	prov := &countingProvider{inner: llm.NewMockProvider()}
	srv := startTestServer(t, Deps{Fetcher: fakeFetcher{pr: samplePR()}, Provider: prov, Store: s})

	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	if prov.calls.Load() != 0 {
		t.Errorf("cache hit 应跳过 LLM，调用次数 = %d", prov.calls.Load())
	}

	frames := parseSSE(body)
	var (
		sawSummary, sawRisks, sawDone bool
		summaryText                   strings.Builder
	)
	for _, f := range frames {
		switch f.Type {
		case "summary_delta":
			sawSummary = true
			var p struct {
				Delta string `json:"delta"`
			}
			_ = json.Unmarshal([]byte(f.Data), &p)
			summaryText.WriteString(p.Delta)
		case "risks_done":
			sawRisks = true
			if !strings.Contains(f.Data, "cached") {
				t.Errorf("risks 应是缓存内容，得到 %s", f.Data)
			}
		case "done":
			sawDone = true
		}
	}
	if !sawSummary || !strings.Contains(summaryText.String(), "回放的总结内容") {
		t.Errorf("summary_delta 应回放缓存文本，得到 %q", summaryText.String())
	}
	if !sawRisks {
		t.Error("缺 risks_done 帧")
	}
	if !sawDone {
		t.Error("缺 done 帧")
	}
}

func TestPostReview_NilStore_NoCrashAndNoCache(t *testing.T) {
	prov := &countingProvider{inner: dualMockProvider{
		textReply: "summary",
		jsonReply: `{"risks":[],"suggestions":[]}`,
	}}
	srv := startTestServer(t, Deps{Fetcher: fakeFetcher{pr: samplePR()}, Provider: prov, Store: nil})

	res, _ := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})
	if res.StatusCode != 200 {
		t.Errorf("nil Store 不应影响主流程，得到 status=%d", res.StatusCode)
	}
	if prov.calls.Load() == 0 {
		t.Error("nil Store 时应正常跑 stage")
	}
}

func TestPostReview_StageError_SkipsCache(t *testing.T) {
	s := newTestStore(t)
	// mockProvider 返非 JSON 文本 → RisksStage / SuggestionsStage 解析失败 → emit error event
	srv := startTestServer(t, Deps{
		Fetcher: fakeFetcher{pr: samplePR()},
		Provider: dualMockProvider{
			textReply: "summary text",
			jsonReply: "not valid json",
		},
		Store: s,
	})

	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}

	rec, err := s.Get(context.Background(), "golang", "go", 42, "deadbeef")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if rec != nil {
		t.Errorf("stage 报错时不应写缓存，得到 %+v", rec)
	}
}

func TestPostReview_EmptyPR_ShortCircuits(t *testing.T) {
	srv := startTestServer(t, Deps{
		Fetcher: fakeFetcher{
			pr: gh.PullRequest{
				Owner: "o", Repo: "r", Number: 1, HeadSHA: "sha",
				Title: "empty PR", Files: nil,
			},
		},
		Provider: llm.NewMockProvider(),
	})

	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/o/r/pull/1"})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d, want 200; body=%s", res.StatusCode, body)
	}

	frames := parseSSE(body)
	var sawInfo, sawDone bool
	for _, f := range frames {
		switch f.Type {
		case "pr":
			// OK，首帧
		case "info":
			sawInfo = true
			if !strings.Contains(f.Data, "无可评审") {
				t.Errorf("info 消息错: %s", f.Data)
			}
		case "done":
			sawDone = true
		case "summary_delta", "risks_done", "suggestions_done":
			t.Errorf("空 PR 不应跑 stage，得到 %s", f.Type)
		}
	}
	if !sawInfo {
		t.Error("缺 info 帧")
	}
	if !sawDone {
		t.Error("缺 done 帧")
	}
}

func TestPostReview_PRNotFound(t *testing.T) {
	srv := startTestServer(t, Deps{
		Fetcher:  fakeFetcher{err: gh.ErrPRNotFound},
		Provider: llm.NewMockProvider(),
	})
	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/o/r/pull/1"})
	if res.StatusCode != 404 {
		t.Errorf("status=%d, want 404; body=%s", res.StatusCode, body)
	}
	if !strings.Contains(body, "GITHUB_TOKEN") {
		t.Errorf("错误消息应提示设置 GITHUB_TOKEN，得到 %s", body)
	}
}

func TestPostReview_AccessDenied(t *testing.T) {
	srv := startTestServer(t, Deps{
		Fetcher:  fakeFetcher{err: gh.ErrAccessDenied},
		Provider: llm.NewMockProvider(),
	})
	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/o/r/pull/1"})
	if res.StatusCode != 403 {
		t.Errorf("status=%d, want 403; body=%s", res.StatusCode, body)
	}
}

func TestPostReview_PrEvent_IncludesMeta(t *testing.T) {
	mock := llm.NewMockProvider()
	mock.Reply = "summary"
	srv := startTestServer(t, Deps{Fetcher: fakeFetcher{pr: samplePRWithMeta()}, Provider: mock})

	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	frames := parseSSE(body)
	if len(frames) == 0 || frames[0].Type != "pr" {
		t.Fatalf("首帧应是 pr，得到 %+v", frames)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(frames[0].Data), &meta); err != nil {
		t.Fatalf("decode pr data: %v", err)
	}

	if meta["author"] != "lin-mei" {
		t.Errorf("author=%v want lin-mei", meta["author"])
	}
	if meta["author_role"] != "CONTRIBUTOR" {
		t.Errorf("author_role=%v want CONTRIBUTOR", meta["author_role"])
	}
	if meta["state"] != "open" {
		t.Errorf("state=%v want open", meta["state"])
	}
	if meta["base_ref"] != "main" || meta["head_ref"] != "fix/scanner-race" {
		t.Errorf("refs 错: base=%v head=%v", meta["base_ref"], meta["head_ref"])
	}
	if meta["ci"] != "passing" {
		t.Errorf("ci=%v want passing", meta["ci"])
	}
	labels, _ := meta["labels"].([]any)
	if len(labels) != 2 || labels[0] != "bug" {
		t.Errorf("labels=%v", labels)
	}
	stats, _ := meta["stats"].(map[string]any)
	if stats == nil || stats["files"] != float64(5) || stats["additions"] != float64(96) {
		t.Errorf("stats=%v", stats)
	}
	checks, _ := meta["checks"].([]any)
	if len(checks) != 2 {
		t.Errorf("checks 应为 2，得到 %v", checks)
	} else {
		c0, _ := checks[0].(map[string]any)
		if c0["name"] != "build" || c0["status"] != "passing" || c0["duration_ms"] != float64(24100) || c0["note"] != "82.4% (-0.3%)" {
			t.Errorf("checks[0]=%v", c0)
		}
	}
	if _, ok := meta["pr_created_at"]; !ok {
		t.Error("缺 pr_created_at")
	}
}

func TestPrMetaPayload_OmitsEmptyAuthorRole(t *testing.T) {
	meta := prMetaPayload(samplePR(), "https://github.com/golang/go/pull/42")
	if _, ok := meta["author_role"]; ok {
		t.Fatalf("author_role should be omitted when empty, got %v", meta["author_role"])
	}
}

func TestPostReview_FilesEvent_AfterPr(t *testing.T) {
	pr := samplePR()
	pr.Files = []gh.File{
		{Path: "a.go", Status: "modified", Patch: "@@ -1 +1 @@\n-old\n+new", Additions: 1, Deletions: 1},
		{Path: "b.go", Status: "added", Patch: "@@ -0 +1 @@\n+hello", Additions: 1, Deletions: 0},
	}
	srv := startTestServer(t, Deps{Fetcher: fakeFetcher{pr: pr}, Provider: llm.NewMockProvider()})

	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	frames := parseSSE(body)
	if len(frames) < 2 {
		t.Fatalf("帧数不足: %d", len(frames))
	}
	if frames[0].Type != "pr" {
		t.Errorf("首帧应是 pr，得到 %q", frames[0].Type)
	}
	if frames[1].Type != "files" {
		t.Errorf("次帧应是 files（紧跟 pr），得到 %q", frames[1].Type)
	}

	var files []map[string]any
	if err := json.Unmarshal([]byte(frames[1].Data), &files); err != nil {
		t.Fatalf("files data not JSON array: %v body=%s", err, frames[1].Data)
	}
	if len(files) != 2 {
		t.Fatalf("files 长度 %d 应为 2", len(files))
	}
	if files[0]["path"] != "a.go" || files[0]["status"] != "modified" {
		t.Errorf("files[0] 错: %+v", files[0])
	}
	if files[0]["additions"] != float64(1) || files[0]["deletions"] != float64(1) {
		t.Errorf("files[0] 加减行错: %+v", files[0])
	}
	if !strings.Contains(files[0]["patch"].(string), "@@") {
		t.Errorf("files[0] patch 应含 diff 头: %v", files[0]["patch"])
	}
	if _, leak := files[0]["full_text"]; leak {
		t.Error("FullText 不应序列化到 SSE（json:\"-\" 失效？）")
	}
}

func TestPostReview_EmptyPR_OmitsFilesEvent(t *testing.T) {
	pr := samplePR()
	pr.Files = nil
	srv := startTestServer(t, Deps{Fetcher: fakeFetcher{pr: pr}, Provider: llm.NewMockProvider()})

	_, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})
	for _, f := range parseSSE(body) {
		if f.Type == "files" {
			t.Errorf("空 PR 不应发 files 帧；得到 %s", f.Data)
		}
	}
}

func TestPostReview_CacheMiss_PersistsAllMetaFields(t *testing.T) {
	s := newTestStore(t)
	srv := startTestServer(t, Deps{
		Fetcher: fakeFetcher{pr: samplePRWithMeta()},
		Provider: dualMockProvider{
			textReply: "summary text",
			jsonReply: `{"risks":[],"suggestions":[]}`,
		},
		Store: s,
	})

	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}

	rec, err := s.Get(context.Background(), "golang", "go", 42, "deadbeef")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if rec == nil {
		t.Fatal("缓存应被写入")
	}
	var p cachedPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.Author != "lin-mei" || p.State != "open" || p.CI != "passing" {
		t.Errorf("meta 未持久化: %+v", p)
	}
	if p.BaseRef != "main" || p.HeadRef != "fix/scanner-race" {
		t.Errorf("refs 未持久化: %+v", p)
	}
	if p.Stats.Files != 5 || p.Stats.Additions != 96 {
		t.Errorf("stats 未持久化: %+v", p.Stats)
	}
	if len(p.Labels) != 2 || len(p.Checks) != 2 {
		t.Errorf("labels/checks 未持久化: %+v %+v", p.Labels, p.Checks)
	}
	if p.PRCreatedAt.IsZero() {
		t.Error("pr_created_at 应被持久化")
	}
}

func TestPostReview_CacheMiss_PersistsFiles(t *testing.T) {
	s := newTestStore(t)
	pr := samplePR()
	pr.Files = []gh.File{
		{Path: "scanner.go", Status: "modified", Patch: "@@ patch @@", Additions: 5, Deletions: 2},
	}
	srv := startTestServer(t, Deps{
		Fetcher: fakeFetcher{pr: pr},
		Provider: dualMockProvider{
			textReply: "summary",
			jsonReply: `{"risks":[],"suggestions":[]}`,
		},
		Store: s,
	})

	res, body := postJSON(t, srv, "/api/review", map[string]string{"url": "https://github.com/golang/go/pull/42"})
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	rec, err := s.Get(context.Background(), "golang", "go", 42, "deadbeef")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if rec == nil {
		t.Fatal("缓存应被写入")
	}
	var p cachedPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(p.Files) != 1 || p.Files[0].Path != "scanner.go" || p.Files[0].Patch == "" {
		t.Errorf("Files 未持久化或字段缺失: %+v", p.Files)
	}
}
