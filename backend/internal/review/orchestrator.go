// Package review 跑三个分析阶段（summary / risks / suggestions），按 SSE 推到 API 层。
package review

import (
	"context"
	"encoding/json"

	"github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/i18n"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
)

// Event 一条 SSE 消息
type Event struct {
	Type string          `json:"type"` // summary_delta | risks_done | suggestions_done | error | done
	Data json.RawMessage `json:"data"`
}

// Stage 一个独立分析步骤
// v1 三个 stage = 单次 LLM 调用；v2 可把任一 stage 换成 agent.Agent 驱动的实现
type Stage interface {
	Name() string
	Run(ctx context.Context, c prctx.Context, p llm.Provider) (<-chan Event, error)
}

// Orchestrator 串起 fetch → build → 跑 stage
type Orchestrator struct {
	Provider llm.Provider
	Builder  prctx.Builder
	// NOT WIRED: Run below is unimplemented and has no construction sites, so neither field is ever read.
	// The live path decides stage locale in api.newStage; look there, not here.
	Locale i18n.Locale // Would be passed to each default stage's Locale field; picks the system-prompt and template language.
	Stages []Stage     // 默认 [SummaryStage{Locale: o.Locale}, RisksStage{Locale: o.Locale}, SuggestionsStage{Locale: o.Locale}]
}

// Run 启动配置好的所有 stage，返回合并后的事件 channel。
// userID 预留给 v2 权限校验；v1 始终传 nil。
// PR #4 接 summary；PR #8 加 risks；PR #9 加 suggestions；PR #12 升级 SSE。
func (o *Orchestrator) Run(ctx context.Context, pr github.PullRequest, userID *string) <-chan Event {
	ch := make(chan Event, 1)

	data, err := json.Marshal(map[string]string{
		"message": "orchestrator run not implemented",
	})
	if err != nil {
		data = json.RawMessage(`{"message":"orchestrator run not implemented"}`)
	}

	ch <- Event{
		Type: "error",
		Data: data,
	}
	close(ch)
	return ch
}
