package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
)

// scriptedProvider returns preset chunks per call; for multi-round ReAct tests
type scriptedProvider struct {
	steps [][]llm.Chunk // one chunk group per Stream call
	calls []llm.Request // every request received, for assertions
}

func (p *scriptedProvider) Stream(_ context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	p.calls = append(p.calls, req)
	idx := len(p.calls) - 1
	if idx >= len(p.steps) {
		return nil, errors.New("scriptedProvider: ran out of script")
	}
	chunks := p.steps[idx]
	ch := make(chan llm.Chunk, len(chunks)+1)
	go func() {
		defer close(ch)
		for _, c := range chunks {
			ch <- c
		}
		ch <- llm.Chunk{Done: true}
	}()
	return ch, nil
}

// echoTool echoes args back so tests can assert the tool actually ran
type echoTool struct{ name string }

func (e *echoTool) Spec() ToolSpec {
	return ToolSpec{Name: e.name, Description: "echo args", Parameters: json.RawMessage(`{}`)}
}
func (e *echoTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	return "echo:" + string(args), nil
}

// failingTool always errors, for testing error feedback
type failingTool struct{}

func (f *failingTool) Spec() ToolSpec {
	return ToolSpec{Name: "boom", Description: "always fail", Parameters: json.RawMessage(`{}`)}
}
func (f *failingTool) Run(_ context.Context, _ json.RawMessage) (string, error) {
	return "", errors.New("boom failed on purpose")
}

func TestAgent_Run_NoToolCalls_ImmediateReturn(t *testing.T) {
	p := &scriptedProvider{steps: [][]llm.Chunk{
		{{Text: "no tool needed; here's the answer"}},
	}}
	a := &Agent{Provider: p, Tools: NewRegistry(), MaxSteps: 3}
	res, err := a.Run(context.Background(), llm.Request{User: "hi"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Steps != 1 {
		t.Errorf("want Steps=1, got %d", res.Steps)
	}
	if !strings.Contains(res.Output, "no tool needed") {
		t.Errorf("unexpected output: %q", res.Output)
	}
}

func TestAgent_Run_OneToolCall_ThenFinalText(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&echoTool{name: "echo"})

	p := &scriptedProvider{steps: [][]llm.Chunk{
		// step 1: LLM calls echo
		{{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: `{"x":1}`}}}},
		// step 2: LLM sees the result and answers
		{{Text: "done after tool"}},
	}}
	a := &Agent{Provider: p, Tools: reg, MaxSteps: 5}
	res, err := a.Run(context.Background(), llm.Request{User: "do it"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Steps != 2 {
		t.Errorf("want Steps=2, got %d", res.Steps)
	}
	if res.Output != "done after tool" {
		t.Errorf("unexpected output: %q", res.Output)
	}
	// the 2nd call's Messages should include assistant(tool_calls) + tool(result)
	if len(p.calls) != 2 {
		t.Fatalf("want 2 provider calls, got %d", len(p.calls))
	}
	msgs := p.calls[1].Messages
	if len(msgs) < 3 {
		t.Fatalf("want ≥3 msgs in 2nd call, got %d: %+v", len(msgs), msgs)
	}
	var sawTool bool
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == "c1" && strings.Contains(m.Content, "echo:") {
			sawTool = true
		}
	}
	if !sawTool {
		t.Errorf("回灌的 tool message 不正确: %+v", msgs)
	}
}

func TestAgent_Run_UnknownTool_ReturnsErrorString(t *testing.T) {
	p := &scriptedProvider{steps: [][]llm.Chunk{
		{{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "missing", Arguments: `{}`}}}},
		{{Text: "ok recovered"}},
	}}
	a := &Agent{Provider: p, Tools: NewRegistry(), MaxSteps: 5}
	res, err := a.Run(context.Background(), llm.Request{User: "go"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Output != "ok recovered" {
		t.Errorf("output=%q", res.Output)
	}
	// the 2nd call should see a tool message containing "unknown tool"
	toolMsg := p.calls[1].Messages[len(p.calls[1].Messages)-1]
	if !strings.Contains(toolMsg.Content, "unknown tool") {
		t.Errorf("应回灌 unknown tool err, got %q", toolMsg.Content)
	}
}

func TestAgent_Run_ToolError_ReturnsErrorString(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&failingTool{})

	p := &scriptedProvider{steps: [][]llm.Chunk{
		{{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "boom", Arguments: `{}`}}}},
		{{Text: "fallback answer"}},
	}}
	a := &Agent{Provider: p, Tools: reg, MaxSteps: 5}
	res, _ := a.Run(context.Background(), llm.Request{User: "go"})
	if res.Output != "fallback answer" {
		t.Errorf("output=%q", res.Output)
	}
	toolMsg := p.calls[1].Messages[len(p.calls[1].Messages)-1]
	if !strings.Contains(toolMsg.Content, "boom failed on purpose") {
		t.Errorf("应回灌 tool err 字符串, got %q", toolMsg.Content)
	}
}

func TestAgent_Run_MaxStepsReached(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&echoTool{name: "echo"})

	// distinct args each step: never converges, never trips the repeat guard
	step := func(n int) []llm.Chunk {
		return []llm.Chunk{
			{Text: "thinking..."},
			{ToolCalls: []llm.ToolCall{{ID: "c", Name: "echo", Arguments: fmt.Sprintf(`{"n":%d}`, n)}}},
		}
	}
	p := &scriptedProvider{steps: [][]llm.Chunk{step(1), step(2), step(3)}}
	a := &Agent{Provider: p, Tools: reg, MaxSteps: 3}
	res, err := a.Run(context.Background(), llm.Request{User: "loop"})
	if !errors.Is(err, ErrMaxStepsReached) {
		t.Errorf("want ErrMaxStepsReached, got %v", err)
	}
	if res.Steps != 3 {
		t.Errorf("want Steps=3, got %d", res.Steps)
	}
	// the last text should be carried out
	if !strings.Contains(res.Output, "thinking") {
		t.Errorf("max steps 时应返最后 text, got %q", res.Output)
	}
}

func TestAgent_Run_NilProvider_ImmediateError(t *testing.T) {
	a := &Agent{}
	_, err := a.Run(context.Background(), llm.Request{})
	if err == nil || !strings.Contains(err.Error(), "Provider is nil") {
		t.Errorf("want Provider is nil err, got %v", err)
	}
}

func TestAgent_Run_CallbacksFireInOrder(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&echoTool{name: "echo"})

	p := &scriptedProvider{steps: [][]llm.Chunk{
		// step 1: streamed text deltas + a tool_call
		{
			{Text: "thinking "},
			{Text: "first..."},
			{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: `{"x":1}`}}},
		},
		// step 2: final answer
		{{Text: "all done"}},
	}}

	var (
		textDeltas []string
		starts     []llm.ToolCall
		dones      []struct {
			call   llm.ToolCall
			result string
		}
	)
	a := &Agent{
		Provider: p,
		Tools:    reg,
		MaxSteps: 5,
		OnText: func(_ context.Context, d string) {
			textDeltas = append(textDeltas, d)
		},
		OnToolCallStart: func(_ context.Context, c llm.ToolCall) {
			starts = append(starts, c)
		},
		OnToolCallDone: func(_ context.Context, c llm.ToolCall, r string) {
			dones = append(dones, struct {
				call   llm.ToolCall
				result string
			}{c, r})
		},
	}
	_, err := a.Run(context.Background(), llm.Request{User: "go"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// text deltas: 2 in step 1 + 1 in step 2 = 3
	if len(textDeltas) != 3 {
		t.Errorf("want 3 text deltas, got %d: %v", len(textDeltas), textDeltas)
	}
	if strings.Join(textDeltas, "") != "thinking first...all done" {
		t.Errorf("text delta order off: %v", textDeltas)
	}

	// tool callbacks: one start + one done in step 1
	if len(starts) != 1 || starts[0].ID != "c1" || starts[0].Name != "echo" {
		t.Errorf("starts=%+v", starts)
	}
	if len(dones) != 1 || dones[0].call.ID != "c1" || !strings.Contains(dones[0].result, "echo:") {
		t.Errorf("dones=%+v", dones)
	}
}

func TestAgent_Run_NilCallbacks_Safe(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&echoTool{name: "echo"})
	p := &scriptedProvider{steps: [][]llm.Chunk{
		{{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: `{}`}}}},
		{{Text: "done"}},
	}}
	a := &Agent{Provider: p, Tools: reg, MaxSteps: 5}
	// all three callbacks nil; must not panic
	if _, err := a.Run(context.Background(), llm.Request{User: "go"}); err != nil {
		t.Fatalf("nil callbacks should be safe: %v", err)
	}
}

func TestAgent_Run_MessagesModeOverridesSystemUser(t *testing.T) {
	// Messages takes precedence; System/User should be ignored
	p := &scriptedProvider{steps: [][]llm.Chunk{{{Text: "ok"}}}}
	a := &Agent{Provider: p, Tools: NewRegistry()}
	_, _ = a.Run(context.Background(), llm.Request{
		System:   "ignored",
		User:     "ignored",
		Messages: []llm.Message{{Role: "user", Content: "real prompt"}},
	})
	first := p.calls[0]
	if len(first.Messages) != 1 || first.Messages[0].Content != "real prompt" {
		t.Errorf("Messages 模式未生效: %+v", first.Messages)
	}
}

// countingTool returns "result#N" where N is its invocation count.
type countingTool struct {
	name  string
	count int
}

func (c *countingTool) Spec() ToolSpec {
	return ToolSpec{Name: c.name, Description: "counts calls", Parameters: json.RawMessage(`{}`)}
}
func (c *countingTool) Run(_ context.Context, _ json.RawMessage) (string, error) {
	c.count++
	return fmt.Sprintf("result#%d", c.count), nil
}

func TestAgent_Run_RepeatedToolCall_ReusesResultInsteadOfRerunning(t *testing.T) {
	tool := &countingTool{name: "read_file"}
	reg := NewRegistry()
	reg.Register(tool)

	p := &scriptedProvider{steps: [][]llm.Chunk{
		{{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"file":"a.go"}`}}}},
		{{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "read_file", Arguments: `{"file":"a.go"}`}}}},
		{{Text: "final"}},
	}}
	a := &Agent{Provider: p, Tools: reg, MaxSteps: 5}
	res, err := a.Run(context.Background(), llm.Request{User: "go"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Output != "final" {
		t.Errorf("output=%q", res.Output)
	}
	if tool.count != 1 {
		t.Errorf("identical call should not re-run the tool; ran %d times", tool.count)
	}
	// Every tool_call_id needs a matching tool message or the next API call is rejected.
	msgs := p.calls[2].Messages
	last := msgs[len(msgs)-1]
	if last.Role != "tool" || last.ToolCallID != "c2" {
		t.Fatalf("want tool message for c2 last, got %+v", last)
	}
	if !strings.Contains(last.Content, "result#1") {
		t.Errorf("repeat should carry the earlier result, got %q", last.Content)
	}
	if last.Content == "result#1" {
		t.Errorf("repeat should tell the model it is a duplicate, got bare result")
	}
}

func TestAgent_Run_RepeatedToolCall_MatchesReorderedJSONArgs(t *testing.T) {
	tool := &countingTool{name: "grep_patches"}
	reg := NewRegistry()
	reg.Register(tool)

	p := &scriptedProvider{steps: [][]llm.Chunk{
		{{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "grep_patches", Arguments: `{"pattern":"TODO","regex":false}`}}}},
		{{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "grep_patches", Arguments: `{ "regex": false, "pattern": "TODO" }`}}}},
		{{Text: "final"}},
	}}
	a := &Agent{Provider: p, Tools: reg, MaxSteps: 5}
	if _, err := a.Run(context.Background(), llm.Request{User: "go"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if tool.count != 1 {
		t.Errorf("same JSON with reordered keys and spacing should count as a repeat; ran %d times", tool.count)
	}
}

func TestAgent_Run_DifferentArgs_RunEachTime(t *testing.T) {
	tool := &countingTool{name: "read_file"}
	reg := NewRegistry()
	reg.Register(tool)

	p := &scriptedProvider{steps: [][]llm.Chunk{
		{{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read_file", Arguments: `{"file":"a.go"}`}}}},
		{{ToolCalls: []llm.ToolCall{{ID: "c2", Name: "read_file", Arguments: `{"file":"b.go"}`}}}},
		{{Text: "final"}},
	}}
	a := &Agent{Provider: p, Tools: reg, MaxSteps: 5}
	if _, err := a.Run(context.Background(), llm.Request{User: "go"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if tool.count != 2 {
		t.Errorf("different args must run the tool again; ran %d times", tool.count)
	}
	last := p.calls[2].Messages[len(p.calls[2].Messages)-1]
	if last.Content != "result#2" {
		t.Errorf("non-repeat should get the plain fresh result, got %q", last.Content)
	}
}

func TestAgent_Run_PersistentRepeat_StopsEarly(t *testing.T) {
	tool := &countingTool{name: "read_file"}
	reg := NewRegistry()
	reg.Register(tool)

	loop := []llm.Chunk{
		{Text: "still looking"},
		{ToolCalls: []llm.ToolCall{{ID: "c", Name: "read_file", Arguments: `{"file":"a.go"}`}}},
	}
	p := &scriptedProvider{steps: [][]llm.Chunk{loop, loop, loop, loop, loop, loop, loop, loop, loop, loop}}
	var starts, dones int
	a := &Agent{
		Provider:        p,
		Tools:           reg,
		MaxSteps:        10,
		OnToolCallStart: func(context.Context, llm.ToolCall) { starts++ },
		OnToolCallDone:  func(context.Context, llm.ToolCall, string) { dones++ },
	}
	res, err := a.Run(context.Background(), llm.Request{User: "go"})
	if !errors.Is(err, ErrRepeatedToolCall) {
		t.Fatalf("want ErrRepeatedToolCall, got %v", err)
	}
	// model call 1 runs the tool, call 2 gets the repeat note, call 3 repeats anyway -> stop
	if len(p.calls) != 3 {
		t.Errorf("want stop after 3 model calls instead of burning all steps, got %d", len(p.calls))
	}
	if res.Steps != 3 {
		t.Errorf("want Steps=3, got %d", res.Steps)
	}
	if res.Output != "still looking" {
		t.Errorf("early stop should keep the last text for degraded display, got %q", res.Output)
	}
	if tool.count != 1 {
		t.Errorf("tool should have run once, ran %d times", tool.count)
	}
	if starts != dones {
		t.Errorf("every tool_call_start needs a done or the UI spinner never stops: starts=%d dones=%d", starts, dones)
	}
}
