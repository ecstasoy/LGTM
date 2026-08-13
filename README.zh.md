# LGTM

[English](./README.md) · **中文**

LGTM 是一个面向 GitHub Pull Request 的评审助手。它会拉取 PR 元信息、diff、CI 状态和仓库约定文档，然后分三步生成变更摘要、风险列表和行内修改建议。登录后可以保存历史评审；安装 GitHub App 后，还能把建议发回 PR，或由 webhook 在新 PR 和 push 更新时自动评审。

## 在线试用

线上地址：<https://lgtm-alpha.vercel.app>

基本流程：

1. 用 GitHub 登录。
2. 粘贴公开 PR 链接，例如 `https://github.com/ecstasoy/LGTM/pull/93`。
3. 等待 SSE 流式返回。摘要会边生成边显示，风险和建议阶段完成后一次性更新。

评审页有三个视图：

- **评审报告**：摘要、风险列表、行内建议。
- **改动 Diff**：文件树、patch hunk、建议定位。
- **代理会话**：展示解析 PR、拉取 diff、构建上下文、调用 LLM、写缓存的步骤；也可以继续追问这个 PR。

只看评审结果不需要给仓库安装 App。要把建议评论到 PR、把 GitHub suggestion 应用成 commit，或者让 bot 在 `pull_request` webhook 后自动评审，需要把 LGTM GitHub App 安装到目标仓库。

## 当前能力

- 拉取 GitHub PR 的标题、正文、作者、分支、标签、统计信息、文件 diff、CI checks。
- 读取仓库根目录的 `README.md`、`CONTRIBUTING.md`、`CLAUDE.md` 或 `AGENTS.md`，作为项目约定上下文。
- 并发运行三个评审阶段：`summary`、`risks`、`suggestions`。
- 通过 SSE 推送 `pr`、`files`、`budget_report`、`summary_delta`、`risks_done`、`suggestions_done`、`review_id` 等事件。
- 按 `owner/repo/pr/head_sha` 缓存结果。head SHA 不变时可直接回放历史结果。
- 支持 SQLite 和 Postgres 作为持久化存储，支持 MemoryCache 和 RedisCache 做 session、限流和通知缓存。
- 支持 GitHub OAuth 登录，session 放在 HttpOnly cookie 中。
- 支持 GitHub App webhook：`pull_request.opened`、`synchronize`、`reopened` 自动评审；PR 评论 `/lgtm review` 可手动重跑。
- 支持把单条建议发成 PR review comment，建议带 `suggestion` 代码块时可进一步调用 GitHub GraphQL 应用成 commit。
- 支持追问 Agent。沙盒工具（`read_file`、`list_dir`、`grep_patches`）只能访问本 PR 改动文件；接入 RAG 后额外提供 `search_repo` 工具按 query 在全仓索引内语义检索，不会任意读本地文件系统。

几个限制也需要直接说明：

- GitHub `ListFiles` 会翻页拉取，最多 30 页 × 100 个文件，即封顶 3000 个文件——这也是 GitHub 单个 PR 文件列表本身的上限，不是 LGTM 的限制；超预算裁剪是另一回事，由 `prctx` 处理并通过 `BudgetReport.Dropped` 上报。
- L2 上下文当前主要是 patch hunk，`FileContext.FullText` 字段已预留，但真实文件全文还没有接入。
- `LLM_PROVIDER=mock` 只能验证启动、拉取和 summary 流式输出；`risks` 和 `suggestions` 需要 JSON 输出，mock 默认回复不是 JSON，所以完整体验要接真实模型。
- `backend/internal/review/orchestrator.go` 是早期占位。当前真实调度逻辑在 `backend/internal/api/review.go` 的 `mergeStages` 和相关函数里。

## 本地开发

依赖：

- Go 1.25+
- Node 20+
- pnpm 10+

安装并启动：

```bash
make install
make dev
```

默认端口：

- 后端：`http://localhost:8080`
- 前端：`http://localhost:3000`
- 健康检查：`http://localhost:8080/api/health`

不配置环境变量时，后端会用 `mock` LLM provider 启动，也不会强制登录；可以直接调用后端接口：

```bash
curl -N -X POST http://localhost:8080/api/review \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://github.com/ecstasoy/LGTM/pull/93"}'
```

前端落地页目前默认有登录门槛。如果本地没有配置 GitHub OAuth，可以直接打开流式页测试 UI：

```text
http://localhost:3000/review/streaming?url=https%3A%2F%2Fgithub.com%2Fecstasoy%2FLGTM%2Fpull%2F93
```

### 接真实模型

本地开发时可以新建 `backend/.env`，后端启动会自动读取 `.env` 或 `backend/.env`。

```env
LLM_PROVIDER=openai
OPENAI_BASE_URL=https://api.deepseek.com
OPENAI_API_KEY=sk-xxx
LLM_MODEL=deepseek-chat

GITHUB_TOKEN=ghp_xxx
SQLITE_PATH=./data/reviews.db
RAG_DB_PATH=./data/rag.db
```

`openai` provider 调的是 OpenAI-compatible `/v1/chat/completions`。DeepSeek、OpenAI、Kimi、通义这类兼容接口都可以通过 `OPENAI_BASE_URL` 和 `LLM_MODEL` 切换。`GITHUB_TOKEN` 可选，但不填会走 GitHub 匿名限流，公开仓库也容易超过 60 req/h 的速率上限。

### 接 OAuth 和 GitHub App

如果要在本地走完整前端登录、评论、提交、webhook 流程，需要 GitHub App 的 OAuth 配置：

```env
GITHUB_OAUTH_CLIENT_ID=Iv1.xxxx
GITHUB_OAUTH_CLIENT_SECRET=xxxx
GITHUB_OAUTH_REDIRECT_URI=http://localhost:3000/api/auth/github/callback

GITHUB_APP_ID=123456
GITHUB_APP_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----\n...\n-----END RSA PRIVATE KEY-----"
GITHUB_APP_WEBHOOK_SECRET=replace-with-random-secret
```

当前代码把 `GITHUB_APP_PRIVATE_KEY` 当 PEM 内容解析，不会自动读取文件路径。Webhook 本地调试可用 `ngrok http 8080`，GitHub App 的 Webhook URL 填 `<ngrok-url>/api/webhook/github`。

App 权限建议以 [`docs/github-app-manifest.yml`](./docs/github-app-manifest.yml) 为准：`contents: read`、`metadata: read`、`pull_requests: write`、`checks: read`，`issues: read` 可按需要保留。真正把 suggestion 应用成 commit 时，GitHub 还会按当前登录用户对 PR head 分支的权限和 fork 可编辑状态做最终裁决。

### 开启 RAG

Embedding 和聊天模型分开配置。默认 `EMBEDDING_PROVIDER=mock`，可以跑通流程，但向量没有语义质量。真实召回建议用 OpenAI 兼容 embedding 服务：

```env
EMBEDDING_PROVIDER=openai
EMBEDDING_BASE_URL=https://api.openai.com
EMBEDDING_API_KEY=sk-xxx
EMBEDDING_MODEL=text-embedding-3-small
RAG_DB_PATH=./data/rag.db
```

运行时会把本次 PR 的 patch 按 hunk 切成 chunk 写入 `rag.db`。这能积累同一仓库过去评审过的 PR 上下文，但不是完整仓库索引。

如果要先索引整个本地仓库，可以手动跑：

```bash
cd backend
go run ./cmd/indexrepo --scope ecstasoy/LGTM --dir .. --db ./data/rag.db --env .env
```

容器部署时还有一条路径：`backend/entrypoint.sh` 会在 `RAG_SCOPE` 非空且 `/app/src` 存在时，后台执行 `/app/indexrepo` 做全仓预索引。Fly 配置里已经给本仓设置了 `RAG_SCOPE`。

## API 概览

后端路由统一挂在 `/api` 下，前端 Next.js 通过 `next.config.ts` 把 `/api/*` rewrite 到后端。

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/health` | liveness |
| `GET` | `/api/health/ready` | store readiness |
| `POST` | `/api/review` | 提交 PR URL，返回 SSE |
| `GET` | `/api/reviews` | 评审历史列表 |
| `GET` | `/api/reviews/:id` | 评审详情 |
| `DELETE` | `/api/reviews/:id` | 删除自己的评审记录 |
| `POST` | `/api/review/:id/steer` | 按用户引导重跑风险/建议，或启动 Agent 追问 |
| `POST` | `/api/review/:id/comment/:idx` | 把第 `idx` 条建议发到 GitHub PR |
| `POST` | `/api/review/:id/commit/:idx` | 发评论后调用 GitHub GraphQL 应用 suggestion |
| `DELETE` | `/api/review/:id/comment/:cid` | 删除已发出的 PR review comment |
| `GET` | `/api/auth/github/login` | GitHub OAuth 登录 |
| `GET` | `/api/auth/github/callback` | OAuth callback |
| `POST` | `/api/auth/logout` | 登出 |
| `GET` | `/api/me` | 当前登录用户 |
| `GET` | `/api/perms?owner=&repo=` | 当前用户对仓库的评论/提交权限 |
| `POST` | `/api/webhook/github` | GitHub App webhook |
| `GET` | `/api/notifications` | webhook 完成后的站内通知 |

更细的事件协议可看 [`frontend/lib/sse.ts`](./frontend/lib/sse.ts) 和 [`backend/internal/api/review.go`](./backend/internal/api/review.go)。

## 代码入口

后端：

- [`backend/cmd/server/main.go`](./backend/cmd/server/main.go)：加载配置、选择 LLM provider、选择 store/cache、接 RAG、注册路由。
- [`backend/internal/api/review.go`](./backend/internal/api/review.go)：手动评审主流程，包含 SSE、缓存、RAG 写入、三阶段并发调度。
- [`backend/internal/review/*.go`](./backend/internal/review/)：summary、risks、suggestions 三个 stage。
- [`backend/internal/prctx/layered.go`](./backend/internal/prctx/layered.go)：L1-L4 上下文构建和预算裁剪。
- [`backend/internal/index/`](./backend/internal/index/)：embedding、SQLite RAG、离线索引接口。
- [`backend/internal/agent/`](./backend/internal/agent/)：ReAct 风格工具调用循环和内置沙盒工具。
- [`backend/internal/oauth/`](./backend/internal/oauth/)：GitHub OAuth、App JWT、installation token、PR comment、GraphQL apply suggestion。

前端：

- [`frontend/app/(main)/page.tsx`](./frontend/app/(main)/page.tsx)：首页、登录门槛和 PR URL 入口。
- [`frontend/app/review/[id]/page.tsx`](./frontend/app/review/[id]/page.tsx)：流式评审和缓存详情共用的评审页。
- [`frontend/components/review/`](./frontend/components/review/)：报告、diff、会话、行内建议、追问抽屉。
- [`frontend/lib/sse.ts`](./frontend/lib/sse.ts)：POST + SSE 的客户端解析。

## 模型选择

LLM 抽象在 `backend/internal/llm.Provider`，当前只有一个核心方法：`Stream(ctx, Request)`。业务层只依赖这个接口，不直接依赖 DeepSeek 或 OpenAI SDK。

当前实现有两个 provider：

- `mock`：默认值，不发网络请求，按词流式返回固定 markdown。适合验证服务能启动、SSE 能通、前端能渲染。
- `openai`：调用 OpenAI-compatible `/v1/chat/completions`。`OPENAI_BASE_URL`、`OPENAI_API_KEY`、`LLM_MODEL` 决定具体模型和供应商。

生产默认配置倾向 DeepSeek `deepseek-chat`，原因比较实际：OpenAI 协议兼容，国内网络可达性好，成本低，上下文窗口也够当前分层裁剪使用。代码没有把 DeepSeek 写死，换模型只改环境变量。

三个 stage 对模型能力的要求不同：

- `summary` 是流式 markdown 生成，优先看稳定性和速度。
- `risks` 和 `suggestions` 要求 JSON 输出。代码用 `response_format: json_object` 约束格式，然后在后端解析失败时发 `error` SSE event。
- 代码层面 `SummaryStage`、`RisksStage`、`SuggestionsStage` 都有 `Model` 字段。`POST /api/review` 接受请求体里的 `stage_models` map，按 stage 覆盖模型，解析优先级是 `stage_models[stage] > model > 部署方默认值`（见 `backend/internal/api/review.go:146`）；网页端已经有对应的按阶段选模型开关。

Embedding 单独走 `index.Embedder`，不复用聊天模型。DeepSeek 目前没有 embedding API，所以真实 RAG 默认建议 `text-embedding-3-small` 或其他 OpenAI-compatible embedding 服务。没有 key 时降级 mock embedder，服务不断，但召回质量不可用于判断评审效果。

## 上下文获取

这个项目的核心判断是：评审质量主要取决于上下文，而不是单纯把 diff 丢给模型。

当前上下文分四层：

| 层 | 来源 | 当前实现 |
|---|---|---|
| L1 | PR meta | 标题、正文、作者、标签、分支、文件统计、CI checks、每个文件的增删行 |
| L2 | PR diff | 每个改动文件的 patch hunk；当前不拉完整文件全文 |
| L3 | 项目约定 | PR head 上的 `README.md`、`CONTRIBUTING.md`、`CLAUDE.md` 或 `AGENTS.md` |
| L4 | RAG 引用 | SQLite 中同一 `owner/repo` scope 下的代码 chunk，来自离线索引或过去评审写入的 PR hunk |

预算逻辑在 [`backend/internal/prctx/layered.go`](./backend/internal/prctx/layered.go)：

- 默认 token limit 是 48000，按字符数粗估 token。
- L1 永远保留；如果 L1 自己超过上限，直接返回错误。
- L3 默认拿 10% 预算，并且单个约定文件拉取时先限制在 16KB。
- L4 默认拿 20% 预算，仅在 retriever 不是 `NoopRetriever` 时启用。
- L2 使用剩余预算，并保留 1000 token floor，避免被 L3 或 L4 挤空。
- 超预算文件会进入 `BudgetReport.Dropped`，前端会收到 `budget_report`。

L4 不是盲目把检索结果塞进 prompt：

- scope 是 `owner/repo`，避免跨仓库串数据。
- 当前 PR 已经在 L2 的文件会从 L4 里跳过，减少重复。
- 默认召回 top 4，低于 cosine `0.35` 的结果会过滤掉。
- `summary` 默认用 PR meta 做 query；`risks` 会用偏 bug、安全、并发、资源泄漏的 query；`suggestions` 会用偏重构、性能、可读性的 query。

Agent 追问也是同一套思路：先把 L1/L3/L4 注入 prompt，再让工具补充。内置工具分两层沙盒：

- PR 沙盒：`read_file`、`list_dir`、`grep_patches` 只能读缓存的 PR 文件列表，逃出会被拒绝。
- RAG 检索：`search_repo` 在 `owner/repo` scope 内调 `index.Retriever`，按 query 召回全仓 chunk，方便 agent 在「相关代码」段不够时换更精准的 query 再查一次。Retriever 缺失或 NoopRetriever 时该工具不注册，agent 仍可用前三件套。

构造点在 `backend/internal/api/steer.go` 的 `agent.RegisterDefaultsWithRAG`。

## 部署

推荐部署形态是 Fly.io 后端 + Vercel 前端：

- 后端 Docker 镜像包含 `server` 和 `indexrepo` 两个二进制。
- Fly volume 挂 `/data`，用于 SQLite 评审历史和 RAG DB。
- 前端用 Next.js standalone 构建，Vercel 上通过 `BACKEND_URL` rewrite `/api/*` 到 Fly 后端。
- SSE 不走 Vercel server function，而是浏览器经 rewrite 直连后端，避免边缘函数超时。

最小部署命令可参考 [`docs/DEPLOY.md`](./docs/DEPLOY.md)。注意该文档有些说明沿用了早期阶段，遇到冲突时以 `backend/cmd/server/main.go`、`backend/fly.toml` 和本 README 为准。

## 未来扩展方向

- **更可靠的跨文件上下文**：当前 RAG 是文本 chunk + cosine。下一步更适合接 tree-sitter、LSP、调用图和类型信息，把“语义相近”升级成“真实引用关系”。
- **异步索引和队列**：现在手动评审会同步写 PR hunk，容器启动可后台预索引。多租户后应把索引放到 worker，配 Redis Streams、Postgres job table 或队列服务，避免影响评审请求延迟。
- **向量存储升级**：SQLite brute-force 对 demo 和小仓库够用；chunk 到万级以上可以换 sqlite-vss、pgvector 或 Qdrant。接口已经收敛在 `index.Retriever` 和 `index.Indexer`。
- **按阶段的进阶路由**：按 stage 选择模型的开关已经上线（`stage_models`，见「模型选择」一节）；还没做的是按阶段自动调温度，以及给风险判断阶段接 reasoner 或二次验证模型，这些都需要评测集先证明收益。
- **评测 harness**：准备一批带 ground truth 的 PR，记录误报、漏报、建议可应用率、耗时和成本。没有评测就很难判断模型切换是否真的变好。
- **Agent 工具扩展**：当前是 PR 沙盒三件套 + RAG `search_repo`。未来可以接符号定义、测试结果、CI 日志、远端文件读取（带白名单和 rate limit），但每个工具都要有权限边界和调用预算。
- **GitHub App 产品化**：webhook 当前直接起 goroutine，失败只记日志。生产版需要队列、重试、幂等 key、sticky comment 更新、更多 slash command 和更清晰的安装状态。
- **多实例运行**：PostgresStore、RedisCache 已经实现。进一步需要补迁移策略、备份、指标、配额和按用户/组织的可见性模型。

## 第三方依赖

后端：

- `gin-gonic/gin`：HTTP 路由和中间件。
- `google/go-github/v66`：GitHub REST API。
- `mattn/go-sqlite3`：SQLite store 和 RAG DB。
- `jackc/pgx/v5`：Postgres store。
- `redis/go-redis/v9`：Redis cache。
- `golang-jwt/jwt/v5`：GitHub App JWT。
- `caarlos0/env/v11`、`joho/godotenv`：配置加载。
- `getsentry/sentry-go`、OpenTelemetry：可观测性入口。

前端：

- `next` 16 + `react` 19。
- `tailwindcss` v4。
- `react-markdown` + `remark-gfm`。
- `react-diff-viewer-continued`。
- `highlight.js`。
- `lucide-react`。
- `class-variance-authority`、`clsx`、`tailwind-merge`。
- `vitest`（dev 依赖）：`lib/` 下纯函数的单元测试。

## 原创说明

本项目的 Go 后端、前端组件、prompt 模板、SSE 协议、L1-L4 上下文预算、RAG 检索接线、GitHub App/OAuth 接线和 Agent 工具实现均为项目内实现。

架构和产品形态参考过以下方向：

- qodo-ai/pr-agent：多阶段评审拆分。
- CodeRabbit：风险分级和行内 review comment 形态。
- Greptile：跨文件上下文检索。
- Anthropic Claude Code Review：多轮验证和工具化 reviewer 的方向。

## License

[MIT](./LICENSE)

开发者：[@ecstasoy](https://github.com/ecstasoy) 和 [@Claude](https://github.com/claude)。
