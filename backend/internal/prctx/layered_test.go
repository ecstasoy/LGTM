package prctx

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/index"
)

// stubRetriever 受控返回预设 References；用于测 L4 过滤逻辑（阈值 + 去重）
type stubRetriever struct {
	refs []index.Reference
}

func (s stubRetriever) Retrieve(_ context.Context, _, _ string, _ int) ([]index.Reference, error) {
	return s.refs, nil
}

func newPR(files []github.File) github.PullRequest {
	return github.PullRequest{
		Owner:   "golang",
		Repo:    "go",
		Number:  42,
		HeadSHA: "deadbeef",
		Title:   "fix race",
		Body:    "fixes #123",
		Files:   files,
	}
}

func TestLayered_BasicMeta(t *testing.T) {
	b := NewLayeredBuilder()
	pr := newPR([]github.File{
		{Path: "a.go", Status: "modified", Patch: "@@ small @@", Additions: 1, Deletions: 1},
	})
	ctx, err := b.Build(context.Background(), pr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if !strings.Contains(ctx.L1Meta, "golang/go#42") {
		t.Errorf("L1 缺仓库标识: %q", ctx.L1Meta)
	}
	if !strings.Contains(ctx.L1Meta, "fix race") {
		t.Errorf("L1 缺标题: %q", ctx.L1Meta)
	}
	if !strings.Contains(ctx.L1Meta, "- a.go (modified) +1 -1") {
		t.Errorf("L1 缺 per-file 统计: %q", ctx.L1Meta)
	}
}

// 分页后大 PR 可能上千文件；L1 逐行列全部会自己撑爆预算并 hard-error。
// L1 文件清单应封顶，Build 仍能成功（文件 patch 的裁剪由 L2 + Dropped 负责）。
func TestLayered_ManyFiles_L1DoesNotOverflow(t *testing.T) {
	b := NewLayeredBuilder() // 默认 48000
	files := make([]github.File, 4000)
	for i := range files {
		files[i] = github.File{
			Path:      fmt.Sprintf("internal/pkg/module/file_%04d.go", i),
			Status:    "modified",
			Patch:     "@@ -1 +1 @@\n-a\n+b",
			Additions: 1,
		}
	}
	ctx, err := b.Build(context.Background(), newPR(files))
	if err != nil {
		t.Fatalf("4000 文件不应让 L1 撑爆预算: %v", err)
	}
	// 文件总数仍要如实呈现，且 L1 不再逐行列全部
	if !strings.Contains(ctx.L1Meta, "改动 4000 个文件") {
		t.Errorf("L1 应保留真实文件总数: %q", firstN(ctx.L1Meta, 80))
	}
	if !strings.Contains(ctx.L1Meta, "未逐一列出") {
		t.Errorf("L1 文件清单应封顶并标注省略: %q", firstN(ctx.L1Meta, 200))
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func TestLayered_PatchesFitInBudget(t *testing.T) {
	b := NewLayeredBuilder()
	pr := newPR([]github.File{
		{Path: "a.go", Patch: strings.Repeat("a", 100)},
		{Path: "b.go", Patch: strings.Repeat("b", 100)},
	})
	ctx, err := b.Build(context.Background(), pr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(ctx.L2Files) != 2 {
		t.Errorf("两个小文件都应保留，得到 %d 个", len(ctx.L2Files))
	}
	if len(ctx.BudgetReport.Dropped) != 0 {
		t.Errorf("不应有 Dropped，得到 %v", ctx.BudgetReport.Dropped)
	}
}

func TestLayered_DropsLargeFiles(t *testing.T) {
	// 限到 2000 tokens；L1 + L3 占去一部分，剩余 L2 大约 ~1500
	b := NewLayeredBuilder(WithTokenLimit(2000))
	pr := newPR([]github.File{
		{Path: "small.go", Patch: strings.Repeat("a", 100)},  // ~33 tokens
		{Path: "huge.go", Patch: strings.Repeat("b", 50000)}, // ~16667 tokens，必丢
	})
	ctx, err := b.Build(context.Background(), pr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	var paths []string
	for _, f := range ctx.L2Files {
		paths = append(paths, f.Path)
	}
	if !contains(paths, "small.go") {
		t.Errorf("small.go 应保留: %v", paths)
	}
	if contains(paths, "huge.go") {
		t.Errorf("huge.go 不应保留: %v", paths)
	}
	if !contains(ctx.BudgetReport.Dropped, "huge.go") {
		t.Errorf("huge.go 应在 Dropped: %v", ctx.BudgetReport.Dropped)
	}
}

func TestLayered_L3Conventions(t *testing.T) {
	b := NewLayeredBuilder()
	pr := newPR([]github.File{{Path: "a.go", Patch: "x"}})
	pr.Conventions = github.Conventions{
		Readme:    "# Go Repo",
		AgentDocs: "Use errors.Is for sentinels.",
	}

	ctx, err := b.Build(context.Background(), pr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(ctx.L3Conventions, "## README") {
		t.Errorf("L3 缺 README 段: %q", ctx.L3Conventions)
	}
	if !strings.Contains(ctx.L3Conventions, "## AGENT DOCS") {
		t.Errorf("L3 缺 AGENT DOCS 段: %q", ctx.L3Conventions)
	}
	if !strings.Contains(ctx.L3Conventions, "errors.Is") {
		t.Errorf("L3 内容错: %q", ctx.L3Conventions)
	}
}

func TestLayered_L3TruncatedWhenLarge(t *testing.T) {
	b := NewLayeredBuilder(WithTokenLimit(2000))
	pr := newPR([]github.File{{Path: "a.go", Patch: "x"}})
	// L3 预算 = 2000/10 = 200 tokens ≈ 600 chars；写 100K chars 必截断
	pr.Conventions = github.Conventions{Readme: strings.Repeat("R", 100000)}

	ctx, err := b.Build(context.Background(), pr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.HasSuffix(ctx.L3Conventions, "truncated]") {
		t.Errorf("L3 应被截断带 truncated 标记: 末尾 = %q", ctx.L3Conventions[max(0, len(ctx.L3Conventions)-30):])
	}
	if ctx.BudgetReport.UsedL3 > 250 {
		t.Errorf("L3 用 tokens 应 < 250，得到 %d", ctx.BudgetReport.UsedL3)
	}
}

func TestLayered_BudgetReport(t *testing.T) {
	b := NewLayeredBuilder(WithTokenLimit(10000))
	pr := newPR([]github.File{
		{Path: "a.go", Patch: strings.Repeat("a", 300)},
	})
	ctx, err := b.Build(context.Background(), pr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	r := ctx.BudgetReport
	if r.TokenLimit != 10000 {
		t.Errorf("TokenLimit=%d", r.TokenLimit)
	}
	if r.UsedL1 == 0 {
		t.Errorf("UsedL1 应 > 0")
	}
	if r.UsedL2 == 0 {
		t.Errorf("UsedL2 应 > 0")
	}
	if r.UsedL3 != 0 {
		t.Errorf("空 conventions 时 UsedL3 应 = 0，得到 %d", r.UsedL3)
	}
	if len(r.Dropped) != 0 {
		t.Errorf("无丢弃时 Dropped 应空: %v", r.Dropped)
	}
}

// TestLayered_L4_ScoreThresholdDrops 验证 cosine < defaultRAGScoreThreshold 的召回不入 L4
func TestLayered_L4_ScoreThresholdDrops(t *testing.T) {
	b := NewLayeredBuilder(WithRetriever(stubRetriever{refs: []index.Reference{
		{File: "other.go", Snippet: "good match", Score: 0.8, PRNumber: 76}, // 高于阈值，保留
		{File: "noise.go", Snippet: "bad match", Score: 0.1, PRNumber: 99}, // 远低于阈值，过滤
		{File: "weak.go", Snippet: "weak match", Score: 0.2, PRNumber: 88}, // 同上
	}}))
	pr := newPR([]github.File{{Path: "a.go", Patch: "small"}})
	ctx, err := b.Build(context.Background(), pr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(ctx.L4References) != 1 {
		t.Fatalf("expected 1 ref above threshold, got %d", len(ctx.L4References))
	}
	if ctx.L4References[0].File != "other.go" {
		t.Errorf("expected other.go, got %s", ctx.L4References[0].File)
	}
	if ctx.L4References[0].PRNumber != 76 {
		t.Errorf("PRNumber 未保留: %d", ctx.L4References[0].PRNumber)
	}
}

// TestLayered_L4_DedupesL2Paths 验证 L4 召回的 path 若已在 L2 出现则跳过
func TestLayered_L4_DedupesL2Paths(t *testing.T) {
	b := NewLayeredBuilder(WithRetriever(stubRetriever{refs: []index.Reference{
		{File: "a.go", Snippet: "from same PR", Score: 0.9, PRNumber: 42},  // 应被 L2 去重过滤
		{File: "other.go", Snippet: "cross-file", Score: 0.7, PRNumber: 76}, // 保留
	}}))
	pr := newPR([]github.File{
		{Path: "a.go", Patch: "this is in L2"}, // 同 path 会进 L2
	})
	ctx, err := b.Build(context.Background(), pr)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, ref := range ctx.L4References {
		if ref.File == "a.go" {
			t.Errorf("L4 不应含 L2 已有的 path: %s", ref.File)
		}
	}
	if len(ctx.L4References) != 1 || ctx.L4References[0].File != "other.go" {
		t.Errorf("expected only other.go in L4, got %+v", ctx.L4References)
	}
}

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
