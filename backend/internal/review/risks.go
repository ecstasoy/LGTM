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

// Risk risks_done 事件 payload 中的一项。
type Risk struct {
	File       string  `json:"file"`
	Line       int     `json:"line,omitempty"`
	Severity   string  `json:"severity"`   // high | medium | low
	Category   string  `json:"category"`   // bug | security | perf | style | concurrency | breaking | other
	Confidence float32 `json:"confidence"` // 0.0-1.0，前端按 ≥ 0.9 默认展开
	Reason     string  `json:"reason"`
}

// risksSystemByLocale carries the persona and the output-format instruction; the stage template carries the task.
var risksSystemByLocale = map[i18n.Locale]string{
	i18n.ZH: "你是一位 code reviewer，仅按要求输出严格 JSON。",
	i18n.EN: "You are a code reviewer. Emit strict JSON exactly as specified, nothing else.",
}

// RisksStage 渲染 risks.<locale>.tmpl，强制 JSON 输出，解析后 emit 一次 risks_done。
type RisksStage struct {
	Model       string
	Temperature float32
	Locale      i18n.Locale // Output language; a zero value fails template/system lookup, so callers must set it explicitly.
}

func (RisksStage) Name() string { return "risks" }

func (s RisksStage) Run(ctx context.Context, c prctx.Context, p llm.Provider) (<-chan Event, error) {
	tmpl, err := prompts.ParseFor("risks", s.Locale)
	if err != nil {
		return nil, fmt.Errorf("risks: load template: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, c); err != nil {
		return nil, fmt.Errorf("risks: render template: %w", err)
	}

	chunks, err := p.Stream(ctx, llm.Request{
		System:      risksSystemByLocale[s.Locale],
		User:        buf.String(),
		Model:       s.Model,
		Temperature: s.Temperature,
		JSONSchema:  &llm.Schema{Name: "risks"}, // 触发 provider 的 json_object 模式
	})
	if err != nil {
		return nil, fmt.Errorf("risks: stream: %w", err)
	}

	events := make(chan Event, 4)
	go func() {
		defer close(events)

		// 先把所有 chunk 拼成完整 JSON 文本
		var raw strings.Builder
		for chunk := range chunks {
			if chunk.Err != nil {
				emit(events, "error", map[string]string{"stage": "risks", "message": chunk.Err.Error()})
				return
			}
			if chunk.Done {
				break
			}
			raw.WriteString(chunk.Text)
		}

		// 解析 JSON {"risks":[...]}
		var parsed struct {
			Risks *[]Risk `json:"risks"`
		}
		if err := json.Unmarshal([]byte(raw.String()), &parsed); err != nil {
			emit(events, "error", map[string]string{
				"stage":   "risks",
				"message": fmt.Sprintf("parse risks JSON: %v", err),
			})
			return
		}
		if parsed.Risks == nil {
			emit(events, "error", map[string]string{
				"stage":   "risks",
				"message": "parse risks JSON: missing risks array",
			})
			return
		}

		select {
		case <-ctx.Done():
			return
		case events <- buildEvent("risks_done", *parsed.Risks):
		}
		emit(events, "done", map[string]string{"stage": "risks"})
	}()
	return events, nil
}
