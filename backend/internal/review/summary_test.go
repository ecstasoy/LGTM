package review

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/i18n"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
)

func TestSummaryStage_Run_WithMockProvider(t *testing.T) {
	p := llm.NewMockProvider()
	p.Reply = "Hello world from mock"

	stage := SummaryStage{Locale: i18n.ZH}
	ctx := prctx.Context{
		L1Meta:        "标题: fix bug\nbody: 修复空指针",
		L3Conventions: "用 errors.Is 检查 sentinel",
	}

	ch, err := stage.Run(context.Background(), ctx, p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var deltas []string
	doneSeen := false
	for ev := range ch {
		switch ev.Type {
		case "summary_delta":
			var payload struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal(ev.Data, &payload); err != nil {
				t.Fatalf("unmarshal delta: %v", err)
			}
			deltas = append(deltas, payload.Delta)
		case "done":
			doneSeen = true
		case "error":
			t.Fatalf("意外 error event: %s", string(ev.Data))
		}
	}

	joined := strings.Join(deltas, "")
	if !strings.Contains(joined, "Hello") || !strings.Contains(joined, "world") {
		t.Errorf("聚合 deltas 应含 Hello world，得到 %q", joined)
	}
	if !doneSeen {
		t.Error("应收到 done event")
	}
}

// TestSummaryStage_Run_SystemPromptByLocale pins the exact System string MockProvider.Stream receives for each
// locale, independent of summarySystemByLocale itself (the expected strings are hardcoded here rather than looked
// up from the map), so a typo'd or removed map entry fails this test instead of compiling away silently.
func TestSummaryStage_Run_SystemPromptByLocale(t *testing.T) {
	cases := []struct {
		locale i18n.Locale
		want   string
	}{
		{i18n.ZH, "你是一位 code reviewer，回答请使用中文 Markdown。"},
		{i18n.EN, "You are a code reviewer. Answer in English Markdown."},
	}
	for _, tc := range cases {
		t.Run(string(tc.locale), func(t *testing.T) {
			p := llm.NewMockProvider()
			p.Reply = "ok"
			stage := SummaryStage{Locale: tc.locale}
			ch, err := stage.Run(context.Background(), prctx.Context{L1Meta: "test"}, p)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			for range ch {
			}
			got := p.LastRequest()
			if got == nil {
				t.Fatal("LastRequest() = nil; Stream was never called")
			}
			if got.System != tc.want {
				t.Errorf("System = %q, want %q", got.System, tc.want)
			}
		})
	}
}

func TestSummaryStage_Run_StreamError(t *testing.T) {
	stage := SummaryStage{Locale: i18n.ZH}
	ctx := prctx.Context{L1Meta: "test"}

	// errProvider 直接返同步错误，模拟 Stream 失败（如鉴权）
	p := errProvider{err: streamErr{msg: "stream failed"}}
	_, err := stage.Run(context.Background(), ctx, p)
	if err == nil {
		t.Fatal("期望同步 Stream 错误向上冒")
	}
	if !strings.Contains(err.Error(), "stream") {
		t.Errorf("错误信息应含 stream，得到 %v", err)
	}
}

// streamErr 仅给测试用，自定义 Error 信息。
type streamErr struct{ msg string }

func (e streamErr) Error() string { return e.msg }

// errProvider 总是同步报错的 Provider。
type errProvider struct{ err error }

func (e errProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	return nil, e.err
}
