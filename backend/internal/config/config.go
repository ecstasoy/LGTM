package config

import (
	"log/slog"
	"os"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is everything loaded from environment variables.
// Defaults let `go run ./cmd/server` start with no configuration at all
// LLM can degrade to mock
type Config struct {
	Port int `env:"PORT" envDefault:"8080"`

	GithubToken string `env:"GITHUB_TOKEN"`

	LLMProvider   string `env:"LLM_PROVIDER" envDefault:"mock"`
	OpenAIAPIKey  string `env:"OPENAI_API_KEY"`
	OpenAIBaseURL string `env:"OPENAI_BASE_URL" envDefault:"https://api.deepseek.com"`
	LLMModel      string `env:"LLM_MODEL" envDefault:"deepseek-chat"`

	// Per-stage model routing (L1): empty falls back to LLMModel / the provider default for that stage.
	// e.g. RISKS_MODEL=deepseek-reasoner runs risks on a reasoning model while summary stays on the fast one.
	// With an L2 registry these resolve as registry keys first, then as raw model names (see llm.Registry.Resolve).
	SummaryModel     string `env:"SUMMARY_MODEL"`
	RisksModel       string `env:"RISKS_MODEL"`
	SuggestionsModel string `env:"SUGGESTIONS_MODEL"`

	// LLMModels (L2): raw JSON of LLM_MODELS declaring the named model registry; empty means a single provider (legacy).
	LLMModels string `env:"LLM_MODELS"`

	SQLitePath string `env:"SQLITE_PATH" envDefault:"./data/reviews.db"`

	// PostgresURL non-empty switches the store to PostgresStore; otherwise SQLite.
	// Handlers and router are unchanged; main picks one on cfg.PostgresURL != "".
	// Form: postgres://user:pass@host:5432/dbname?sslmode=disable
	PostgresURL string `env:"POSTGRES_URL"`

	// RedisURL non-empty switches the cache to RedisCache; otherwise MemoryCache.
	// Form: redis://:password@host:6379/0
	// Used for: cross-instance rate-limit counters / SSE session state / a future RAG indexing queue
	RedisURL string `env:"REDIS_URL"`

	// OAuth / GitHub App: empty falls back to the PAT path (GithubToken)
	GithubAppID         int64  `env:"GITHUB_APP_ID"`
	GithubAppPrivateKey string `env:"GITHUB_APP_PRIVATE_KEY"`    // PEM contents or file path
	GithubAppWebhookSec string `env:"GITHUB_APP_WEBHOOK_SECRET"` // webhook signature verification
	GithubOAuthClientID string `env:"GITHUB_OAUTH_CLIENT_ID"`
	GithubOAuthSecret   string `env:"GITHUB_OAUTH_CLIENT_SECRET"`
	// GithubOAuthRedirectURI must match the Callback URL in the GitHub App settings exactly
	// Defaults to https://lgtm-alpha.vercel.app/api/auth/github/callback (Vercel rewrite → fly.dev)
	// Local dev: http://localhost:3000/api/auth/github/callback (Next dev proxy)
	GithubOAuthRedirectURI string `env:"GITHUB_OAUTH_REDIRECT_URI" envDefault:"https://lgtm-alpha.vercel.app/api/auth/github/callback"`

	// Observability: Sentry / OTLP; empty disables
	SentryDSN    string `env:"SENTRY_DSN"`
	OTLPEndpoint string `env:"OTLP_ENDPOINT"`                        // OTel collector address, e.g. http://otel-collector:4318
	Environment  string `env:"ENVIRONMENT" envDefault:"development"` // dev / staging / prod, used as the Sentry tag

	// TrustedProxies is the comma-separated list of reverse-proxy IPs/CIDRs to trust,
	// so c.ClientIP() reads X-Forwarded-For correctly.
	// Empty (default) trusts no proxy and takes RemoteAddr directly.
	TrustedProxies string `env:"TRUSTED_PROXIES" envDefault:""`

	// TrustedPlatform names a header the hosting platform sets to the real client IP
	// (Fly-Client-IP on Fly, CF-Connecting-IP behind Cloudflare). When set it wins over
	// TrustedProxies, which cannot express "the proxy is whatever 6PN address fronts us
	// today". Leave empty anywhere the header is not injected by the platform itself:
	// a client can forge it otherwise.
	TrustedPlatform string `env:"TRUSTED_PLATFORM" envDefault:""`

	// ShutdownTimeout bounds how long a stop signal waits for in-flight work before cutting it off.
	// Keep it below the platform's own kill timeout (Fly kill_timeout), or the platform kills the process first.
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"10s"`

	// DefaultLocale is the single fallback for review output language: the last tier of the API's
	// body > Accept-Language > default chain, and the value used by the webhook path, which has no user request.
	DefaultLocale string `env:"DEFAULT_LOCALE" envDefault:"zh"`

	// RAG embedding provider:
	//   - "mock" (default): deterministic hash vectors so CI / keyless dev runs the pipeline
	//   - "openai": a real OpenAI-compatible endpoint
	// DeepSeek has no embedding API; real vectors need OpenAI / Doubao / Voyage
	EmbeddingProvider string `env:"EMBEDDING_PROVIDER" envDefault:"mock"`
	EmbeddingBaseURL  string `env:"EMBEDDING_BASE_URL" envDefault:"https://api.openai.com"`
	EmbeddingAPIKey   string `env:"EMBEDDING_API_KEY"`
	EmbeddingModel    string `env:"EMBEDDING_MODEL" envDefault:"text-embedding-3-small"`

	// RAG index SQLite file path; empty disables the Retriever (prctx skips L4)
	// Separate from SQLITE_PATH so the RAG DB stays on SQLite vectors even when the store moves to Postgres
	RAGDBPath string `env:"RAG_DB_PATH" envDefault:"./data/rag.db"`
}

// ModelConfig is one item of the LLM_MODELS JSON array (L2 registry).
// api_key_env names the env var holding that endpoint's key, so secrets stay out of the JSON, config, and logs.
type ModelConfig struct {
	Key       string `json:"key"`         // registry key (referenced per request / per stage)
	Label     string `json:"label"`       // display name in the frontend
	BaseURL   string `json:"base_url"`    // OpenAI-compatible endpoint
	APIKeyEnv string `json:"api_key_env"` // env var to read the key from
	Model     string `json:"model"`       // model name at that endpoint
}

// MustLoad parses the environment and exits the process on failure.
func MustLoad() Config {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(1)
	}
	return cfg
}
