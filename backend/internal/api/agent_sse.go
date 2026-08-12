package api

import (
	"context"
	"net/http"

	"github.com/ecstasoy/LGTM/backend/internal/agent"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
)

// WireAgentSSE hooks an SSE writer up to the Agent's callbacks, turning tool calls / text deltas
// into SSE frames for the frontend's lib/sse.ts. It overwrites the existing callback fields (call it once, before Run).
//
// SSE frame contract (as consumed by the frontend):
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
// Distinct from review.go's existing `summary_delta`: agent_text_delta is text from the agent loop itself,
// while summary_delta streams from the review SummaryStage. The frontend timeline renders tool_call_* as extra steps.
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

// flush pushes buf immediately; gaps between an agent loop's tool calls can be long, so every frame must be flushed
// for the browser to receive it live (gin buffers by default + the Flusher interface)
func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
