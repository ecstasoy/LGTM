package api

import (
	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/api/middleware"
	"github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/i18n"
	"github.com/ecstasoy/LGTM/backend/internal/index"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/memory"
	"github.com/ecstasoy/LGTM/backend/internal/oauth"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
	"github.com/ecstasoy/LGTM/backend/internal/session"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// Deps is everything the routes + handlers need.
// Built once in main and injected downward.
// Store may be nil (disables caching + history), so handlers must be nil-safe.
// Cache may be nil (the rate limit middleware degrades to pass-through); production should inject MemoryCache or RedisCache
// Embedder may be nil (RAG off); it feeds the RAG retriever
// Retriever may be nil (RAG off); when absent prctx skips L4
// Indexer may be nil (same as Retriever; usually the same instance, split into its own interface for write-side indexing)
// OAuthClient + Sessions may be nil (with OAuth unconfigured, /api/auth/* returns 503)
// Memory may be nil (disables agent follow-up session memory); worth having in production for better multi-turn follow-ups
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
	// StageModels overrides the model per stage name (summary/risks/suggestions);
	// an empty map / empty value falls through to the provider default (L1 per-stage model routing).
	StageModels map[string]string
	// Models is the named model registry (L2); when non-nil it resolves (provider, model) per stage / per request.
	// When nil it falls back to Provider (kept for older callers and unit tests).
	Models *llm.Registry
	// DefaultLocale is the last tier of the per-request locale chain (body > Accept-Language > this) and the
	// value used outright by paths with no request to negotiate from (webhook). Expected to already be
	// normalized (i18n.Normalize(cfg.DefaultLocale, i18n.ZH)); the zero value is treated as i18n.ZH by callers,
	// so leaving it unset in older callers and unit tests is safe.
	DefaultLocale i18n.Locale
}

// RegisterWithSecret is Register but takes the webhook secret explicitly
// (required for webhook signature verification; the webhook secret is a fly secret and does not belong in Deps where every handler can see it)
func RegisterWithSecret(r *gin.Engine, d Deps, webhookSecret string) {
	registerRoutes(r, d, webhookSecret)
}

// Register is kept for older callers; webhook verification fails secure and rejects (so it cannot be abused when no secret is set)
func Register(r *gin.Engine, d Deps) {
	registerRoutes(r, d, "")
}

func registerRoutes(r *gin.Engine, d Deps, webhookSecret string) {
	g := r.Group("/api")
	g.Use(middleware.AuthCtx(d.Sessions))

	expensive := middleware.RateLimit(d.Cache, middleware.ExpensiveDefault)
	read := middleware.RateLimit(d.Cache, middleware.ReadDefault)

	// /health stays as a liveness alias (backwards compatible with v1/v2 config)
	g.GET("/health", Health)
	g.GET("/health/live", Health)
	g.GET("/health/ready", Readiness(d))

	g.POST("/review", expensive, PostReview(d))
	g.GET("/models", read, Models(d)) // L3: allowlist of selectable models
	g.GET("/reviews", read, ListReviews(d))
	g.GET("/reviews/:id", read, GetReview(d))
	g.DELETE("/reviews/:id", read, DeleteReview(d))
	g.POST("/review/:id/steer", expensive, PostSteer(d))

	// OAuth: login / callback / logout + current user info
	// Not on the expensive limiter (these are low-frequency user actions); on the read limiter to prevent abuse
	g.GET("/auth/github/login", read, AuthLogin(d.OAuthClient))
	g.GET("/auth/github/callback", read, AuthCallback(d.OAuthClient, d.Sessions))
	g.POST("/auth/logout", read, AuthLogout(d.Sessions))
	g.GET("/me", read, GetMe())
	g.GET("/perms", read, GetPerms(d.OAuthClient))

	// Adopt: turn suggestion idx of a review into a GitHub PR review comment and post it
	// Requires login + comment permission on the repo; see the perms endpoint
	g.POST("/review/:id/comment/:idx", expensive, PostAdoptComment(d))

	// Same but one step further: comment + GraphQL apply → one-click commit onto the PR branch
	// Requires write permission; when a fork PR disallows edits the apply fails but the comment still lands on the PR
	g.POST("/review/:id/commit/:idx", expensive, PostAdoptCommit(d))

	// Withdraw a comment: cid = the GitHub PR review comment databaseId (not the review idx)
	g.DELETE("/review/:id/comment/:cid", read, DeleteAdoptComment(d))

	// Webhook: GitHub pushes pull_request.opened → the bot reviews, writes back to the PR and pushes a notification
	// Not on the read limiter; GitHub's own retry mechanism throttles it, and HMAC verification blocks forgeries
	g.POST("/webhook/github", WebhookGitHub(d, webhookSecret))

	// In-app notifications: filled into the user's cache list once a webhook completes; the frontend polls for them
	g.GET("/notifications", read, GetNotificationsHandler(d.Cache))
}
