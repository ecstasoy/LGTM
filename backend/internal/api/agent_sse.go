package api

import (
	"context"
	"net/http"

	"github.com/ecstasoy/LGTM/backend/internal/agent"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
)

// WireAgentSSE 把 SSE writer 接到 Agent 的 callbacks：tool 调用 / text 增量
// 写成 SSE 帧推给前端 lib/sse.ts。覆盖现有 callback 字段（调用方应在 Run 之前调一次）。
//
// SSE 帧约定（前端消费）：
//
//	event: tool_call_start
//	data:  { "id": "call_abc", "name": "read_file", "arguments": "{\"file\":\"main.go\"}" }
//
//	event: tool_call_done
//	data:  { "id": "call_abc", "name": "read_file", "result": "..." }
//
//	event: agent_text_delta
//	data:  { "delta": "..." }
//
// 与 review.go 现有 `summary_delta` 区分：agent_text_delta 是 agent loop 自己的文本，
// summary_delta 是 review SummaryStage 流式。前端时间线把 tool_call_* 渲染成额外步骤。
func WireAgentSSE(a *agent.Agent, w http.ResponseWriter) {
	a.OnToolCallStart = func(_ context.Context, c llm.ToolCall) {
		writeSSE(w, "tool_call_start", map[string]any{
			"id":        c.ID,
			"name":      c.Name,
			"arguments": c.Arguments,
		})
		flush(w)
	}
	a.OnToolCallDone = func(_ context.Context, c llm.ToolCall, result string) {
		writeSSE(w, "tool_call_done", map[string]any{
			"id":     c.ID,
			"name":   c.Name,
			"result": result,
		})
		flush(w)
	}
	a.OnText = func(_ context.Context, delta string) {
		writeSSE(w, "agent_text_delta", map[string]string{"delta": delta})
		flush(w)
	}
}

// flush 立即推 buf；agent loop tool 调用之间可能间隔较长，必须每帧 flush 让浏览器
// 实时收到（gin 默认 buffer + Flusher 接口）
func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
