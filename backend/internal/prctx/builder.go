// Package prctx 构建分层 prompt 上下文（L1 meta / L2 文件 / L3 约定 / L4 检索）。
// 目录名避开标准库 context；包名 prctx。
package prctx

import (
	"context"

	"github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/index"
)

// Context 结构化 prompt 输入。
type Context struct {
	L1Meta        string            // PR 标题 + body + per-file 统计
	L2Files       []FileContext     // per-file patch + 可选全文
	L3Conventions string            // README / CONTRIBUTING / agent docs 拼接
	L4References  []index.Reference // v1 永远 nil；v2 RAG 填充
	BudgetReport  BudgetReport
}

// FileContext 单文件 patch + 可选全文。
type FileContext struct {
	Path     string
	Patch    string
	FullText string // 被预算丢弃时为空
}

// BuildOptions 调 BuildWith 时的可选配置。零值 = 走默认。
// 目前只有 RAGQuery：让 caller（如 steer agent）用用户问题替代默认的 L1Meta 作为 retriever query。
type BuildOptions struct {
	// RAGQuery 非空时覆盖 buildL4 的查询 string（默认是 L1Meta 前 1KB）
	// 追问场景 / steer 重跑场景应该传 user 输入，让召回更对题
	RAGQuery string
}

// Builder 把 PullRequest 转成 Context。
// 实现侧构造时接收 github.Fetcher 和 index.Retriever（v1 注入 NoopRetriever）。
// ctx 用于传递取消信号；RAG retriever / embedder 走外部 API 时必须能被请求 ctx 取消。
type Builder interface {
	Build(ctx context.Context, pr github.PullRequest) (Context, error)
	// BuildWith 同 Build，但接受 BuildOptions 控制 RAG 行为
	BuildWith(ctx context.Context, pr github.PullRequest, opts BuildOptions) (Context, error)
}
