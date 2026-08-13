package config

import (
	"log/slog"
	"os"

	"github.com/caarlos0/env/v11"
)

// Config 读取并加载所有环境变量中的配置。
// 默认值保证 `go run ./cmd/server` 无需任何配置即可启动
// LLM 可降级到 mock
type Config struct {
	Port int `env:"PORT" envDefault:"8080"`

	GithubToken string `env:"GITHUB_TOKEN"`

	LLMProvider   string `env:"LLM_PROVIDER" envDefault:"mock"`
	OpenAIAPIKey  string `env:"OPENAI_API_KEY"`
	OpenAIBaseURL string `env:"OPENAI_BASE_URL" envDefault:"https://api.deepseek.com"`
	LLMModel      string `env:"LLM_MODEL" envDefault:"deepseek-chat"`

	// 按阶段模型路由（L1）：空串则该阶段回退到 LLMModel / provider 默认。
	// 例：RISKS_MODEL=deepseek-reasoner 让风险阶段用推理模型，summary 仍走快模型。
	// L2 注册表存在时，这里的值优先当注册表 key 解析；未命中再当原始模型名（见 llm.Registry.Resolve）。
	SummaryModel     string `env:"SUMMARY_MODEL"`
	RisksModel       string `env:"RISKS_MODEL"`
	SuggestionsModel string `env:"SUGGESTIONS_MODEL"`

	// LLMModels（L2）：LLM_MODELS 环境变量的原始 JSON，声明具名模型注册表；空则走单 provider（legacy）。
	LLMModels string `env:"LLM_MODELS"`

	SQLitePath string `env:"SQLITE_PATH" envDefault:"./data/reviews.db"`

	// v3 真部署：PostgresURL 非空时 store 切到 PostgresStore；否则继续走 SQLite。
	// 业务代码（handler / router）零变化，差异在 main 接线时按 cfg.PostgresURL != "" 二选一。
	// 形如：postgres://user:pass@host:5432/dbname?sslmode=disable
	PostgresURL string `env:"POSTGRES_URL"`

	// v3 真部署：RedisURL 非空时 cache 切到 RedisCache；否则走 MemoryCache。
	// 形如：redis://:password@host:6379/0
	// 用途：rate limit 计数跨实例共享 / SSE session 状态 / 未来 RAG 索引队列
	RedisURL string `env:"REDIS_URL"`

	// v3 OAuth / GitHub App：空时回退到旧 PAT 路径（GithubToken）
	GithubAppID         int64  `env:"GITHUB_APP_ID"`
	GithubAppPrivateKey string `env:"GITHUB_APP_PRIVATE_KEY"`    // PEM 内容或文件路径
	GithubAppWebhookSec string `env:"GITHUB_APP_WEBHOOK_SECRET"` // webhook 签名校验
	GithubOAuthClientID string `env:"GITHUB_OAUTH_CLIENT_ID"`
	GithubOAuthSecret   string `env:"GITHUB_OAUTH_CLIENT_SECRET"`
	// GithubOAuthRedirectURI 必须跟 GitHub App settings 里 Callback URL 完全一致
	// 默认 https://lgtm-alpha.vercel.app/api/auth/github/callback（Vercel rewrite → fly.dev）
	// 本地 dev 改 http://localhost:3000/api/auth/github/callback（Next dev proxy）
	GithubOAuthRedirectURI string `env:"GITHUB_OAUTH_REDIRECT_URI" envDefault:"https://lgtm-alpha.vercel.app/api/auth/github/callback"`

	// v3 可观测性：Sentry / OTLP；空 = 不启用
	SentryDSN    string `env:"SENTRY_DSN"`
	OTLPEndpoint string `env:"OTLP_ENDPOINT"`                        // OTel collector 地址，如 http://otel-collector:4318
	Environment  string `env:"ENVIRONMENT" envDefault:"development"` // dev / staging / prod，给 Sentry tag 用

	// TrustedProxies 信任的反代 IP/CIDR 列表（逗号分隔）；
	// 用于 c.ClientIP() 正确解析 X-Forwarded-For。
	// 空字符串（默认）表示不信任任何代理，直接取 RemoteAddr。
	TrustedProxies string `env:"TRUSTED_PROXIES" envDefault:""`

	// DefaultLocale is the single fallback for review output language: the last tier of the API's
	// body > Accept-Language > default chain, and the value used by the webhook path, which has no user request.
	DefaultLocale string `env:"DEFAULT_LOCALE" envDefault:"zh"`

	// v3 RAG embedding provider：
	//   - "mock" 默认 —— 确定性 hash 向量；CI / 无 key dev 跑通 pipeline
	//   - "openai" —— 真 OpenAI 兼容 endpoint
	// DeepSeek 没 embedding API；要用真值必须 OpenAI / 豆包 / Voyage
	EmbeddingProvider string `env:"EMBEDDING_PROVIDER" envDefault:"mock"`
	EmbeddingBaseURL  string `env:"EMBEDDING_BASE_URL" envDefault:"https://api.openai.com"`
	EmbeddingAPIKey   string `env:"EMBEDDING_API_KEY"`
	EmbeddingModel    string `env:"EMBEDDING_MODEL" envDefault:"text-embedding-3-small"`

	// RAG 索引 SQLite 文件路径；空时关闭 Retriever（prctx 跳 L4）
	// 与 SQLITE_PATH 分开：让 RAG 数据库即使切到 Postgres store 也仍能用 SQLite vector 存
	RAGDBPath string `env:"RAG_DB_PATH" envDefault:"./data/rag.db"`
}

// ModelConfig LLM_MODELS JSON 数组的一项（L2 注册表）。
// api_key_env 是存放该端点 key 的环境变量名 —— secret 不内联进 JSON，避免出现在配置 / 日志里。
type ModelConfig struct {
	Key       string `json:"key"`         // 注册表 key（请求 / 按阶段引用用）
	Label     string `json:"label"`       // 前端展示名
	BaseURL   string `json:"base_url"`    // OpenAI 兼容端点
	APIKeyEnv string `json:"api_key_env"` // 取 key 的环境变量名
	Model     string `json:"model"`       // 该端点下的模型名
}

// MustLoad 解析环境变量；失败则打印错误并退出进程。
func MustLoad() Config {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		slog.Error("config load", "err", err)
		os.Exit(1)
	}
	return cfg
}
