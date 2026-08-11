package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/index"
)

// fakeRetriever 受控返回：可指定 refs / err / 记录最近一次入参
type fakeRetriever struct {
	refs       []index.Reference
	err        error
	lastScope  string
	lastQuery  string
	lastK      int
}

func (f *fakeRetriever) Retrieve(_ context.Context, scope, query string, k int) ([]index.Reference, error) {
	f.lastScope = scope
	f.lastQuery = query
	f.lastK = k
	if f.err != nil {
		return nil, f.err
	}
	return f.refs, nil
}

func sampleFiles() []gh.File {
	return []gh.File{
		{Path: "main.go", Status: "modified", Patch: "@@ -1,2 +1,3 @@\n package main\n+// TODO fix race\n var x = 1", Additions: 1, Deletions: 0},
		{Path: "util/helper.go", Status: "added", Patch: "@@ -0,0 +1,5 @@\n+package util\n+\n+func Helper() string {\n+\treturn \"ok\"\n+}", Additions: 5, Deletions: 0},
		{Path: "README.md", Status: "modified", Patch: "@@ -1 +1,2 @@\n # PR-Review\n+## Quick start", Additions: 1, Deletions: 0},
	}
}

func TestReadFileTool_Found(t *testing.T) {
	tool := NewReadFileTool(sampleFiles())
	out, err := tool.Run(context.Background(), json.RawMessage(`{"file":"main.go"}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "patch:") || !strings.Contains(out, "TODO fix race") {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestReadFileTool_SandboxRejects(t *testing.T) {
	tool := NewReadFileTool(sampleFiles())
	_, err := tool.Run(context.Background(), json.RawMessage(`{"file":"/etc/passwd"}`))
	if err == nil || !strings.Contains(err.Error(), "沙盒") {
		t.Errorf("want sandbox rejection, got err=%v", err)
	}
}

func TestReadFileTool_MissingArg(t *testing.T) {
	tool := NewReadFileTool(sampleFiles())
	_, err := tool.Run(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "file 不能为空") {
		t.Errorf("want missing-file error, got %v", err)
	}
}

func TestListDirTool_Filtered(t *testing.T) {
	tool := NewListDirTool(sampleFiles())
	out, err := tool.Run(context.Background(), json.RawMessage(`{"prefix":"util/"}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "util/helper.go") {
		t.Errorf("util/helper.go missing in output: %s", out)
	}
	if strings.Contains(out, "main.go") {
		t.Errorf("main.go should be filtered out, but appears in: %s", out)
	}
}

func TestListDirTool_NoPrefix_ListsAll(t *testing.T) {
	tool := NewListDirTool(sampleFiles())
	out, _ := tool.Run(context.Background(), json.RawMessage(`{}`))
	for _, path := range []string{"main.go", "util/helper.go", "README.md"} {
		if !strings.Contains(out, path) {
			t.Errorf("path %s missing: %s", path, out)
		}
	}
}

func TestGrepTool_LiteralMatch(t *testing.T) {
	tool := NewGrepTool(sampleFiles())
	out, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"TODO"}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "main.go:") || !strings.Contains(out, "TODO fix race") {
		t.Errorf("unexpected: %s", out)
	}
}

func TestGrepTool_Regex(t *testing.T) {
	tool := NewGrepTool(sampleFiles())
	out, err := tool.Run(context.Background(), json.RawMessage(`{"pattern":"^\\+.*func","regex":true}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "func Helper") {
		t.Errorf("regex hit expected, got: %s", out)
	}
}

func TestGrepTool_NoMatch(t *testing.T) {
	tool := NewGrepTool(sampleFiles())
	out, _ := tool.Run(context.Background(), json.RawMessage(`{"pattern":"impossible-zzz-xyz"}`))
	if !strings.Contains(out, "无命中") {
		t.Errorf("want 无命中 message, got: %s", out)
	}
}

func TestRegisterDefaults_AllThree(t *testing.T) {
	r := NewRegistry()
	RegisterDefaults(r, sampleFiles())
	for _, name := range []string{"read_file", "list_dir", "grep_patches"} {
		if _, ok := r.Lookup(name); !ok {
			t.Errorf("default tool %s missing after RegisterDefaults", name)
		}
	}
	if _, ok := r.Lookup("search_repo"); ok {
		t.Errorf("search_repo should NOT be in plain RegisterDefaults")
	}
}

func TestSearchRepoTool_FormatsHits(t *testing.T) {
	fr := &fakeRetriever{refs: []index.Reference{
		{File: "internal/api/review.go", Snippet: "func PostReview() {}", Score: 0.83, PRNumber: 76},
		{File: "internal/llm/openai.go", Snippet: "func (p *OpenAIProvider) Stream() {}", Score: 0.71},
	}}
	tool := NewSearchRepoTool(fr, "owner/repo")
	out, err := tool.Run(context.Background(), json.RawMessage(`{"query":"PostReview handler","top_k":2}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if fr.lastScope != "owner/repo" || fr.lastQuery != "PostReview handler" || fr.lastK != 2 {
		t.Errorf("retriever called with wrong args: scope=%q query=%q k=%d", fr.lastScope, fr.lastQuery, fr.lastK)
	}
	for _, want := range []string{
		"[1] internal/api/review.go", "score=0.83", "pr=#76", "func PostReview",
		"[2] internal/llm/openai.go", "score=0.71",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// 没 PRNumber 的条目不带 pr= 段
	if strings.Contains(out, "pr=#0") {
		t.Errorf("PRNumber=0 should not render pr= tag: %s", out)
	}
}

func TestSearchRepoTool_TopKClamp(t *testing.T) {
	fr := &fakeRetriever{}
	tool := NewSearchRepoTool(fr, "x/y")
	_, _ = tool.Run(context.Background(), json.RawMessage(`{"query":"q","top_k":50}`))
	if fr.lastK != 10 {
		t.Errorf("top_k should clamp to 10, got %d", fr.lastK)
	}
	_, _ = tool.Run(context.Background(), json.RawMessage(`{"query":"q"}`))
	if fr.lastK != 5 {
		t.Errorf("default top_k should be 5, got %d", fr.lastK)
	}
}

func TestSearchRepoTool_EmptyQuery(t *testing.T) {
	tool := NewSearchRepoTool(&fakeRetriever{}, "x/y")
	_, err := tool.Run(context.Background(), json.RawMessage(`{"query":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "query 不能为空") {
		t.Errorf("want empty-query rejection, got %v", err)
	}
}

func TestSearchRepoTool_NoHits(t *testing.T) {
	tool := NewSearchRepoTool(&fakeRetriever{refs: nil}, "x/y")
	out, err := tool.Run(context.Background(), json.RawMessage(`{"query":"impossible"}`))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "无命中") {
		t.Errorf("expected 无命中 hint, got: %s", out)
	}
}

func TestSearchRepoTool_RetrieverError(t *testing.T) {
	tool := NewSearchRepoTool(&fakeRetriever{err: errors.New("boom")}, "x/y")
	_, err := tool.Run(context.Background(), json.RawMessage(`{"query":"q"}`))
	if err == nil || !strings.Contains(err.Error(), "检索失败") {
		t.Errorf("want 检索失败 wrap, got %v", err)
	}
}

func TestSearchRepoTool_SnippetTruncation(t *testing.T) {
	big := strings.Repeat("a", 1500)
	fr := &fakeRetriever{refs: []index.Reference{{File: "big.go", Snippet: big, Score: 0.5}}}
	tool := NewSearchRepoTool(fr, "x/y")
	out, _ := tool.Run(context.Background(), json.RawMessage(`{"query":"q"}`))
	if !strings.Contains(out, "已截断") {
		t.Errorf("long snippet should be truncated with marker: len=%d", len(out))
	}
	if strings.Count(out, "a") >= 1500 {
		t.Errorf("snippet not actually truncated: %d a's", strings.Count(out, "a"))
	}
}

func TestRegisterDefaultsWithRAG_RegistersSearchRepo(t *testing.T) {
	r := NewRegistry()
	RegisterDefaultsWithRAG(r, sampleFiles(), &fakeRetriever{}, "owner/repo")
	if _, ok := r.Lookup("search_repo"); !ok {
		t.Errorf("search_repo should be registered with real retriever + scope")
	}
}

func TestRegisterDefaultsWithRAG_SkipsWhenNoRetriever(t *testing.T) {
	cases := []struct {
		name      string
		retriever index.Retriever
		scope     string
	}{
		{"nil retriever", nil, "owner/repo"},
		{"noop retriever", index.NoopRetriever{}, "owner/repo"},
		{"empty scope", &fakeRetriever{}, ""},
		{"whitespace scope", &fakeRetriever{}, "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			RegisterDefaultsWithRAG(r, sampleFiles(), tc.retriever, tc.scope)
			if _, ok := r.Lookup("search_repo"); ok {
				t.Errorf("search_repo should be skipped when %s", tc.name)
			}
			// PR 沙盒三件套始终注册
			for _, name := range []string{"read_file", "list_dir", "grep_patches"} {
				if _, ok := r.Lookup(name); !ok {
					t.Errorf("sandbox tool %s missing", name)
				}
			}
		})
	}
}
