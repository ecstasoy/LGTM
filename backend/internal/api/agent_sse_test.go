package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/agent"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
)

// verifies that all three WireAgentSSE callbacks write frames in the SSE protocol
func TestWireAgentSSE_WritesFrames(t *testing.T) {
	rec := httptest.NewRecorder()
	a := &agent.Agent{}
	WireAgentSSE(a, rec)

	a.OnText(context.Background(), "hello ")
	a.OnText(context.Background(), "world")
	a.OnToolCallStart(context.Background(), llm.ToolCall{
		ID: "c1", Name: "read_file", Arguments: `{"file":"main.go"}`,
	})
	a.OnToolCallDone(context.Background(), llm.ToolCall{
		ID: "c1", Name: "read_file",
	}, "file contents here")

	body := rec.Body.String()
	for _, want := range []string{
		"event: agent_text_delta",
		`"delta":"hello "`,
		`"delta":"world"`,
		"event: tool_call_start",
		`"name":"read_file"`,
		`"arguments":"{\"file\":\"main.go\"}"`,
		"event: tool_call_done",
		`"result":"file contents here"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody=%s", want, body)
		}
	}
}

func TestWireAgentSSE_OverwritesExistingCallbacks(t *testing.T) {
	rec := httptest.NewRecorder()
	called := false
	a := &agent.Agent{
		OnText: func(_ context.Context, _ string) { called = true },
	}
	WireAgentSSE(a, rec)
	a.OnText(context.Background(), "x")
	if called {
		t.Errorf("WireAgentSSE should overwrite existing OnText, but original was called")
	}
	if !strings.Contains(rec.Body.String(), "agent_text_delta") {
		t.Errorf("new OnText should write SSE frame, got %s", rec.Body.String())
	}
}
