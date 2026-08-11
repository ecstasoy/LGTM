package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
)

// scriptedProvider 按调用次数返预设的 chunks；测试 ReAct 多轮场景用
type scriptedProvider struct {
	steps [][]llm.Chunk // 每次 Stream 调用返一组 chunks
	calls []llm.Request // 录所有收到的请求供断言
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

// echoTool 返回 args 原样的可读字符串，方便断言 tool 真被调
type echoTool struct{ name string }

func (e *echoTool) Spec() ToolSpec {
	return ToolSpec{Name: e.name, Description: "echo args", Parameters: json.RawMessage(`{}`)}
}
func (e *echoTool) Run(_ context.Context, args json.RawMessage) (string, error) {
	return "echo:" + string(args), nil
}

// failingTool 总是返 err，测错误回灌
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
		// 第 1 步：LLM 调 echo
		{{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: `{"x":1}`}}}},
		// 第 2 步：LLM 看到结果返最终答案
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
	// 第 2 次调用的 Messages 应该含 assistant(tool_calls) + tool(result)
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
	// 第二次调用应看到 tool 消息含 "unknown tool"
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

	// 让 LLM 每次都调 tool，永远不收敛
	loopStep := []llm.Chunk{
		{Text: "thinking..."},
		{ToolCalls: []llm.ToolCall{{ID: "c", Name: "echo", Arguments: `{}`}}},
	}
	p := &scriptedProvider{steps: [][]llm.Chunk{loopStep, loopStep, loopStep}}
	a := &Agent{Provider: p, Tools: reg, MaxSteps: 3}
	res, err := a.Run(context.Background(), llm.Request{User: "loop"})
	if !errors.Is(err, ErrMaxStepsReached) {
		t.Errorf("want ErrMaxStepsReached, got %v", err)
	}
	if res.Steps != 3 {
		t.Errorf("want Steps=3, got %d", res.Steps)
	}
	// 最后一次的 text 应该被带出来
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
		// 第 1 步：流式 text 增量 + tool_call
		{
			{Text: "thinking "},
			{Text: "first..."},
			{ToolCalls: []llm.ToolCall{{ID: "c1", Name: "echo", Arguments: `{"x":1}`}}},
		},
		// 第 2 步：最终答案
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

	// 文本增量：第 1 步 2 段 + 第 2 步 1 段 = 3 次
	if len(textDeltas) != 3 {
		t.Errorf("want 3 text deltas, got %d: %v", len(textDeltas), textDeltas)
	}
	if strings.Join(textDeltas, "") != "thinking first...all done" {
		t.Errorf("text delta order off: %v", textDeltas)
	}

	// tool callbacks：第 1 步 1 次 start + 1 次 done
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
	// 三个 callback 都 nil；不应 panic
	if _, err := a.Run(context.Background(), llm.Request{User: "go"}); err != nil {
		t.Fatalf("nil callbacks should be safe: %v", err)
	}
}

func TestAgent_Run_MessagesModeOverridesSystemUser(t *testing.T) {
	// Messages 非空时优先用；System/User 应被忽略
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
