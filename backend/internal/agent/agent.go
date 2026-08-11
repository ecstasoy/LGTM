// Package agent 是 v2 工具调用 / agent 循环的入口。
// v1 每个 review stage 只调一次 LLM；v2 可把某个 stage 换成 Agent.Run
// 实现"LLM -> 选工具 -> 跑工具 -> 回灌结果 -> 再问"的循环。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
)

// ErrMaxStepsReached agent loop 达到 MaxSteps 仍未收敛（LLM 一直在调工具）。
// Result.Output 仍含最后一次 assistant text 供调用方降级展示。
var ErrMaxStepsReached = errors.New("agent: max steps reached")

const defaultMaxSteps = 6

// ToolSpec OpenAI function-calling 风格的工具描述。
// 直接复用 llm 包的同形状定义：Registry.Specs() 可直传 llm.Request.Tools。
type ToolSpec = llm.ToolSpec

// Tool 一项可调用能力。
type Tool interface {
	Spec() ToolSpec
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry 按名字存放 Tool。
type Registry struct {
	tools map[string]Tool
}

// NewRegistry 空注册表。
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register 按 Spec().Name 注册;同名覆盖。
func (r *Registry) Register(t Tool) {
	r.tools[t.Spec().Name] = t
}

// Lookup 按名查 Tool。
func (r *Registry) Lookup(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Specs 返回所有 ToolSpec，供 prompt 注入。
func (r *Registry) Specs() []ToolSpec {
	specs := make([]ToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		specs = append(specs, t.Spec())
	}
	return specs
}

// Result Agent 循环结束后的最终输出。
type Result struct {
	Output string
	Steps  int
}

// Agent 一个工具调用循环。
//
// 可选 callback 字段（nil 安全）：用于 SSE 推帧 / 日志 / metric / tracing；
// 不参与状态机决策，调用方只读取事件不能改变 loop 行为。
type Agent struct {
	Provider llm.Provider
	Tools    *Registry
	MaxSteps int

	// OnToolCallStart 工具被调用前；用于前端 SSE tool_call_start 帧
	OnToolCallStart func(ctx context.Context, call llm.ToolCall)
	// OnToolCallDone 工具执行完后；result 即将作 tool message 回灌 LLM；
	// 用于前端 SSE tool_call_done 帧。result 中含执行错误字符串（"error: ..."）。
	OnToolCallDone func(ctx context.Context, call llm.ToolCall, result string)
	// OnText 每收到一段 assistant 文本增量（流式）；用于前端实时显示模型思考
	// 注意：A2 内部用 lastText 累积；这里同样在 chunk 到达时调用一次
	OnText func(ctx context.Context, delta string)
}

// Run 跑 ReAct 循环：LLM → 看是否 tool_calls → 跑工具 → 结果回灌作 role=tool 消息 → 再调 LLM。
// 最多 MaxSteps 轮（默认 6）；用尽返 ErrMaxStepsReached 但 Result.Output 仍含最后 text。
//
// 兼容两种 Request 模式：
//   - Messages 非空：直接作初始对话（agent 高层接口）
//   - System / User：组装成 system+user 两条消息（兼容 v1/v2 stage 风格）
//
// 工具执行错误（Run 返 err）不让 loop 挂：错误文字作 tool result 回灌让 LLM 决定如何应对。
// 未知 tool 同样返错回灌（防 LLM 调到未注册工具时整轮失败）。
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

		// 没 tool_calls：本轮 assistant 给出最终答案，结束
		if len(calls) == 0 {
			return Result{Output: lastText.String(), Steps: step + 1}, nil
		}

		// 把本轮 assistant message（含 tool_calls）+ 每个 tool 的执行结果回灌
		msgs = append(msgs, llm.Message{
			Role:      "assistant",
			Content:   lastText.String(),
			ToolCalls: calls,
		})
		for _, tc := range calls {
			if a.OnToolCallStart != nil {
				a.OnToolCallStart(ctx, tc)
			}
			result := a.runTool(ctx, tc)
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

	// MaxSteps 用尽：返最后 text + 显式 error 让调用方降级
	return Result{Output: lastText.String(), Steps: maxSteps}, ErrMaxStepsReached
}

// runTool 单次工具调用；未知 tool / 执行 err 都返字符串供回灌（不抛 err 中断 loop）。
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
