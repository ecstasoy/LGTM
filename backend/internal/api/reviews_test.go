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

// seedReview writes one cached review straight into the store for the list / get tests
func seedReview(t *testing.T, s store.Store, owner, repo string, pr int, sha, title string, when time.Time) string {
	t.Helper()
	payload, _ := json.Marshal(cachedPayload{
		Title:       title,
		Summary:     "summary for " + title,
		Risks:       json.RawMessage(`[]`),
		Suggestions: json.RawMessage(`[]`),
	})
	id := store.NewID()
	rec := &store.Record{
		ID: id, Owner: owner, Repo: repo, PRNumber: pr, HeadSHA: sha,
		Payload: payload, CreatedAt: when,
	}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return id
}

func TestListReviews_ReturnsRecordsDesc(t *testing.T) {
	s := newTestStore(t)
	seedReview(t, s, "golang", "go", 1, "sha1", "older PR", time.Unix(1000, 0))
	newerID := seedReview(t, s, "golang", "go", 2, "sha2", "newer PR", time.Unix(2000, 0))

	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/reviews")
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}

	var list []reviewListItem
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if len(list) != 2 {
		t.Fatalf("应返 2 条，得到 %d: %s", len(list), body)
	}
	if list[0].ID != newerID {
		t.Errorf("最新一条应排首位；首条 ID=%s 期望 %s", list[0].ID, newerID)
	}
	if list[0].Title != "newer PR" {
		t.Errorf("title 应从 payload 解出，得到 %q", list[0].Title)
	}
}

func TestListReviews_LimitParam(t *testing.T) {
	s := newTestStore(t)
	for i := range 5 {
		seedReview(t, s, "o", "r", i+1, "sha", "t", time.Unix(int64(1000+i), 0))
	}
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})

	res, body := getJSON(t, srv, "/api/reviews?limit=2")
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	var list []reviewListItem
	_ = json.Unmarshal([]byte(body), &list)
	if len(list) != 2 {
		t.Errorf("limit=2 应返 2 条，得到 %d", len(list))
	}
}

func TestListReviews_NoStore_503(t *testing.T) {
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: nil})
	res, body := getJSON(t, srv, "/api/reviews")
	if res.StatusCode != 503 {
		t.Errorf("status=%d want 503; body=%s", res.StatusCode, body)
	}
}

func TestGetReview_ReturnsDetail(t *testing.T) {
	s := newTestStore(t)
	id := seedReview(t, s, "golang", "go", 42, "deadbeef", "fix race", time.Unix(1000, 0))
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})

	res, body := getJSON(t, srv, "/api/reviews/"+id)
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var d reviewDetail
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if d.ID != id || d.PR != 42 || d.HeadSHA != "deadbeef" || d.Title != "fix race" {
		t.Errorf("meta 错: %+v", d)
	}
	if !strings.Contains(d.Summary, "fix race") {
		t.Errorf("summary 错: %q", d.Summary)
	}
	if string(d.Risks) != "[]" {
		t.Errorf("risks 应是 raw JSON [], 得到 %s", d.Risks)
	}
}

func TestGetReview_NotFound_404(t *testing.T) {
	s := newTestStore(t)
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, _ := getJSON(t, srv, "/api/reviews/no-such-id")
	if res.StatusCode != 404 {
		t.Errorf("status=%d want 404", res.StatusCode)
	}
}

func TestGetReview_NoStore_503(t *testing.T) {
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: nil})
	res, _ := getJSON(t, srv, "/api/reviews/anything")
	if res.StatusCode != 503 {
		t.Errorf("status=%d want 503", res.StatusCode)
	}
}

func TestParseLimit(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", defaultListLimit},
		{"0", defaultListLimit},
		{"-3", defaultListLimit},
		{"abc", defaultListLimit},
		{"5", 5},
		{"500", maxListLimit},
	}
	for _, tc := range cases {
		if got := parseLimit(tc.in); got != tc.want {
			t.Errorf("parseLimit(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
}

// seedFullReview writes a cached review with complete meta + checks + custom risks.
// Used by the ListReviews CI/risks_counts test and the GetReview full-meta test.
func seedFullReview(t *testing.T, s store.Store, risksJSON string) string {
	t.Helper()
	payload, _ := json.Marshal(cachedPayload{
		Title:       "fix race",
		Author:      "lin-mei",
		AuthorRole:  "CONTRIBUTOR",
		Lang:        "Go",
		State:       gh.StateOpen,
		Labels:      []string{"bug", "concurrency"},
		BaseRef:     "main",
		HeadRef:     "fix/race",
		PRCreatedAt: time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC),
		Stats:       gh.Stats{Files: 5, Additions: 96, Deletions: 41, Commits: 4, Comments: 7},
		CI:          gh.CIStatusPassing,
		Checks: []gh.Check{
			{Name: "build", Status: gh.CIStatusPassing, DurationMS: 24100, Note: "82.4% (-0.3%)"},
		},
		Summary:     "summary text",
		Risks:       json.RawMessage(risksJSON),
		Suggestions: json.RawMessage(`[]`),
	})
	id := store.NewID()
	rec := &store.Record{
		ID: id, Owner: "golang", Repo: "go", PRNumber: 42, HeadSHA: "deadbeef",
		Payload: payload, CreatedAt: time.Unix(2000, 0),
	}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return id
}

func TestListReviews_IncludesCIAndRiskCounts(t *testing.T) {
	s := newTestStore(t)
	seedFullReview(t, s, `[
{"file":"a.go","severity":"high","category":"bug","confidence":0.9,"reason":"x"},
{"file":"b.go","severity":"high","category":"bug","confidence":0.8,"reason":"y"},
{"file":"c.go","severity":"medium","category":"perf","confidence":0.7,"reason":"z"},
{"file":"d.go","severity":"low","category":"style","confidence":0.6,"reason":"w"}
]`)

	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/reviews")
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if len(list) != 1 {
		t.Fatalf("应返 1 条，得到 %d", len(list))
	}
	item := list[0]
	if item["ci"] != "passing" {
		t.Errorf("ci=%v want passing", item["ci"])
	}
	counts, _ := item["risk_counts"].(map[string]any)
	if counts == nil {
		t.Fatalf("缺 risk_counts: %v", item)
	}
	if counts["high"] != float64(2) || counts["medium"] != float64(1) || counts["low"] != float64(1) {
		t.Errorf("risk_counts=%v want high:2 medium:1 low:1", counts)
	}
}

func TestGetReview_IncludesFullMeta(t *testing.T) {
	s := newTestStore(t)
	id := seedFullReview(t, s, `[{"file":"a.go","severity":"high","category":"bug","confidence":0.9,"reason":"x"}]`)

	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/reviews/"+id)
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if d["author"] != "lin-mei" || d["author_role"] != "CONTRIBUTOR" || d["state"] != "open" || d["ci"] != "passing" {
		t.Errorf("meta 字段缺失: %v", d)
	}
	if d["base_ref"] != "main" || d["head_ref"] != "fix/race" {
		t.Errorf("refs 缺失: %v", d)
	}
	if labels, _ := d["labels"].([]any); len(labels) != 2 {
		t.Errorf("labels=%v", d["labels"])
	}
	if stats, _ := d["stats"].(map[string]any); stats == nil || stats["files"] != float64(5) {
		t.Errorf("stats 缺: %v", d["stats"])
	}
	if checks, _ := d["checks"].([]any); len(checks) != 1 {
		t.Errorf("checks=%v", d["checks"])
	} else {
		c0, _ := checks[0].(map[string]any)
		if c0["note"] != "82.4% (-0.3%)" {
			t.Errorf("checks[0].note=%v", c0["note"])
		}
	}
	if d["pr_created_at"] == nil {
		t.Error("缺 pr_created_at")
	}
	if counts, _ := d["risk_counts"].(map[string]any); counts == nil || counts["high"] != float64(1) {
		t.Errorf("risk_counts 缺: %v", d["risk_counts"])
	}
}

func TestGetReview_IncludesFiles(t *testing.T) {
	s := newTestStore(t)
	payload, _ := json.Marshal(cachedPayload{
		Title: "with files",
		Files: []gh.File{
			{Path: "scanner.go", Status: "modified", Patch: "@@ -1 +1 @@", Additions: 1, Deletions: 1},
			{Path: "README.md", Status: "added", Patch: "@@ -0 +1 @@\n+new", Additions: 1, Deletions: 0},
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
		t.Fatalf("seed: %v", err)
	}

	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/reviews/"+id)
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	files, _ := d["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("files 应 2 个，得到 %v", files)
	}
	f0, _ := files[0].(map[string]any)
	if f0["path"] != "scanner.go" || f0["status"] != "modified" {
		t.Errorf("files[0] 字段错: %+v", f0)
	}
	if _, leak := f0["full_text"]; leak {
		t.Error("FullText 不应序列化")
	}
}

func TestListReviews_ExcludesFiles(t *testing.T) {
	// the list endpoint should not return files (each is large, and 50 of them would blow up the response)
	s := newTestStore(t)
	payload, _ := json.Marshal(cachedPayload{
		Title: "with files",
		Files: []gh.File{
			{Path: "scanner.go", Status: "modified", Patch: "@@ -1 +1 @@"},
		},
		Summary:     "s",
		Risks:       json.RawMessage(`[]`),
		Suggestions: json.RawMessage(`[]`),
	})
	rec := &store.Record{
		ID: store.NewID(), Owner: "o", Repo: "r", PRNumber: 1, HeadSHA: "sha",
		Payload: payload, CreatedAt: time.Unix(1000, 0),
	}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/reviews")
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var list []map[string]any
	_ = json.Unmarshal([]byte(body), &list)
	if len(list) == 0 {
		t.Fatal("应有 1 条")
	}
	if _, hasFiles := list[0]["files"]; hasFiles {
		t.Error("list 端不应包含 files 字段（detail 才有）")
	}
}

func TestListReviews_IncludesLang(t *testing.T) {
	s := newTestStore(t)
	seedFullReview(t, s, `[]`)

	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/reviews")
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var list []map[string]any
	_ = json.Unmarshal([]byte(body), &list)
	if len(list) != 1 || list[0]["lang"] != "Go" {
		t.Errorf("lang 应从 payload 解出为 Go，得到 %v", list[0]["lang"])
	}
}

func TestGetReview_IncludesLang(t *testing.T) {
	s := newTestStore(t)
	id := seedFullReview(t, s, `[]`)

	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/reviews/"+id)
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var d map[string]any
	_ = json.Unmarshal([]byte(body), &d)
	if d["lang"] != "Go" {
		t.Errorf("detail.lang 应为 Go，得到 %v", d["lang"])
	}
}

// TestGetReview_IncludesLocale confirms a review persisted with a locale surfaces it unchanged
// through the detail endpoint (Task 22).
func TestGetReview_IncludesLocale(t *testing.T) {
	s := newTestStore(t)
	payload, _ := json.Marshal(cachedPayload{
		Title:       "fix race",
		Locale:      "en",
		Summary:     "summary text",
		Risks:       json.RawMessage(`[]`),
		Suggestions: json.RawMessage(`[]`),
	})
	id := store.NewID()
	rec := &store.Record{
		ID: id, Owner: "golang", Repo: "go", PRNumber: 42, HeadSHA: "deadbeef",
		Locale: "en", Payload: payload, CreatedAt: time.Unix(1000, 0),
	}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/reviews/"+id)
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var d map[string]any
	_ = json.Unmarshal([]byte(body), &d)
	if d["locale"] != "en" {
		t.Errorf("detail.locale 应为 en，得到 %v", d["locale"])
	}
}

// TestGetReview_AbsentPayloadLocale_NeverFabricatesZh is the backward-compatibility case that
// matters most for Task 22: a row written before the locale field existed has no "locale" key in
// its stored payload JSON, even though the reviews.locale *column* was backfilled to "zh" for
// every pre-existing row (store.ensureLocaleColumn's `ALTER TABLE ... DEFAULT 'zh'`). GetReview
// must build its response from the payload, not that column, so the detail's locale comes back
// empty — never a fabricated "zh" — letting the frontend treat it as unknown and show no notice.
func TestGetReview_AbsentPayloadLocale_NeverFabricatesZh(t *testing.T) {
	s := newTestStore(t)
	id := seedReview(t, s, "golang", "go", 1, "sha1", "legacy PR", time.Unix(1000, 0))

	// Sanity-check the disagreement this test guards against: the store column reads back "zh"
	// (Put's normalizeLocale backfill), even though seedReview's payload never set Locale.
	rec, err := s.GetByID(context.Background(), id)
	if err != nil || rec == nil {
		t.Fatalf("GetByID: %v", err)
	}
	if rec.Locale != store.DefaultLocale {
		t.Fatalf("expected the store column to be backfilled to %q, got %q", store.DefaultLocale, rec.Locale)
	}

	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/reviews/"+id)
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if v, ok := d["locale"]; ok && v != "" {
		t.Errorf("a pre-i18n row's locale should be absent/empty, got %v", v)
	}
}

// TestListReviews_AbsentPayloadLocale_NeverFabricatesZh is the list-endpoint counterpart of
// TestGetReview_AbsentPayloadLocale_NeverFabricatesZh — the /history table must not badge a
// pre-i18n row as Chinese either.
func TestListReviews_AbsentPayloadLocale_NeverFabricatesZh(t *testing.T) {
	s := newTestStore(t)
	seedReview(t, s, "golang", "go", 1, "sha1", "legacy PR", time.Unix(1000, 0))

	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/reviews")
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if len(list) != 1 {
		t.Fatalf("应返 1 条，得到 %d", len(list))
	}
	if v, ok := list[0]["locale"]; ok && v != "" {
		t.Errorf("a pre-i18n row's locale should be absent/empty, got %v", v)
	}
}

func TestCountRisksBySeverity(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want riskCounts
	}{
		{"empty raw", "", riskCounts{}},
		{"empty array", "[]", riskCounts{}},
		{"single high", `[{"severity":"high"}]`, riskCounts{High: 1}},
		{"mixed", `[{"severity":"high"},{"severity":"high"},{"severity":"medium"},{"severity":"low"},{"severity":"low"}]`, riskCounts{High: 2, Medium: 1, Low: 2}},
		{"unknown severity ignored", `[{"severity":"critical"}]`, riskCounts{}},
		{"malformed", `not json`, riskCounts{}},
	}
	for _, tc := range cases {
		got := countRisksBySeverity(json.RawMessage(tc.in))
		if got != tc.want {
			t.Errorf("[%s] got=%+v want=%+v", tc.name, got, tc.want)
		}
	}
}

// verifies BudgetReport survives the cache roundtrip and comes out of /api/reviews/:id intact, in the snake_case the frontend expects
func TestGetReview_IncludesBudgetReport(t *testing.T) {
	s := newTestStore(t)
	id := store.NewID()
	payload, _ := json.Marshal(cachedPayload{
		Title:       "PR with budget",
		Summary:     "s",
		Risks:       json.RawMessage(`[]`),
		Suggestions: json.RawMessage(`[]`),
		BudgetReport: &budgetReportPayload{
			TokenLimit: 16000,
			UsedL1:     4200,
			UsedL2:     8800,
			UsedL3:     1500,
			Dropped:    []string{"big/file.go"},
		},
	})
	rec := &store.Record{
		ID: id, Owner: "o", Repo: "r", PRNumber: 1, HeadSHA: "sha",
		Payload: payload, CreatedAt: time.Unix(3000, 0),
	}
	if err := s.Put(context.Background(), rec); err != nil {
		t.Fatalf("put: %v", err)
	}

	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/reviews/"+id)
	if res.StatusCode != 200 {
		t.Fatalf("status=%d body=%s", res.StatusCode, body)
	}
	// assert against the raw JSON field names, so a changed reviewDetail tag cannot slip through
	for _, want := range []string{
		`"budget_report":`, `"token_limit":16000`, `"used_l1":4200`,
		`"used_l2":8800`, `"used_l3":1500`, `"dropped":["big/file.go"]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s\nbody=%s", want, body)
		}
	}

	// older cache entries (no budget) must stay backwards compatible: detail carries no budget_report field
	idOld := seedReview(t, s, "old", "r", 2, "sha2", "no budget", time.Unix(4000, 0))
	res2, body2 := getJSON(t, srv, "/api/reviews/"+idOld)
	if res2.StatusCode != 200 {
		t.Fatalf("old status=%d body=%s", res2.StatusCode, body2)
	}
	if strings.Contains(body2, `"budget_report"`) {
		t.Errorf("old payload should not surface budget_report, got %s", body2)
	}
}
