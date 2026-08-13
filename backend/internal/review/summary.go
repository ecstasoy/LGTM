package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ecstasoy/LGTM/backend/internal/i18n"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
	"github.com/ecstasoy/LGTM/backend/internal/prompts"
)

// SummaryStage 渲染 summary.<locale>.tmpl，吃 L1 + L2 + L3，输出 markdown 总结。
type SummaryStage struct {
	Model       string  // 覆盖 provider 默认 model；空串走默认
	Temperature float32 // 0 走 provider 默认
}

// Name 实现 Stage
func (SummaryStage) Name() string { return "summary" }

// Run 实现 Stage
func (s SummaryStage) Run(ctx context.Context, c prctx.Context, p llm.Provider) (<-chan Event, error) {
	tmpl, err := prompts.ParseFor("summary", i18n.ZH)
	if err != nil {
		return nil, fmt.Errorf("summary: load template: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, c); err != nil {
		return nil, fmt.Errorf("summary: render template: %w", err)
	}

	chunks, err := p.Stream(ctx, llm.Request{
		System:      "你是一位 code reviewer，回答请使用中文 Markdown。",
		User:        buf.String(),
		Model:       s.Model,
		Temperature: s.Temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("summary: stream: %w", err)
	}

	events := make(chan Event, 16)
	go func() {
		defer close(events)
		for chunk := range chunks {
			if chunk.Err != nil {
				emit(events, "error", map[string]string{"stage": "summary", "message": chunk.Err.Error()})
				return
			}
			if chunk.Done {
				emit(events, "done", map[string]string{"stage": "summary"})
				return
			}
			if chunk.Text == "" {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case events <- buildEvent("summary_delta", map[string]string{"delta": chunk.Text}):
			}
		}
	}()
	return events, nil
}

// emit 把 event 推入 channel；ctx 取消时安全退出。
func emit(ch chan<- Event, typ string, data any) {
	ch <- buildEvent(typ, data)
}

// buildEvent 把任意 payload 序列化成 Event。失败时 panic 不可能发生（map[string]string 一定能序列化）。
func buildEvent(typ string, data any) Event {
	raw, _ := json.Marshal(data)
	return Event{Type: typ, Data: raw}
}
