package prompts_test

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"text/template"
	"text/template/parse"

	"github.com/ecstasoy/LGTM/backend/internal/i18n"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
	"github.com/ecstasoy/LGTM/backend/internal/prompts"
)

var allTemplates = []string{
	"summary.zh.tmpl", "risks.zh.tmpl", "suggestions.zh.tmpl",
	"summary.en.tmpl", "risks.en.tmpl", "suggestions.en.tmpl",
}

var zhTemplates = []string{"summary.zh.tmpl", "risks.zh.tmpl", "suggestions.zh.tmpl"}

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
	for _, name := range zhTemplates {
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
	for _, name := range []string{"risks.zh.tmpl", "suggestions.zh.tmpl"} {
		out := render(t, name, minimalCtx())
		if !strings.Contains(out, "示例") {
			t.Errorf("%s 缺少 few-shot 示例段", name)
		}
	}
}

// 误报护栏：risks 必须明确「不要报告」的清单，降低误报。
func TestRisksTemplate_HasFalsePositiveGuardrails(t *testing.T) {
	out := render(t, "risks.zh.tmpl", minimalCtx())
	if !strings.Contains(out, "不要报告") {
		t.Errorf("risks.zh.tmpl 缺少误报护栏（不要报告 清单）:\n%s", out)
	}
}

// 建议护栏：suggestions 不得给破坏性 / 改变语义的改写。
func TestSuggestionsTemplate_HasGuardrails(t *testing.T) {
	out := render(t, "suggestions.zh.tmpl", minimalCtx())
	if !strings.Contains(out, "不要建议") {
		t.Errorf("suggestions.zh.tmpl 缺少建议护栏:\n%s", out)
	}
}

// 新增评审维度：破坏性变更类别 + 测试缺口 + PR 描述对齐。
func TestRisksTemplate_CoversNewDimensions(t *testing.T) {
	out := render(t, "risks.zh.tmpl", minimalCtx())
	for _, want := range []string{"breaking", "破坏性", "测试", "描述"} {
		if !strings.Contains(out, want) {
			t.Errorf("risks.zh.tmpl 缺少维度关键词 %q", want)
		}
	}
}

// Every stage must have a template in every supported locale.
func TestParseForCoversEveryStageAndLocale(t *testing.T) {
	for _, stage := range []string{"summary", "risks", "suggestions"} {
		for _, locale := range []i18n.Locale{i18n.ZH, i18n.EN} {
			if _, err := prompts.ParseFor(stage, locale); err != nil {
				t.Errorf("ParseFor(%q, %q): %v", stage, locale, err)
			}
		}
	}
}

// The English templates must carry the same guardrails as the Chinese ones.
func TestEnglishTemplates_MirrorGuardrails(t *testing.T) {
	if out := render(t, "risks.en.tmpl", minimalCtx()); !strings.Contains(out, "Do not report") {
		t.Errorf("risks.en.tmpl is missing the false-positive guardrail list:\n%s", out)
	}
	if out := render(t, "suggestions.en.tmpl", minimalCtx()); !strings.Contains(out, "Do not suggest") {
		t.Errorf("suggestions.en.tmpl is missing the suggestion guardrail list:\n%s", out)
	}
	for _, name := range []string{"risks.en.tmpl", "suggestions.en.tmpl"} {
		if out := render(t, name, minimalCtx()); !strings.Contains(out, "Example") {
			t.Errorf("%s is missing the few-shot example section", name)
		}
	}
	out := render(t, "risks.en.tmpl", minimalCtx())
	for _, want := range []string{"breaking", "Breaking changes", "Test gaps", "PR description alignment"} {
		if !strings.Contains(out, want) {
			t.Errorf("risks.en.tmpl is missing the dimension keyword %q", want)
		}
	}
}

// With nothing dropped the excluded-files section must stay out of the English prompts too.
func TestEnglishTemplates_OmitDroppedSectionWhenEmpty(t *testing.T) {
	for _, name := range []string{"summary.en.tmpl", "risks.en.tmpl", "suggestions.en.tmpl"} {
		if out := render(t, name, minimalCtx()); strings.Contains(out, "over the context budget") {
			t.Errorf("%s should not emit the excluded-files section when nothing was dropped:\n%s", name, out)
		}
	}
}

// --- locale parity ---------------------------------------------------------
//
// The zh and en templates must stay structurally identical: same actions, same
// nesting, same order, and — for the JSON stages — the same wire contract. This
// is checked against the parse tree rather than by grepping for "{{...}}",
// because a sorted grep hides reordering and duplicate counts.

// astShape flattens a template into a depth-tagged list of node kinds and pipelines.
// Text is recorded by kind only: its content is exactly what is meant to differ.
func astShape(tmpl *template.Template) []string {
	var out []string
	var walk func(n parse.Node, depth int)
	walk = func(n parse.Node, depth int) {
		if n == nil {
			return
		}
		indent := strings.Repeat("  ", depth)
		switch v := n.(type) {
		case *parse.ListNode:
			if v == nil { // an absent {{ else }} arrives as a typed-nil *ListNode
				return
			}
			out = append(out, indent+"List")
			for _, child := range v.Nodes {
				walk(child, depth+1)
			}
		case *parse.TextNode:
			out = append(out, indent+"Text")
		case *parse.ActionNode:
			out = append(out, indent+"Action "+v.Pipe.String())
		case *parse.IfNode:
			out = append(out, indent+"If "+v.Pipe.String())
			walk(v.List, depth+1)
			walk(v.ElseList, depth+1)
		case *parse.RangeNode:
			out = append(out, indent+"Range "+v.Pipe.String())
			walk(v.List, depth+1)
			walk(v.ElseList, depth+1)
		case *parse.WithNode:
			out = append(out, indent+"With "+v.Pipe.String())
			walk(v.List, depth+1)
			walk(v.ElseList, depth+1)
		default:
			out = append(out, indent+fmt.Sprintf("%T", v))
		}
	}
	walk(tmpl.Tree.Root, 0)
	return out
}

func mustSource(t *testing.T, stage string, locale i18n.Locale) string {
	t.Helper()
	src, err := os.ReadFile(fmt.Sprintf("%s.%s.tmpl", stage, locale))
	if err != nil {
		t.Fatalf("read %s.%s.tmpl: %v", stage, locale, err)
	}
	return string(src)
}

var (
	jsonFenceRe = regexp.MustCompile("(?s)```json\\n(.*?)```")
	enumLineRe  = regexp.MustCompile("(?m)^- `(?:severity|category|type)`:.*$")
	headingRe   = regexp.MustCompile(`(?m)^#{1,6} `)
)

// jsonStrValRe matches one "key": "value" pair inside a JSON block.
var jsonStrValRe = regexp.MustCompile(`"([a-zA-Z_]+)"(\s*:\s*)"(?:[^"\\]|\\.)*"`)

// redactProse blanks the human-readable string values in a JSON block while leaving
// the enum-valued fields — the ones the decoders and the frontend switch on — intact.
// What survives is the part of the block that must not drift between locales.
func redactProse(block string) string {
	return jsonStrValRe.ReplaceAllStringFunc(block, func(m string) string {
		g := jsonStrValRe.FindStringSubmatch(m)
		switch g[1] {
		case "severity", "category", "type", "lang":
			return m
		}
		return `"` + g[1] + `"` + g[2] + `"<prose>"`
	})
}

// Same actions, same nesting, same order in both locales.
func TestLocaleParity_ActionTreeIsIdentical(t *testing.T) {
	for _, stage := range []string{"summary", "risks", "suggestions"} {
		t.Run(stage, func(t *testing.T) {
			zh, err := prompts.ParseFor(stage, i18n.ZH)
			if err != nil {
				t.Fatalf("parse zh: %v", err)
			}
			en, err := prompts.ParseFor(stage, i18n.EN)
			if err != nil {
				t.Fatalf("parse en: %v", err)
			}
			zhShape, enShape := astShape(zh), astShape(en)
			if !reflect.DeepEqual(zhShape, enShape) {
				t.Errorf("%s: action tree differs across locales\nzh (%d nodes):\n%s\n\nen (%d nodes):\n%s",
					stage, len(zhShape), strings.Join(zhShape, "\n"), len(enShape), strings.Join(enShape, "\n"))
			}
		})
	}
}

// The JSON stages ship a wire contract: skeletons and enum lines must be byte-identical.
func TestLocaleParity_JSONContractIsByteIdentical(t *testing.T) {
	for _, stage := range []string{"risks", "suggestions"} {
		t.Run(stage, func(t *testing.T) {
			zhSrc, enSrc := mustSource(t, stage, i18n.ZH), mustSource(t, stage, i18n.EN)

			zhEnums, enEnums := enumLineRe.FindAllString(zhSrc, -1), enumLineRe.FindAllString(enSrc, -1)
			if len(zhEnums) == 0 {
				t.Fatalf("%s: found no enum constraint lines; the test's own regexp is stale", stage)
			}
			if !reflect.DeepEqual(zhEnums, enEnums) {
				t.Errorf("%s: enum constraint lines differ\nzh: %q\nen: %q", stage, zhEnums, enEnums)
			}

			zhJSON, enJSON := jsonFenceRe.FindAllString(zhSrc, -1), jsonFenceRe.FindAllString(enSrc, -1)
			if len(zhJSON) == 0 {
				t.Fatalf("%s: found no ```json blocks; the test's own regexp is stale", stage)
			}
			if len(zhJSON) != len(enJSON) {
				t.Fatalf("%s: %d json blocks in zh vs %d in en", stage, len(zhJSON), len(enJSON))
			}
			// Only free prose inside the blocks is allowed to differ. Redact it and the
			// two locales must be byte-identical: same keys in the same order, same
			// nesting, same numbers, same enum values, same punctuation.
			for i := range zhJSON {
				if zhRed, enRed := redactProse(zhJSON[i]), redactProse(enJSON[i]); zhRed != enRed {
					t.Errorf("%s: json block %d differs beyond its prose\nzh:\n%s\nen:\n%s", stage, i, zhRed, enRed)
				}
			}
		})
	}
}

// A section added to only one locale shows up as a heading count mismatch.
func TestLocaleParity_HeadingCountsMatch(t *testing.T) {
	for _, stage := range []string{"summary", "risks", "suggestions"} {
		zh := len(headingRe.FindAllString(mustSource(t, stage, i18n.ZH), -1))
		en := len(headingRe.FindAllString(mustSource(t, stage, i18n.EN), -1))
		if zh != en {
			t.Errorf("%s: %d headings in zh vs %d in en", stage, zh, en)
		}
	}
}
