package api

import (
	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/api/middleware"
	"github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/index"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/memory"
	"github.com/ecstasoy/LGTM/backend/internal/oauth"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
	"github.com/ecstasoy/LGTM/backend/internal/session"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// Deps 是路由 + handler 需要的所有依赖。
// 在 main 构造一次，向下注入。
// Store 可为 nil（关闭缓存 + 历史功能），handler 必须 nil-safe。
// Cache 可为 nil（限流中间件降级 pass-through）；生产环境应注入 MemoryCache 或 RedisCache
// Embedder 可为 nil（RAG 关闭）；B1 引入用于 B2/B3 RAG retriever
// Retriever 可为 nil（RAG 关闭）；缺失时 prctx 跳 L4
// Indexer 可为 nil（同 Retriever；通常与 Retriever 同实例，B4 引入分接口）
// OAuthClient + Sessions 可为 nil（OAuth 未配时 /api/auth/* 返 503）
// Memory 可为 nil（关闭 agent 追问会话记忆）；建议生产装上，提升多轮追问体验
type Deps struct {
	Fetcher     github.Fetcher
	Provider    llm.Provider
	Builder     prctx.Builder
	Store       store.Store
	Cache       store.Cache
	Embedder    index.Embedder
	Retriever   index.Retriever
	Indexer     index.Indexer
	OAuthClient *oauth.Client
	Sessions    *session.Manager
	Memory      memory.SessionStore
	// StageModels 按 stage name（summary/risks/suggestions）的模型覆盖；
	// 空 map / 空值走 provider 默认（L1 按阶段模型路由）。
	StageModels map[string]string
	// Models 具名模型注册表（L2）；非 nil 时按阶段 / 按请求经它解析 (provider, model)。
	// nil 时回退到 Provider（兼容旧调用与单测）。
	Models *llm.Registry
}

// RegisterWithSecret 同 Register 但接受 webhook secret 显式注入
// （webhook 验签必须的；webhook secret 是 fly secret 不该塞 Deps 暴露给所有 handler）
func RegisterWithSecret(r *gin.Engine, d Deps, webhookSecret string) {
	registerRoutes(r, d, webhookSecret)
}

// Register 兼容老 caller；webhook 验签会 fail-secure 拒绝（避免无 secret 时被滥用）
func Register(r *gin.Engine, d Deps) {
	registerRoutes(r, d, "")
}

func registerRoutes(r *gin.Engine, d Deps, webhookSecret string) {
	g := r.Group("/api")
	g.Use(middleware.AuthCtx(d.Sessions))

	expensive := middleware.RateLimit(d.Cache, middleware.ExpensiveDefault)
	read := middleware.RateLimit(d.Cache, middleware.ReadDefault)

	// /health 留为 liveness 别名（向后兼容 v1/v2 配置）
	g.GET("/health", Health)
	g.GET("/health/live", Health)
	g.GET("/health/ready", Readiness(d))

	g.POST("/review", expensive, PostReview(d))
	g.GET("/models", read, Models(d)) // L3：可选模型白名单
	g.GET("/reviews", read, ListReviews(d))
	g.GET("/reviews/:id", read, GetReview(d))
	g.DELETE("/reviews/:id", read, DeleteReview(d))
	g.POST("/review/:id/steer", expensive, PostSteer(d))

	// OAuth：登录 / 回调 / 登出 + 当前用户信息
	// 不走 expensive 限流（用户行为低频）；走 read 限流防滥用
	g.GET("/auth/github/login", read, AuthLogin(d.OAuthClient))
	g.GET("/auth/github/callback", read, AuthCallback(d.OAuthClient, d.Sessions))
	g.POST("/auth/logout", read, AuthLogout(d.Sessions))
	g.GET("/me", read, GetMe())
	g.GET("/perms", read, GetPerms(d.OAuthClient))

	// Adopt：把 review 里第 idx 条 suggestion 转成 GitHub PR review comment 发出去
	// 需要登录 + 对 repo 有 comment 权限；详见 perms 端点
	g.POST("/review/:id/comment/:idx", expensive, PostAdoptComment(d))

	// 同上但更进一步：comment + GraphQL apply → 一键 commit 到 PR 分支
	// 需要 write 权限；fork PR 不允许编辑时 apply 会失败，comment 仍上 PR
	g.POST("/review/:id/commit/:idx", expensive, PostAdoptCommit(d))

	// 撤回 comment：cid = GitHub PR review comment databaseId（不是 review idx）
	g.DELETE("/review/:id/comment/:cid", read, DeleteAdoptComment(d))

	// Webhook：GitHub 推 pull_request.opened → bot 自动评 + 写回 PR + push 通知
	// 不走 read 限流；GitHub 重试机制本身有节流；HMAC 校验防伪造
	g.POST("/webhook/github", WebhookGitHub(d, webhookSecret))

	// In-app 通知：webhook 完成后填用户 cache 列表；前端轮询拉
	g.GET("/notifications", read, GetNotificationsHandler(d.Cache))
}
