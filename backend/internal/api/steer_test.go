package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/i18n"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// seedSteerReview writes a cached review for the steer tests: non-empty files + complete meta
func seedSteerReview(t *testing.T, s store.Store) string {
	t.Helper()
	payload, _ := json.Marshal(cachedPayload{
		Title:   "fix race",
		BaseRef: "main", HeadRef: "fix/race",
		Stats: gh.Stats{Files: 1, Additions: 5, Deletions: 2},
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
	// no stage given → defaults to risks
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

// TestBuildAgentSystemPrompt_BaseByLocale pins the exact assembled prompt (no optional fragments) for each
// locale, independent of agentSystemByLocale itself (expected strings are hardcoded here, not looked up from the
// map), so a typo'd or removed map entry fails this test instead of compiling away silently.
// This exercises buildAgentSystemPrompt directly rather than the full HTTP handler: PostSteer's mode=agent path
// now resolves locale from the request (resolveLocale), but driving it to the EN branch through the full handler
// would mean asserting on Accept-Language / body wiring that TestResolveLocalePrefersBodyThenHeaderThenDefault
// already covers directly — this test's job is only to pin the prompt text per locale.
func TestBuildAgentSystemPrompt_BaseByLocale(t *testing.T) {
	wantZH := "你是 code reviewer agent。回答 PR 相关问题。\n\n" +
		"## 关键：先看「相关代码」段\n" +
		"prompt 末尾「## 相关代码（跨文件 RAG 召回）」段已是基于用户问题语义召回的本仓库相关代码（可能来自本 PR 也可能来自 main 上未在本 PR 改动的文件）。" +
		"**如果该段已包含足以回答问题的内容，直接基于它给答案，不要调工具。**\n\n" +
		"## 工具\n" +
		"- `read_file` / `list_dir` / `grep_patches`：仅限**本 PR 改动文件**沙盒，跨出会被拒绝。" +
		"\n\n## 输出\n" +
		"用一段简洁中文文字回答（不要 JSON）。优先引用具体文件路径 + 行为，让读者能直接定位。"
	wantEN := "You are a code reviewer agent. Answer PR-related questions.\n\n" +
		"## Key: check the \"Related code\" section first\n" +
		"The end of the prompt has a \"## Related code (cross-file RAG retrieval)\" section, already retrieved by semantic search against the user's question (it may come from this PR or from files on main that this PR did not touch). " +
		"**If that section already contains enough to answer, answer from it directly — do not call a tool.**\n\n" +
		"## Tools\n" +
		"- `read_file` / `list_dir` / `grep_patches`: sandboxed to **files this PR changed** only; anything outside is rejected." +
		"\n\n## Output\n" +
		"Answer in a concise paragraph of English (no JSON). Prefer citing concrete file paths and what happens there, so the reader can go straight to it."

	cases := []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZH, wantZH},
		{i18n.EN, wantEN},
	}
	for _, tc := range cases {
		t.Run(string(tc.locale), func(t *testing.T) {
			got := buildAgentSystemPrompt(tc.locale, false, false)
			if got != tc.want {
				t.Errorf("buildAgentSystemPrompt(%s, false, false) =\n%q\nwant\n%q", tc.locale, got, tc.want)
			}
		})
	}
}

// TestBuildAgentSystemPrompt_OptionalFragments checks the search_repo / conversation-history fragments are
// appended only when requested, and only in the requested locale (no cross-locale leakage).
func TestBuildAgentSystemPrompt_OptionalFragments(t *testing.T) {
	base := buildAgentSystemPrompt(i18n.EN, false, false)
	if strings.Contains(base, "search_repo") {
		t.Errorf("base EN prompt should not mention search_repo, got %q", base)
	}
	if strings.Contains(base, "Conversation history") {
		t.Errorf("base EN prompt should not mention conversation history, got %q", base)
	}

	withSearch := buildAgentSystemPrompt(i18n.EN, true, false)
	if !strings.Contains(withSearch, "`search_repo`: semantic search over the whole-repo RAG index") {
		t.Errorf("EN prompt with hasSearchRepo=true should mention search_repo, got %q", withSearch)
	}

	withHistory := buildAgentSystemPrompt(i18n.EN, false, true)
	if !strings.Contains(withHistory, "## Conversation history") {
		t.Errorf("EN prompt with hasHistory=true should mention conversation history, got %q", withHistory)
	}

	withSearchZH := buildAgentSystemPrompt(i18n.ZH, true, false)
	if !strings.Contains(withSearchZH, "search_repo") || strings.Contains(withSearchZH, "whole-repo RAG index") {
		t.Errorf("ZH prompt with hasSearchRepo=true should mention search_repo in Chinese, not English, got %q", withSearchZH)
	}
}
