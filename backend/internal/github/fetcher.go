// Package github 抓 PR：meta + 改动文件 + 仓库约定文件。
package github

import (
	"context"
	"time"
)

// PR 状态常量；State 字段值取自 GitHub API（open/closed），merged 在 Fetcher 层合并 merged 标志后归类。
const (
	StateOpen   = "open"
	StateClosed = "closed"
	StateMerged = "merged"
)

// CI 综合状态常量；汇总单个 head commit 的所有 check-run。
// 优先级：任一失败 → failing；否则任一未完成 → pending；全部成功 → passing；无 check → pending。
const (
	CIStatusPassing = "passing"
	CIStatusFailing = "failing"
	CIStatusPending = "pending"
)

// Check 单个 CI 检查项（GitHub Actions / 第三方 CI 暴露的 check-run）。
type Check struct {
	Name       string `json:"name"`           // 检查名（如 "build" / "test (race)" / "lint (golangci)"）
	Status     string `json:"status"`         // passing / failing / pending（与 CI 同枚举）
	DurationMS int    `json:"duration_ms"`    // CompletedAt - StartedAt 毫秒；未完成时 0
	Note       string `json:"note,omitempty"` // check-run output.summary，如覆盖率 "82.4% (-0.3%)"；前端 pending 状态优先显示
}

// Stats PR 体量统计（来自 GitHub API 的 pulls.Get 响应，无需额外请求）。
type Stats struct {
	Files     int `json:"files"` // changed_files
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
	Commits   int `json:"commits"`
	Comments  int `json:"comments"` // PR 评论数（不含 review comments）
}

// PullRequest 一次抓取的完整 PR 快照。
type PullRequest struct {
	Owner   string
	Repo    string
	Number  int
	HeadSHA string
	Title   string
	Body    string
	// Private is the base repository's visibility; callers must not expose a private PR fetched with a shared token.
	Private bool

	// PR meta：用于落地页"最近评审"卡 / 评审顶栏 / 历史表格 / 会话视图分支 chip
	Author     string    // GitHub login，可空（极少数情况）
	AuthorRole string    // GitHub author_association：OWNER / MEMBER / COLLABORATOR / CONTRIBUTOR / FIRST_TIMER / NONE 等；前端格式化
	State      string    // open / closed / merged
	Labels     []string  // 标签名列表，按 API 返回顺序
	BaseRef    string    // base 分支名（如 "main"）
	HeadRef    string    // head 分支名（如 "fix/shard-eviction-race"）
	CreatedAt  time.Time // PR 创建时间，UTC
	Stats      Stats

	// CI 状态：HeadSHA 上所有 check-run 的汇总；checks 失败 / pending / 抓取失败均不阻塞主流程。
	CI     string // passing / failing / pending
	Checks []Check

	Files       []File
	Conventions Conventions
}

// File 一个改动文件。
type File struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // added | modified | removed | renamed
	Patch     string `json:"patch"`  // diff hunk
	FullText  string `json:"-"`      // 仅 prctx.Builder 内部用，不上协议（体积大、前端不需要全文）
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// Conventions 仓库级约定文件，作为 L3 上下文。
type Conventions struct {
	Readme       string
	Contributing string
	AgentDocs    string // CLAUDE.md 或 AGENTS.md，取存在的那个
}

// Fetcher 按 URL 拉 PR 快照。PR #2 实现 google/go-github 版。
type Fetcher interface {
	Fetch(ctx context.Context, url string) (PullRequest, error)
}
