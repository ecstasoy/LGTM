package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/index"
)

// stubIndexer 记录 UpsertMany 调用，便于 assert chunk 内容
type stubIndexer struct {
	mu     sync.Mutex
	calls  int
	scopes []string
	chunks [][]index.IndexerChunk
	err    error
}

func (s *stubIndexer) UpsertMany(_ context.Context, scope string, chunks []index.IndexerChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.scopes = append(s.scopes, scope)
	s.chunks = append(s.chunks, chunks)
	return s.err
}

func TestIndexPRChunks_NoopIndexerSkipsCall(t *testing.T) {
	// NoopIndexer 应直接 short-circuit，避免无谓 embedding API 调用
	pr := gh.PullRequest{
		Owner: "o", Repo: "r", Number: 1,
		Files: []gh.File{{Path: "a.go", Patch: "diff..."}},
	}
	indexPRChunks(context.Background(), index.NoopIndexer{}, pr) // 无 panic 即通过
}

func TestIndexPRChunks_EmptyPatchesNoUpsert(t *testing.T) {
	// 所有 file.Patch 为空（如 binary file）应该不触发 UpsertMany
	idx := &stubIndexer{}
	pr := gh.PullRequest{
		Owner: "o", Repo: "r", Number: 1,
		Files: []gh.File{{Path: "logo.png", Patch: ""}, {Path: "a.bin", Patch: ""}},
	}
	indexPRChunks(context.Background(), idx, pr)
	if idx.calls != 0 {
		t.Fatalf("expected 0 UpsertMany calls, got %d", idx.calls)
	}
}

func TestIndexPRChunks_HappyPath(t *testing.T) {
	// 正常 patch → 一文件一 chunk，scope = owner/repo
	idx := &stubIndexer{}
	pr := gh.PullRequest{
		Owner: "acme", Repo: "widget", Number: 42,
		Files: []gh.File{
			{Path: "a.go", Patch: "diff-a"},
			{Path: "b.go", Patch: "diff-b"},
			{Path: "skip.png", Patch: ""},
		},
	}
	indexPRChunks(context.Background(), idx, pr)
	if idx.calls != 1 {
		t.Fatalf("expected 1 UpsertMany call, got %d", idx.calls)
	}
	if got := idx.scopes[0]; got != "acme/widget" {
		t.Fatalf("scope = %q, want acme/widget", got)
	}
	chunks := idx.chunks[0]
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks (skip empty), got %d", len(chunks))
	}
	if chunks[0].Path != "a.go" || chunks[0].Content != "diff-a" {
		t.Fatalf("chunk[0] = %+v", chunks[0])
	}
	if chunks[1].Path != "b.go" || chunks[1].Content != "diff-b" {
		t.Fatalf("chunk[1] = %+v", chunks[1])
	}
}

func TestIndexPRChunks_TruncatesLongPatch(t *testing.T) {
	// 超过 indexMaxChunkChars 的 patch 截断；防 embedding token 上限报错
	long := strings.Repeat("x", indexMaxChunkChars+500)
	idx := &stubIndexer{}
	pr := gh.PullRequest{
		Owner: "o", Repo: "r", Number: 1,
		Files: []gh.File{{Path: "big.go", Patch: long}},
	}
	indexPRChunks(context.Background(), idx, pr)
	if idx.calls != 1 {
		t.Fatalf("expected 1 UpsertMany call, got %d", idx.calls)
	}
	got := idx.chunks[0][0].Content
	if len(got) != indexMaxChunkChars {
		t.Fatalf("content length = %d, want %d", len(got), indexMaxChunkChars)
	}
}

// TestSplitPatchToHunks 验证多 @@ 头按 hunk 切；fallback 单 hunk；空跳过
func TestSplitPatchToHunks(t *testing.T) {
	cases := []struct {
		name   string
		patch  string
		wantN  int
		wantP0 string // 第一个 hunk 头部
	}{
		{
			"two hunks",
			"@@ -1,3 +1,3 @@\n line1\n-old\n+new\n@@ -10,3 +10,3 @@\n line10\n-old2\n+new2",
			2,
			"@@ -1,3 +1,3 @@",
		},
		{
			"single hunk",
			"@@ -1,3 +1,3 @@\n a\n b",
			1,
			"@@ -1,3 +1,3 @@",
		},
		{
			"no hunk header fallback",
			"some raw content without hunk header\nmore",
			1,
			"some raw content without hunk header",
		},
		{"empty", "", 0, ""},
		{"whitespace only", "   \n\n", 0, ""},
	}
	for _, tc := range cases {
		got := splitPatchToHunks(tc.patch)
		if len(got) != tc.wantN {
			t.Errorf("%s: got %d hunks, want %d (out=%+v)", tc.name, len(got), tc.wantN, got)
			continue
		}
		if tc.wantN > 0 && !strings.HasPrefix(got[0], tc.wantP0) {
			t.Errorf("%s: hunk[0] should start with %q, got %q", tc.name, tc.wantP0, got[0])
		}
	}
}

// TestIndexPRChunks_MultiHunkFile 一个文件含 2 个 hunk → 应产 2 chunks，Idx=0/1
func TestIndexPRChunks_MultiHunkFile(t *testing.T) {
	si := &stubIndexer{}
	pr := gh.PullRequest{
		Owner: "o", Repo: "r", Number: 99,
		Files: []gh.File{{
			Path:  "big.go",
			Patch: "@@ -1,2 +1,2 @@\n a\n-b\n+B\n@@ -100,2 +100,2 @@\n x\n-y\n+Y",
		}},
	}
	indexPRChunks(context.Background(), si, pr)
	if si.calls != 1 {
		t.Fatalf("want 1 UpsertMany call, got %d", si.calls)
	}
	ch := si.chunks[0]
	if len(ch) != 2 {
		t.Fatalf("want 2 chunks (2 hunks), got %d", len(ch))
	}
	if ch[0].Idx != 0 || ch[1].Idx != 1 {
		t.Errorf("hunk indices wrong: %d, %d (want 0,1)", ch[0].Idx, ch[1].Idx)
	}
	if ch[0].PRNumber != 99 || ch[1].PRNumber != 99 {
		t.Errorf("both chunks must carry PRNumber=99: %d, %d", ch[0].PRNumber, ch[1].PRNumber)
	}
	if !strings.HasPrefix(ch[0].Content, "@@ -1,2") || !strings.HasPrefix(ch[1].Content, "@@ -100,2") {
		t.Errorf("hunk content order wrong; got:\n[0]=%q\n[1]=%q", ch[0].Content, ch[1].Content)
	}
}

func TestIndexPRChunks_UpsertErrorDoesNotPanic(t *testing.T) {
	// 索引失败仅 warn，不阻塞评审流程；helper 不返 error 不应 panic
	idx := &stubIndexer{err: errors.New("embed quota exceeded")}
	pr := gh.PullRequest{
		Owner: "o", Repo: "r", Number: 1,
		Files: []gh.File{{Path: "a.go", Patch: "diff"}},
	}
	indexPRChunks(context.Background(), idx, pr) // 应 swallow err
	if idx.calls != 1 {
		t.Fatalf("expected 1 call even on err, got %d", idx.calls)
	}
}
