// 后端 HTTP 服务入口：装配 slog → 加载 .env → 读配置 → 构造依赖 → Gin → 注册路由。
package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/ecstasoy/LGTM/backend/internal/api"
	"github.com/ecstasoy/LGTM/backend/internal/config"
	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/index"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/memory"
	"github.com/ecstasoy/LGTM/backend/internal/oauth"
	"github.com/ecstasoy/LGTM/backend/internal/observability"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
	"github.com/ecstasoy/LGTM/backend/internal/session"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

func main() {
	// 全局 JSON 结构化日志
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	// 尝试加载 .env（相对于当前工作目录）；生产环境一般无 .env：文件不存在时忽略，其它错误给出告警
	for _, p := range []string{".env", "backend/.env"} {
		if err := godotenv.Load(p); err == nil {
			slog.Info("loaded env file", "path", p)
			break
		} else if !os.IsNotExist(err) {
			slog.Warn("failed to load env file", "path", p, "err", err)
		}
	}

	cfg := config.MustLoad()

	// OpenTelemetry：OTLP_ENDPOINT 非空时初始化 trace provider；空时 noop
	otelEndpoint := strings.TrimSpace(cfg.OTLPEndpoint)
	otelCleanup, err := observability.InitTracer(observability.OTelConfig{
		Endpoint:    otelEndpoint,
		Environment: cfg.Environment,
		Insecure:    !strings.HasPrefix(strings.ToLower(otelEndpoint), "https://"),
	})
	if err != nil {
		slog.Warn("otel init failed; continuing without tracing", "err", err)
	}
	defer otelCleanup()

	deps := buildDeps(cfg)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())

	if cleanup, err := observability.InitSentry(observability.SentryConfig{
		DSN:         cfg.SentryDSN,
		Environment: cfg.Environment,
	}); err != nil {
		slog.Warn("sentry init failed; continuing without sentry", "err", err)
	} else {
		defer cleanup()
		if cfg.SentryDSN != "" {
			r.Use(sentrygin.New(sentrygin.Options{Repanic: true}))
		}
	}

	// 配置受信代理：用于 c.ClientIP() 正确解析 X-Forwarded-For。
	// 未配置时显式禁用代理信任，退回 RemoteAddr 解析。
	if cfg.TrustedProxies == "" {
		if err := r.SetTrustedProxies(nil); err != nil {
			slog.Warn("set trusted proxies failed", "err", err)
		}
	} else {
		raw := strings.Split(cfg.TrustedProxies, ",")
		proxies := make([]string, 0, len(raw))
		for _, p := range raw {
			if p = strings.TrimSpace(p); p != "" {
				proxies = append(proxies, p)
			}
		}
		if err := r.SetTrustedProxies(proxies); err != nil {
			slog.Warn("set trusted proxies failed", "err", err)
		}
	}

	api.RegisterWithSecret(r, deps, cfg.GithubAppWebhookSec)

	addr := fmt.Sprintf(":%d", cfg.Port)
	slog.Info("server starting", "addr", addr)
	if err := r.Run(addr); err != nil {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}

// buildDeps 按配置选 Provider；明示用户意图与最终走向，缺 key 显式 warn 而非静默降级。
// Store 打开失败仅 warn，handler 已 nil-safe；缓存 + 历史功能降级停用而非整个服务挂。
func buildDeps(cfg config.Config) api.Deps {
	registry := buildRegistry(cfg)
	defProvider, _ := registry.Default()
	deps := api.Deps{
		Fetcher:  gh.NewRealFetcher(cfg.GithubToken),
		Provider: defProvider, // 注册表默认项；agent / 其它路径仍用它
		Models:   registry,    // L2：按阶段 / 按请求经注册表解析 provider+model
		// Builder 先占位 NewLayeredBuilder()，等下面构造完 retriever 后再用 WithRetriever 替换
		Builder: prctx.NewLayeredBuilder(),
		// 按阶段模型覆盖（L1）：空值走 provider 默认
		StageModels: map[string]string{
			"summary":     cfg.SummaryModel,
			"risks":       cfg.RisksModel,
			"suggestions": cfg.SuggestionsModel,
		},
	}
	if cfg.SummaryModel != "" || cfg.RisksModel != "" || cfg.SuggestionsModel != "" {
		slog.Info("per-stage model routing",
			"summary", cfg.SummaryModel, "risks", cfg.RisksModel, "suggestions", cfg.SuggestionsModel)
	}
	// Store 二选一：POSTGRES_URL 非空走 PostgresStore，否则走 SQLite
	// 失败时仅 warn，handler 已 nil-safe；缓存 + 历史功能降级停用而非整个服务挂
	if cfg.PostgresURL != "" {
		if s, err := store.NewPostgresStore(cfg.PostgresURL); err != nil {
			slog.Error("open postgres store failed; falling back to SQLite", "err", err)
			deps.Store = openSQLiteFallback(cfg.SQLitePath)
		} else {
			slog.Info("store ready", "type", "postgres")
			deps.Store = s
		}
	} else {
		deps.Store = openSQLiteFallback(cfg.SQLitePath)
	}
	// Cache 二选一：REDIS_URL 非空走 RedisCache（v3 真部署跨实例共享），否则走 MemoryCache
	// 失败时降级 MemoryCache + Error log，rate limit 仍单机计数不会挂
	if cfg.RedisURL != "" {
		if c, err := store.NewRedisCache(cfg.RedisURL); err != nil {
			slog.Error("open redis cache failed; falling back to MemoryCache", "err", err)
			deps.Cache = store.NewMemoryCache(0)
			slog.Info("cache ready", "type", "memory")
		} else {
			deps.Cache = c
			slog.Info("cache ready", "type", "redis")
		}
	} else {
		deps.Cache = store.NewMemoryCache(0)
		slog.Info("cache ready", "type", "memory")
	}
	// Embedder：v3 RAG 用。缺 key / EMBEDDING_PROVIDER=mock 走 mock；openai 需 EMBEDDING_API_KEY
	deps.Embedder = pickEmbedder(cfg)
	// Retriever：sqlite-backed brute-force cosine；失败降级 NoopRetriever
	if cfg.RAGDBPath != "" {
		if dir := filepath.Dir(cfg.RAGDBPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				slog.Warn("ensure rag dir failed", "dir", dir, "err", err)
			}
		}
		if rt, err := index.NewSQLiteRetriever(cfg.RAGDBPath, deps.Embedder); err != nil {
			slog.Error("open rag retriever failed; using NoopRetriever", "err", err)
			deps.Retriever = index.NoopRetriever{}
			deps.Indexer = index.NoopIndexer{}
		} else {
			deps.Retriever = rt
			deps.Indexer = rt // SQLiteRetriever 同时实现 Retriever + Indexer
			slog.Info("retriever ready", "type", "sqlite", "path", cfg.RAGDBPath)
		}
	} else {
		deps.Retriever = index.NoopRetriever{}
		deps.Indexer = index.NoopIndexer{}
		slog.Info("retriever ready", "type", "noop")
	}
	// 重新构造 Builder 注入 Retriever；prctx.LayeredBuilder.buildL4 用它召回 RAG references
	deps.Builder = prctx.NewLayeredBuilder(prctx.WithRetriever(deps.Retriever))

	// Memory：会话记忆走 deps.Cache（Redis 跨实例 / Memory 本地）；默认 cap=10 turn / TTL=7d
	// 失败 / nil 时 SessionStore 是 nil，steer.go agent 路径会 skip load/save，不影响主流程
	deps.Memory = memory.NewCacheSessionStore(deps.Cache, 0, 0)
	slog.Info("memory ready", "max_turns", memory.DefaultMaxTurns, "ttl", memory.DefaultTTL)

	// OAuth：缺 client_id / client_secret 时跳过；handler 自己返 503
	// 同时 SessionManager 复用 deps.Cache（Redis 跨实例共享 session）
	if cfg.GithubOAuthClientID != "" && cfg.GithubOAuthSecret != "" {
		deps.OAuthClient = &oauth.Client{
			ClientID:      cfg.GithubOAuthClientID,
			ClientSecret:  cfg.GithubOAuthSecret,
			RedirectURI:   cfg.GithubOAuthRedirectURI,
			AppID:         cfg.GithubAppID,
			PrivateKeyPEM: []byte(cfg.GithubAppPrivateKey),
		}
		deps.Sessions = session.New(deps.Cache, 0)
		slog.Info("oauth ready", "client_id_prefix", cfg.GithubOAuthClientID[:min(8, len(cfg.GithubOAuthClientID))], "redirect", cfg.GithubOAuthRedirectURI)
	} else {
		slog.Warn("oauth disabled: GITHUB_OAUTH_CLIENT_ID / _CLIENT_SECRET not set; /api/auth/* will return 503")
	}
	return deps
}

func pickEmbedder(cfg config.Config) index.Embedder {
	switch strings.ToLower(strings.TrimSpace(cfg.EmbeddingProvider)) {
	case "openai":
		if cfg.EmbeddingAPIKey == "" {
			slog.Warn("EMBEDDING_PROVIDER=openai 但 EMBEDDING_API_KEY 未设；降级到 mock")
			slog.Info("embedder ready", "type", "mock", "reason", "missing key")
			return index.NewMockEmbedder()
		}
		slog.Info("embedder ready", "type", "openai", "base", cfg.EmbeddingBaseURL, "model", cfg.EmbeddingModel)
		return index.NewOpenAIEmbedder(cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel)
	case "mock", "":
		slog.Info("embedder ready", "type", "mock")
		return index.NewMockEmbedder()
	default:
		slog.Warn("未知 EMBEDDING_PROVIDER 值，降级到 mock", "value", cfg.EmbeddingProvider)
		return index.NewMockEmbedder()
	}
}

// openSQLiteFallback 公用 SQLite 打开逻辑：确保父目录存在 + 打开 + warn 失败 → nil
// 抽出来方便 buildDeps 内两条分支复用
func openSQLiteFallback(path string) store.Store {
	if path != ":memory:" {
		if dir := filepath.Dir(path); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				slog.Warn("ensure sqlite dir failed", "dir", dir, "err", err)
			}
		}
	}
	s, err := store.NewSQLiteStore(path)
	if err != nil {
		slog.Warn("open sqlite store failed; cache + history disabled", "path", path, "err", err)
		return nil
	}
	slog.Info("store ready", "type", "sqlite", "path", path)
	return s
}

// buildRegistry 构造 L2 具名模型注册表。
//   - LLM_MODELS 为空 / 解析失败 → 单默认项（来自 LLM_PROVIDER/OPENAI_*/LLM_MODEL），等价 legacy 行为。
//   - 否则按 JSON 数组逐项构造；每项 key 从 api_key_env 指定的环境变量取（缺 key 降级 mock，不阻塞启动）。
func buildRegistry(cfg config.Config) *llm.Registry {
	single := func() *llm.Registry {
		prov := pickProvider(cfg)
		return llm.NewRegistry([]llm.ModelProfile{{
			Key: cfg.LLMModel, Label: cfg.LLMModel, Provider: prov, Model: cfg.LLMModel,
		}}, cfg.LLMModel)
	}

	raw := strings.TrimSpace(cfg.LLMModels)
	if raw == "" {
		return single()
	}
	var mcs []config.ModelConfig
	if err := json.Unmarshal([]byte(raw), &mcs); err != nil || len(mcs) == 0 {
		slog.Error("解析 LLM_MODELS 失败，降级到单 provider", "err", err)
		return single()
	}
	profiles := make([]llm.ModelProfile, 0, len(mcs))
	for _, mc := range mcs {
		key := os.Getenv(mc.APIKeyEnv)
		var prov llm.Provider
		if mc.APIKeyEnv == "" || key == "" {
			slog.Warn("model profile 缺 api key，降级 mock", "key", mc.Key, "api_key_env", mc.APIKeyEnv)
			prov = llm.NewMockProvider()
		} else {
			slog.Info("model profile ready", "key", mc.Key, "base", mc.BaseURL, "model", mc.Model)
			prov = llm.NewOpenAIProvider(mc.BaseURL, key, mc.Model)
		}
		profiles = append(profiles, llm.ModelProfile{Key: mc.Key, Label: mc.Label, Provider: prov, Model: mc.Model})
	}
	slog.Info("model registry ready", "count", len(profiles), "default", profiles[0].Key)
	return llm.NewRegistry(profiles, mcs[0].Key)
}

func pickProvider(cfg config.Config) llm.Provider {
	// LLM_PROVIDER 不区分大小写，容忍 "OpenAI" / "OPENAI" / "openai" 等写法
	switch strings.ToLower(strings.TrimSpace(cfg.LLMProvider)) {
	case "openai":
		if cfg.OpenAIAPIKey == "" {
			slog.Warn("LLM_PROVIDER=openai 但 OPENAI_API_KEY 未设，降级到 mock；请检查 .env 或 shell 环境变量")
			slog.Info("llm provider", "type", "mock", "reason", "missing key")
			return llm.NewMockProvider()
		}
		slog.Info("llm provider", "type", "openai", "base", cfg.OpenAIBaseURL, "model", cfg.LLMModel)
		return llm.NewOpenAIProvider(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey, cfg.LLMModel)
	case "mock", "":
		slog.Info("llm provider", "type", "mock")
		return llm.NewMockProvider()
	default:
		slog.Warn("未知 LLM_PROVIDER 值，降级到 mock", "value", cfg.LLMProvider)
		return llm.NewMockProvider()
	}
}
