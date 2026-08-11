package api

import (
	"encoding/json"
	"net/http"
	"time"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
)

// cachedPayload 缓存的 review 内容。
// summary 存累加后的全文；risks / suggestions 存 stage 原 event data 字节，
// 让回放只需"原样写回"即可，避免与 review 包的具体类型耦合。
//
// PR meta（title/author/state/labels/refs/createdAt/stats/ci/checks）在 persist 时从
// fetcher 输出抄过来，让 /history 列表 + 详情可直接还原顶栏 / 卡片所需信息，免再 GitHub。
// 新字段全部用 omitempty：旧缓存（PR #23 时期）payload 缺这些字段时 JSON 输出保持干净，
// 前端按"字段缺失即未知"处理即可。
type cachedPayload struct {
	Title       string          `json:"title,omitempty"`
	// Source 标记本次评审是怎么来的："manual" 用户手动粘 URL；"webhook" GitHub 推 PR 自动评
	// 前端按这个字段在列表 / 详情顶栏渲染 ⚡ "自动" chip + 触发 Toast 通知
	Source      string          `json:"source,omitempty"`
	Author      string          `json:"author,omitempty"`
	AuthorRole  string          `json:"author_role,omitempty"`
	Lang        string          `json:"lang,omitempty"` // PR 主语言（按文件后缀多数派算），/history 语言筛选用
	State       string          `json:"state,omitempty"`
	Labels      []string        `json:"labels,omitempty"`
	BaseRef     string          `json:"base_ref,omitempty"`
	HeadRef     string          `json:"head_ref,omitempty"`
	PRCreatedAt time.Time       `json:"pr_created_at,omitzero"` // PR 在 GitHub 上的创建时间（区别于 Record.CreatedAt 是评审记录的创建时间）
	Stats       gh.Stats        `json:"stats,omitzero"`
	CI          string          `json:"ci,omitempty"`
	Checks      []gh.Check      `json:"checks,omitempty"`
	Files       []gh.File       `json:"files,omitempty"` // detail 端点回放 Diff 视图所需文件树 + patch
	Summary     string          `json:"summary"`
	Risks       json.RawMessage `json:"risks"`
	Suggestions json.RawMessage `json:"suggestions"`
	// BudgetReport 分层上下文 token 预算实际分配（L1-L4）；指针 + omitempty 让旧缓存不带该字段时 JSON 干净
	BudgetReport *budgetReportPayload `json:"budget_report,omitempty"`
}

// replayCached 把缓存内容按 SSE 协议依次写回；调用方负责事先已发首帧 pr meta。
// 在 c.Stream 外手写，因此最后手动 Flush。
// 不发 info / cached 标记事件：前端 info 语义是"短路隐藏 sections"，发了反而藏住缓存内容；
// 用户体感"秒回"即缓存信号，UI badge 留后续 PR。
func replayCached(w http.ResponseWriter, p cachedPayload) {
	if p.BudgetReport != nil {
		writeSSE(w, "budget_report", p.BudgetReport)
	}
	if p.Summary != "" {
		// 单帧 delta 即可拼出完整 summary（前端 reducer 是 += 累加）
		writeSSE(w, "summary_delta", map[string]string{"delta": p.Summary})
	}
	if len(p.Risks) > 0 {
		writeSSERaw(w, "risks_done", p.Risks)
	}
	if len(p.Suggestions) > 0 {
		writeSSERaw(w, "suggestions_done", p.Suggestions)
	}
	writeSSE(w, "done", map[string]any{})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
