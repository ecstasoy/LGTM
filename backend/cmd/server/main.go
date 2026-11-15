// Backend HTTP entrypoint: slog → .env → config → deps → Gin → routes.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/ecstasoy/LGTM/backend/internal/api"
	"github.com/ecstasoy/LGTM/backend/internal/config"
	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/i18n"
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
	// Global JSON structured logging
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	// Load .env relative to the working directory if present; production usually has none, so a missing file is ignored and other errors warn
	for _, p := range []string{".env", "backend/.env"} {
		if err := godotenv.Load(p); err == nil {
			slog.Info("loaded env file", "path", p)
			break
		} else if !os.IsNotExist(err) {
			slog.Warn("failed to load env file", "path", p, "err", err)
		}
	}

	cfg := config.MustLoad()

	// OpenTelemetry: a trace provider when OTLP_ENDPOINT is set, noop otherwise
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

	// Client IP resolution feeds the rate limiter, so getting it wrong is not cosmetic:
	// every request collapsing onto one proxy address turns a per-client limit into a
	// single bucket shared by everyone. TrustedPlatform is checked first by gin.
	if cfg.TrustedPlatform != "" {
		r.TrustedPlatform = cfg.TrustedPlatform
		slog.Info("client IP resolved from platform header", "header", cfg.TrustedPlatform)
	}

	// Trusted proxies let c.ClientIP() read X-Forwarded-For.
	// Left unset, proxy trust is disabled explicitly and ClientIP falls back to RemoteAddr.
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
		// Only IPs and CIDRs parse here. A hostname fails, leaves no trusted CIDR, and
		// silently drops ClientIP back to RemoteAddr; log it as an error naming the
		// consequence rather than a warning nobody reads.
		if err := r.SetTrustedProxies(proxies); err != nil {
			slog.Error("trusted proxies rejected; X-Forwarded-For will be ignored and ClientIP falls back to RemoteAddr",
				"trusted_proxies", cfg.TrustedProxies, "err", err)
		}
	}

	api.RegisterWithSecret(r, deps, cfg.GithubAppWebhookSec)

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{Addr: addr, Handler: r}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	slog.Info("server starting", "addr", addr)
	go func() { serveErr <- srv.ListenAndServe() }()
	select {
	case err := <-serveErr:
		slog.Error("server exited", "err", err)
		os.Exit(1)
	case <-ctx.Done():
	}
	// Restore default signal handling so a second signal kills the process outright.
	stop()

	slog.Info("stop signal received; draining in-flight work", "timeout", cfg.ShutdownTimeout.String())
	drainCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := api.Drain(drainCtx, srv, deps.Background); err != nil {
		slog.Error("drain timed out; remaining work was cut off", "err", err)
		return
	}
	slog.Info("server drained")
}

// buildDeps picks providers from config, logging what was asked for and what was used; a missing key warns instead of degrading silently.
// A store that fails to open only warns since handlers are nil-safe: caching and history switch off instead of the whole service going down.
func buildDeps(cfg config.Config) api.Deps {
	registry := buildRegistry(cfg)
	defProvider, _ := registry.Default()
	deps := api.Deps{
		Fetcher:  gh.NewRealFetcher(cfg.GithubToken),
		Provider: defProvider, // registry default; the agent and other paths still use it
		Models:   registry,    // L2: per-stage / per-request provider+model resolved through the registry
		// placeholder NewLayeredBuilder(), replaced via WithRetriever once the retriever below is built
		Builder: prctx.NewLayeredBuilder(),
		// per-stage model overrides (L1): empty falls through to the provider default
		StageModels: map[string]string{
			"summary":     cfg.SummaryModel,
			"risks":       cfg.RisksModel,
			"suggestions": cfg.SuggestionsModel,
		},
		// DefaultLocale: normalized once at startup so only "zh"/"en" ever reach the request-boundary resolver.
		DefaultLocale: i18n.Normalize(cfg.DefaultLocale, i18n.ZH),
		Background:    &api.Background{},
	}
	if cfg.SummaryModel != "" || cfg.RisksModel != "" || cfg.SuggestionsModel != "" {
		slog.Info("per-stage model routing",
			"summary", cfg.SummaryModel, "risks", cfg.RisksModel, "suggestions", cfg.SuggestionsModel)
	}
	// Store: PostgresStore when POSTGRES_URL is set, SQLite otherwise
	// a failed open only warns since handlers are nil-safe; caching and history switch off instead of the service going down
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
	// Cache: RedisCache when REDIS_URL is set (shared across instances), MemoryCache otherwise
	// a failed connect falls back to MemoryCache with an Error log; rate limiting keeps counting per instance
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
	// Embedder for RAG: mock without a key or with EMBEDDING_PROVIDER=mock; openai needs EMBEDDING_API_KEY
	deps.Embedder = pickEmbedder(cfg)
	// Retriever: sqlite-backed brute-force cosine; falls back to NoopRetriever on failure
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
			deps.Indexer = rt // SQLiteRetriever implements both Retriever and Indexer
			slog.Info("retriever ready", "type", "sqlite", "path", cfg.RAGDBPath)
		}
	} else {
		deps.Retriever = index.NoopRetriever{}
		deps.Indexer = index.NoopIndexer{}
		slog.Info("retriever ready", "type", "noop")
	}
	// rebuild Builder with the Retriever; prctx.LayeredBuilder.buildL4 recalls RAG references through it
	deps.Builder = prctx.NewLayeredBuilder(prctx.WithRetriever(deps.Retriever))

	// Memory: session memory on deps.Cache (Redis across instances / in-memory locally); defaults cap=10 turns / TTL=7d
	// on failure / nil the SessionStore is nil and the steer.go agent path skips load/save without touching the main flow
	deps.Memory = memory.NewCacheSessionStore(deps.Cache, 0, 0)
	slog.Info("memory ready", "max_turns", memory.DefaultMaxTurns, "ttl", memory.DefaultTTL)

	// OAuth: skipped without client_id / client_secret; the handlers return 503 themselves
	// SessionManager reuses deps.Cache too (sessions shared across instances on Redis)
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

// openSQLiteFallback opens SQLite: ensures the parent dir exists, opens it, warns and returns nil on failure
// shared by both store branches in buildDeps
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

// buildRegistry builds the L2 named model registry.
//   - LLM_MODELS empty / unparsable → a single default entry (from LLM_PROVIDER/OPENAI_*/LLM_MODEL), same as legacy.
//   - otherwise one entry per JSON item; each key comes from the env var named by api_key_env (a missing key degrades to mock without blocking startup).
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
	// LLM_PROVIDER is case-insensitive: "OpenAI" / "OPENAI" / "openai" all work
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
