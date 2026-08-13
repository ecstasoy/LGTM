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

// Patch 一条建议附带的代码改写片段（可选）。
type Patch struct {
	Lang   string `json:"lang"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// Suggestion suggestions_done 事件 payload 中的一项。
// 扩展自 prototype/ 的 data.js shape：title + body 区分摘要与细节；
// patch 可选 —— LLM 给不出具体代码改写时省略，UI 仅展示 title + body。
type Suggestion struct {
	File  string `json:"file"`
	Line  int    `json:"line"`
	Type  string `json:"type"` // bug | style | perf | security | concurrency
	Title string `json:"title"`
	Body  string `json:"body"`
	Patch *Patch `json:"patch,omitempty"`
}

// suggestionsSystemByLocale carries the persona and the output-format instruction; the stage template carries the task.
// The EN entry spells out "in English" explicitly: suggestions has free-text title/body fields (unlike risks'
// single "reason" field), and without this it is the stage most likely to still answer in Chinese.
var suggestionsSystemByLocale = map[i18n.Locale]string{
	i18n.ZH: "你是一位 code reviewer，仅按要求输出严格 JSON。",
	i18n.EN: "You are a code reviewer. Emit strict JSON exactly as specified, nothing else. Write every string field in English.",
}

// SuggestionsStage 渲染 suggestions.<locale>.tmpl，强制 JSON 输出，解析后 emit 一次 suggestions_done。
type SuggestionsStage struct {
	Model       string
	Temperature float32
	Locale      i18n.Locale // Output language; a zero value fails template/system lookup, so callers must set it explicitly.
}

func (SuggestionsStage) Name() string { return "suggestions" }

func (s SuggestionsStage) Run(ctx context.Context, c prctx.Context, p llm.Provider) (<-chan Event, error) {
	tmpl, err := prompts.ParseFor("suggestions", s.Locale)
	if err != nil {
		return nil, fmt.Errorf("suggestions: load template: %w", err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, c); err != nil {
		return nil, fmt.Errorf("suggestions: render template: %w", err)
	}

	chunks, err := p.Stream(ctx, llm.Request{
		System:      suggestionsSystemByLocale[s.Locale],
		User:        buf.String(),
		Model:       s.Model,
		Temperature: s.Temperature,
		JSONSchema:  &llm.Schema{Name: "suggestions"}, // 触发 provider 的 json_object 模式
	})
	if err != nil {
		return nil, fmt.Errorf("suggestions: stream: %w", err)
	}

	events := make(chan Event, 4)
	go func() {
		defer close(events)

		// 拼成完整 JSON
		var raw strings.Builder
		for chunk := range chunks {
			if chunk.Err != nil {
				emit(events, "error", map[string]string{"stage": "suggestions", "message": chunk.Err.Error()})
				return
			}
			if chunk.Done {
				break
			}
			raw.WriteString(chunk.Text)
		}

		// 解析 {"suggestions":[...]}；用指针区分 missing vs []
		var parsed struct {
			Suggestions *[]Suggestion `json:"suggestions"`
		}
		if err := json.Unmarshal([]byte(raw.String()), &parsed); err != nil {
			emit(events, "error", map[string]string{
				"stage":   "suggestions",
				"message": fmt.Sprintf("parse suggestions JSON: %v", err),
			})
			return
		}
		if parsed.Suggestions == nil {
			emit(events, "error", map[string]string{
				"stage":   "suggestions",
				"message": "parse suggestions JSON: missing suggestions array",
			})
			return
		}

		select {
		case <-ctx.Done():
			return
		case events <- buildEvent("suggestions_done", *parsed.Suggestions):
		}
		emit(events, "done", map[string]string{"stage": "suggestions"})
	}()
	return events, nil
}
