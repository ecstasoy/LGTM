// Package agent is the tool-calling agent loop.
// A review stage calls the LLM once; a stage can instead use Agent.Run
// to loop: LLM -> pick tool -> run tool -> feed result back -> ask again.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
)

// ErrMaxStepsReached the loop hit MaxSteps without converging (the LLM kept calling tools).
// Result.Output still holds the last assistant text for degraded display.
var ErrMaxStepsReached = errors.New("agent: max steps reached")

// ErrRepeatedToolCall the model repeated an identical tool call after being told it was a repeat.
var ErrRepeatedToolCall = errors.New("agent: model kept repeating the same tool call")

const defaultMaxSteps = 6

// ToolSpec OpenAI function-calling tool description.
// Aliases the llm type so Registry.Specs() can go straight into llm.Request.Tools.
type ToolSpec = llm.ToolSpec

// Tool one callable capability.
type Tool interface {
	Spec() ToolSpec
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry tools by name.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register registers by Spec().Name; the same name overwrites.
func (r *Registry) Register(t Tool) {
	r.tools[t.Spec().Name] = t
}

// Lookup finds a tool by name.
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Specs returns every ToolSpec for the prompt.
func (r *Registry) Specs() []ToolSpec {
	specs := make([]ToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		specs = append(specs, t.Spec())
	}
	return specs
}

// Result final output of an agent loop.
type Result struct {
	Output string
	Steps  int
}

// Agent one tool-calling loop.
//
// Optional callbacks (nil-safe) for SSE frames / logs / metrics / tracing;
// they only observe events and never change loop behavior.
type Agent struct {
	Provider llm.Provider
	Tools    *Registry
	MaxSteps int

	// OnToolCallStart before a tool runs; drives the frontend tool_call_start SSE frame
	OnToolCallStart func(ctx context.Context, call llm.ToolCall)
	// OnToolCallDone after a tool runs; result is about to be fed back as a tool message.
	// Drives the tool_call_done SSE frame. result includes execution error strings ("error: ...").
	OnToolCallDone func(ctx context.Context, call llm.ToolCall, result string)
	// OnText each streamed assistant text delta; lets the frontend show the model thinking live
	// Called once per chunk as it arrives
	OnText func(ctx context.Context, delta string)
}

// Run runs the ReAct loop: LLM -> tool_calls? -> run tools -> feed results back as role=tool -> LLM again.
// At most MaxSteps rounds (default 6); when exhausted returns ErrMaxStepsReached with the last text in Result.Output.
//
// Two Request modes:
//   - Messages non-empty: used as the initial conversation
//   - System / User: assembled into system + user messages (single-shot stage style)
//
// A tool error (Run returns err) doesn't abort the loop: the error text is fed back so the LLM can react.
// Unknown tools are fed back the same way, so a hallucinated tool name doesn't fail the whole run.
func (a *Agent) Run(ctx context.Context, req llm.Request) (Result, error) {
	if a.Provider == nil {
		return Result{}, errors.New("agent: Provider is nil")
	}
	maxSteps := a.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}

	msgs := append([]llm.Message(nil), req.Messages...)
	if len(msgs) == 0 {
		if req.System != "" {
			msgs = append(msgs, llm.Message{Role: "system", Content: req.System})
		}
		if req.User != "" {
			msgs = append(msgs, llm.Message{Role: "user", Content: req.User})
		}
	}

	var specs []llm.ToolSpec
	if a.Tools != nil {
		specs = a.Tools.Specs()
	}

	var lastText strings.Builder
	seen := make(map[string]string) // name+args -> first result
	warned := make(map[string]bool) // repeats the model has already been told about
	for step := 0; step < maxSteps; step++ {
		lastText.Reset()
		var calls []llm.ToolCall

		ch, err := a.Provider.Stream(ctx, llm.Request{
			Messages:    msgs,
			Tools:       specs,
			Temperature: req.Temperature,
			Model:       req.Model,
			JSONSchema:  req.JSONSchema,
		})
		if err != nil {
			return Result{Steps: step}, fmt.Errorf("agent step %d: %w", step, err)
		}
		for c := range ch {
			if c.Err != nil {
				return Result{Output: lastText.String(), Steps: step}, fmt.Errorf("agent step %d: %w", step, c.Err)
			}
			if c.Done {
				break
			}
			if c.Text != "" {
				lastText.WriteString(c.Text)
				if a.OnText != nil {
					a.OnText(ctx, c.Text)
				}
			}
			if len(c.ToolCalls) > 0 {
				calls = append(calls, c.ToolCalls...)
			}
		}

		// no tool_calls: this round's assistant text is the final answer
		if len(calls) == 0 {
			return Result{Output: lastText.String(), Steps: step + 1}, nil
		}

		// feed back this round's assistant message (with tool_calls) plus each tool result
		msgs = append(msgs, llm.Message{
			Role:      "assistant",
			Content:   lastText.String(),
			ToolCalls: calls,
		})
		for _, tc := range calls {
			key := toolCallKey(tc.Name, tc.Arguments)
			if warned[key] {
				return Result{Output: lastText.String(), Steps: step + 1}, ErrRepeatedToolCall
			}
			if a.OnToolCallStart != nil {
				a.OnToolCallStart(ctx, tc)
			}
			result, repeated := seen[key]
			if repeated {
				warned[key] = true
				result = fmt.Sprintf("note: you already called %s with these exact arguments; "+
					"reusing that result instead of running it again. "+
					"Use different arguments or answer from what you have.\n\n%s", tc.Name, result)
			} else {
				result = a.runTool(ctx, tc)
				seen[key] = result
			}
			if a.OnToolCallDone != nil {
				a.OnToolCallDone(ctx, tc, result)
			}
			msgs = append(msgs, llm.Message{
				Role:       "tool",
				Name:       tc.Name,
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
	}

	// MaxSteps exhausted: return the last text plus an explicit error so the caller can degrade
	return Result{Output: lastText.String(), Steps: maxSteps}, ErrMaxStepsReached
}

// toolCallKey identifies a call by name and canonical JSON args, so key order and spacing don't matter.
func toolCallKey(name, args string) string {
	var v any
	if err := json.Unmarshal([]byte(args), &v); err == nil {
		if canon, err := json.Marshal(v); err == nil {
			return name + "\x00" + string(canon)
		}
	}
	return name + "\x00" + strings.TrimSpace(args)
}

// runTool one tool call; unknown tools and execution errors both come back as strings to feed back, never aborting the loop.
func (a *Agent) runTool(ctx context.Context, tc llm.ToolCall) string {
	if a.Tools == nil {
		return fmt.Sprintf("error: no tool registry configured (asked for %q)", tc.Name)
	}
	tool, ok := a.Tools.Lookup(tc.Name)
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", tc.Name)
	}
	out, err := tool.Run(ctx, json.RawMessage(tc.Arguments))
	if err != nil {
		return fmt.Sprintf("error: %s", err.Error())
	}
	return out
}
