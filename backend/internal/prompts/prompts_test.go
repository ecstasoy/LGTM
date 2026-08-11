package prompts_test

import (
	"strings"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/prctx"
	"github.com/ecstasoy/LGTM/backend/internal/prompts"
)

var allTemplates = []string{"summary.tmpl", "risks.tmpl", "suggestions.tmpl"}

func render(t *testing.T, name string, c prctx.Context) string {
	t.Helper()
	tmpl, err := prompts.Parse(name)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, c); err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return sb.String()
}

// 被预算丢弃的文件必须出现在 prompt 里，让模型知道它没看到这些改动。
func TestTemplates_IncludeDroppedFiles(t *testing.T) {
	c := prctx.Context{
		L1Meta:  "仓库: o/r#1",
		L2Files: []prctx.FileContext{{Path: "a.go", Patch: "@@ -1 +1 @@"}},
		BudgetReport: prctx.BudgetReport{
			Dropped: []string{"big/gen.pb.go", "vendor/huge.go"},
		},
	}
	for _, name := range allTemplates {
		out := render(t, name, c)
		if !strings.Contains(out, "big/gen.pb.go") || !strings.Contains(out, "vendor/huge.go") {
			t.Errorf("%s 未列出被丢弃文件:\n%s", name, out)
		}
	}
}

// 无丢弃文件时不应出现「未纳入」段落，避免误导模型。
func TestTemplates_OmitDroppedSectionWhenEmpty(t *testing.T) {
	c := prctx.Context{
		L1Meta:  "仓库: o/r#1",
		L2Files: []prctx.FileContext{{Path: "a.go", Patch: "@@ -1 +1 @@"}},
	}
	for _, name := range allTemplates {
		out := render(t, name, c)
		if strings.Contains(out, "未纳入") {
			t.Errorf("%s 在无丢弃文件时不应出现未纳入段落:\n%s", name, out)
		}
	}
}

func minimalCtx() prctx.Context {
	return prctx.Context{
		L1Meta:  "仓库: o/r#1",
		L2Files: []prctx.FileContext{{Path: "a.go", Patch: "@@ -1 +1 @@"}},
	}
}

// few-shot：risks / suggestions 必须带具体示例，降低模型瞎猜 schema 与口径。
func TestTemplates_HaveFewShotExamples(t *testing.T) {
	for _, name := range []string{"risks.tmpl", "suggestions.tmpl"} {
		out := render(t, name, minimalCtx())
		if !strings.Contains(out, "示例") {
			t.Errorf("%s 缺少 few-shot 示例段", name)
		}
	}
}

// 误报护栏：risks 必须明确「不要报告」的清单，降低误报。
func TestRisksTemplate_HasFalsePositiveGuardrails(t *testing.T) {
	out := render(t, "risks.tmpl", minimalCtx())
	if !strings.Contains(out, "不要报告") {
		t.Errorf("risks.tmpl 缺少误报护栏（不要报告 清单）:\n%s", out)
	}
}

// 建议护栏：suggestions 不得给破坏性 / 改变语义的改写。
func TestSuggestionsTemplate_HasGuardrails(t *testing.T) {
	out := render(t, "suggestions.tmpl", minimalCtx())
	if !strings.Contains(out, "不要建议") {
		t.Errorf("suggestions.tmpl 缺少建议护栏:\n%s", out)
	}
}

// 新增评审维度：破坏性变更类别 + 测试缺口 + PR 描述对齐。
func TestRisksTemplate_CoversNewDimensions(t *testing.T) {
	out := render(t, "risks.tmpl", minimalCtx())
	for _, want := range []string{"breaking", "破坏性", "测试", "描述"} {
		if !strings.Contains(out, want) {
			t.Errorf("risks.tmpl 缺少维度关键词 %q", want)
		}
	}
}
