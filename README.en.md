# LGTM

**English** · [简体中文](./README.md)

LGTM is a review assistant for GitHub pull requests. It pulls the PR's metadata, diff, CI status, and the repository's convention documents, then runs three passes to produce a change summary, a list of risks, and inline suggestions. Sign in and your past reviews are saved. Install the GitHub App and LGTM can post suggestions back to the PR, or review new PRs and pushes automatically over webhooks.

## Demo

[Demo video](https://screen.studio/share/qphlGDbQ)

For what an automatic review looks like once the GitHub App is installed, see [this PR](https://github.com/ecstasoy/LGTM/pull/104).

## Try it online

Hosted at <https://lgtm-alpha.vercel.app>

The basic flow:

1. Sign in with GitHub.
2. Paste a public PR link, for example `https://github.com/ecstasoy/LGTM/pull/93`.
3. Wait for the SSE stream. The summary renders as it is generated; risks and suggestions each land in a single update once their stage finishes.

The review page has three views:

- **Report**: the summary, the risks, and the inline suggestions.
- **Diff**: the file tree, the patch hunks, and where each suggestion anchors.
- **Session**: the steps taken to parse the PR, fetch the diff, build context, call the LLM, and write the cache. You can also keep asking follow-up questions about the PR from here.

Reading a review needs no App installation. Installing the LGTM GitHub App on the target repository is only required to post suggestions as PR comments, to turn a GitHub suggestion into a commit, or to have the bot review automatically on a `pull_request` webhook.

## Current capabilities

- Fetches a GitHub PR's title, body, author, branches, labels, statistics, file diffs, and CI checks.
- Reads `README.md`, `CONTRIBUTING.md`, `CLAUDE.md`, or `AGENTS.md` from the repository root as project-convention context.
- Runs three review stages concurrently: `summary`, `risks`, `suggestions`.
- Pushes `pr`, `files`, `budget_report`, `summary_delta`, `risks_done`, `suggestions_done`, and `review_id` events over SSE.
- Caches results by `owner/repo/pr/head_sha`. As long as the head SHA has not changed, a past result replays directly.
- Supports SQLite and Postgres for persistence, and MemoryCache or RedisCache for sessions, rate limiting, and notification caching.
- Supports GitHub OAuth sign-in, with the session held in an HttpOnly cookie.
- Supports GitHub App webhooks: `pull_request.opened`, `synchronize`, and `reopened` trigger an automatic review, and a `/lgtm review` comment on the PR reruns it by hand.
- Can post a single suggestion as a PR review comment, and when that suggestion carries a `suggestion` code block, call the GitHub GraphQL API to apply it as a commit.
- Supports a follow-up agent. Its sandboxed tools (`read_file`, `list_dir`, `grep_patches`) can only reach files this PR changed; with RAG wired up it also gets `search_repo`, which runs a semantic query against the repository-wide index. It never reads arbitrary paths on the local filesystem.

A few limits are worth stating outright:

- GitHub `ListFiles` paginates up to 30 pages of 100, i.e. up to 3000 files — that is GitHub's own ceiling on a single PR's file list, not an LGTM limitation. Trimming for the token budget is a separate concern, handled in `prctx` and reported through `BudgetReport.Dropped`.
- L2 context is patch hunks for now. The `FileContext.FullText` field is reserved, but full file text is not wired up yet.
- `LLM_PROVIDER=mock` is only good for verifying that the service starts, that fetching works, and that the summary streams. `risks` and `suggestions` require JSON output, and the mock's default reply is not JSON, so the full experience needs a real model.
- `backend/internal/review/orchestrator.go` is an early placeholder. The scheduling logic that actually runs lives in `mergeStages` and its neighbours in `backend/internal/api/review.go`.

## Local development

Requirements:

- Go 1.25+
- Node 20+
- pnpm 10+

Install and start:

```bash
make install
make dev
```

Default ports:

- Backend: `http://localhost:8080`
- Frontend: `http://localhost:3000`
- Health check: `http://localhost:8080/api/health`

With no environment variables set, the backend starts on the `mock` LLM provider and does not force sign-in, so you can call it directly:

```bash
curl -N -X POST http://localhost:8080/api/review \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://github.com/ecstasoy/LGTM/pull/93"}'
```

The frontend landing page sits behind a sign-in gate by default. If GitHub OAuth is not configured locally, open the streaming page directly to exercise the UI:

```text
http://localhost:3000/review/streaming?url=https%3A%2F%2Fgithub.com%2Fecstasoy%2FLGTM%2Fpull%2F93
```

### Wiring up a real model

For local development, create `backend/.env` — the backend reads `.env` or `backend/.env` on startup.

```env
LLM_PROVIDER=openai
OPENAI_BASE_URL=https://api.deepseek.com
OPENAI_API_KEY=sk-xxx
LLM_MODEL=deepseek-chat

GITHUB_TOKEN=ghp_xxx
SQLITE_PATH=./data/reviews.db
RAG_DB_PATH=./data/rag.db
```

The `openai` provider calls the OpenAI-compatible `/v1/chat/completions`. DeepSeek, OpenAI, Kimi, Qwen and other compatible services can be swapped in through `OPENAI_BASE_URL` and `LLM_MODEL`. `GITHUB_TOKEN` is optional, but without it requests fall back to GitHub's anonymous rate limit, where even public repositories run past the 60 requests per hour ceiling easily.

### Wiring up OAuth and the GitHub App

To exercise the full sign-in, comment, commit, and webhook flow locally, you need the GitHub App's OAuth configuration:

```env
GITHUB_OAUTH_CLIENT_ID=Iv1.xxxx
GITHUB_OAUTH_CLIENT_SECRET=xxxx
GITHUB_OAUTH_REDIRECT_URI=http://localhost:3000/api/auth/github/callback

GITHUB_APP_ID=123456
GITHUB_APP_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----\n...\n-----END RSA PRIVATE KEY-----"
GITHUB_APP_WEBHOOK_SECRET=replace-with-random-secret
```

The current code parses `GITHUB_APP_PRIVATE_KEY` as PEM content and will not read a file path. For local webhook debugging, `ngrok http 8080` works: set the GitHub App's Webhook URL to `<ngrok-url>/api/webhook/github`.

Treat [`docs/github-app-manifest.yml`](./docs/github-app-manifest.yml) as the source of truth for App permissions: `contents: read`, `metadata: read`, `pull_requests: write`, `checks: read`, with `issues: read` kept if you want it. When a suggestion is actually applied as a commit, GitHub still has the final say, based on the signed-in user's permissions on the PR head branch and whether the fork allows edits.

### Enabling RAG

Embedding and chat models are configured separately. The default is `EMBEDDING_PROVIDER=mock`, which is enough to exercise the pipeline but produces vectors with no semantic quality. For real recall, use an OpenAI-compatible embedding service:

```env
EMBEDDING_PROVIDER=openai
EMBEDDING_BASE_URL=https://api.openai.com
EMBEDDING_API_KEY=sk-xxx
EMBEDDING_MODEL=text-embedding-3-small
RAG_DB_PATH=./data/rag.db
```

At runtime the PR's patch is split into per-hunk chunks and written to `rag.db`. That accumulates context from PRs previously reviewed in the same repository, but it is not a full repository index.

To index an entire local repository up front, run:

```bash
cd backend
go run ./cmd/indexrepo --scope ecstasoy/LGTM --dir .. --db ./data/rag.db --env .env
```

Container deployments have one more path: `backend/entrypoint.sh` runs `/app/indexrepo` in the background to pre-index the whole repository whenever `RAG_SCOPE` is non-empty and `/app/src` exists. The Fly configuration already sets `RAG_SCOPE` for this repository.

## API overview

Backend routes all live under `/api`, and the Next.js frontend rewrites `/api/*` to the backend through `next.config.ts`.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/health` | liveness |
| `GET` | `/api/health/ready` | store readiness |
| `POST` | `/api/review` | Submit a PR URL, get SSE back |
| `GET` | `/api/reviews` | Review history list |
| `GET` | `/api/reviews/:id` | Review detail |
| `DELETE` | `/api/reviews/:id` | Delete one of your own review records |
| `POST` | `/api/review/:id/steer` | Rerun risks or suggestions under user steering, or start an agent follow-up |
| `POST` | `/api/review/:id/comment/:idx` | Post suggestion number `idx` to the GitHub PR |
| `POST` | `/api/review/:id/commit/:idx` | Post the comment, then apply the suggestion through GitHub GraphQL |
| `DELETE` | `/api/review/:id/comment/:cid` | Delete a PR review comment that was already posted |
| `GET` | `/api/auth/github/login` | GitHub OAuth sign-in |
| `GET` | `/api/auth/github/callback` | OAuth callback |
| `POST` | `/api/auth/logout` | Sign out |
| `GET` | `/api/me` | The currently signed-in user |
| `GET` | `/api/perms?owner=&repo=` | The current user's comment and commit permissions on a repository |
| `POST` | `/api/webhook/github` | GitHub App webhook |
| `GET` | `/api/notifications` | In-app notifications raised once a webhook run finishes |

For the finer-grained event protocol, read [`frontend/lib/sse.ts`](./frontend/lib/sse.ts) and [`backend/internal/api/review.go`](./backend/internal/api/review.go).

## Code entry points

Backend:

- [`backend/cmd/server/main.go`](./backend/cmd/server/main.go): loads configuration, picks the LLM provider, picks the store and cache, wires RAG, registers routes.
- [`backend/internal/api/review.go`](./backend/internal/api/review.go): the main manual-review flow, covering SSE, caching, RAG writes, and concurrent three-stage scheduling.
- [`backend/internal/review/*.go`](./backend/internal/review/): the `summary`, `risks`, and `suggestions` stages.
- [`backend/internal/prctx/layered.go`](./backend/internal/prctx/layered.go): L1-L4 context building and budget trimming.
- [`backend/internal/index/`](./backend/internal/index/): embeddings, the SQLite RAG store, and the offline indexing interfaces.
- [`backend/internal/agent/`](./backend/internal/agent/): the ReAct-style tool-calling loop and the built-in sandboxed tools.
- [`backend/internal/oauth/`](./backend/internal/oauth/): GitHub OAuth, App JWTs, installation tokens, PR comments, and applying a suggestion over GraphQL.

Frontend:

- [`frontend/app/(main)/page.tsx`](./frontend/app/(main)/page.tsx): the landing page, the sign-in gate, and the PR URL entry point.
- [`frontend/app/review/[id]/page.tsx`](./frontend/app/review/[id]/page.tsx): the review page shared by streaming reviews and cached detail views.
- [`frontend/components/review/`](./frontend/components/review/): the report, the diff, the session, inline suggestions, and the follow-up panel.
- [`frontend/lib/sse.ts`](./frontend/lib/sse.ts): the client-side parser for POST + SSE.

## Model choice

The LLM abstraction is `backend/internal/llm.Provider`, and it has one core method today: `Stream(ctx, Request)`. Application code depends on that interface alone, never on the DeepSeek or OpenAI SDK directly.

Two providers are implemented:

- `mock`: the default. Makes no network calls and streams back fixed markdown word by word. Good for checking that the service boots, that SSE flows, and that the frontend renders.
- `openai`: calls the OpenAI-compatible `/v1/chat/completions`. `OPENAI_BASE_URL`, `OPENAI_API_KEY`, and `LLM_MODEL` decide the actual model and vendor.

The production default leans toward DeepSeek `deepseek-chat`, for fairly practical reasons: it speaks the OpenAI protocol, it is reachable on Chinese networks, it is cheap, and its context window is large enough for the layered trimming in place today. DeepSeek is not hardcoded anywhere — switching models is an environment-variable change.

The three stages ask different things of a model:

- `summary` is streamed markdown generation, so stability and speed matter most.
- `risks` and `suggestions` require JSON output. The code constrains the format with `response_format: json_object`, then emits an `error` SSE event when the backend fails to parse the reply.
- In code, `SummaryStage`, `RisksStage`, and `SuggestionsStage` each carry a `Model` field. `POST /api/review` accepts a `stage_models` map in the request body, and resolution follows `stage_models[stage] > model > the deployment's default` (see `backend/internal/api/review.go:146`). The web UI already exposes a per-stage model picker for this.

Embeddings go through `index.Embedder` separately rather than reusing the chat model. DeepSeek has no embedding API today, so real RAG defaults to `text-embedding-3-small` or another OpenAI-compatible embedding service. Without a key it degrades to the mock embedder, which keeps the service running, but recall quality is then useless for judging review quality.

## Context gathering

The core bet of this project is that review quality depends mostly on context, not on dumping a diff into a model.

Context has four layers today:

| Layer | Source | Current implementation |
|---|---|---|
| L1 | PR meta | Title, body, author, labels, branches, file statistics, CI checks, added and deleted lines per file |
| L2 | PR diff | The patch hunks of every changed file; full file text is not fetched today |
| L3 | Project conventions | `README.md`, `CONTRIBUTING.md`, `CLAUDE.md`, or `AGENTS.md` at the PR head |
| L4 | RAG recall | Code chunks under the same `owner/repo` scope in SQLite, from offline indexing or PR hunks written by past reviews |

The budget logic lives in [`backend/internal/prctx/layered.go`](./backend/internal/prctx/layered.go):

- The default token limit is 48000, with tokens estimated roughly from character count.
- L1 is always kept; if L1 alone exceeds the limit, the build returns an error.
- L3 gets 10% of the budget by default, and each convention file is capped at 16KB when fetched.
- L4 gets 20% by default, and is enabled only when the retriever is not a `NoopRetriever`.
- L2 takes whatever is left, with a 1000-token floor so L3 or L4 cannot squeeze it out.
- Files that do not fit go into `BudgetReport.Dropped`, and the frontend receives a `budget_report`.

L4 is not a blind dump of search results into the prompt:

- The scope is `owner/repo`, so data never crosses repositories.
- Files this PR already put in L2 are skipped in L4, which cuts duplication.
- Recall defaults to the top 4, and anything below a cosine score of `0.35` is filtered out.
- `summary` queries with the PR meta by default; `risks` queries lean toward bugs, security, concurrency, and resource leaks; `suggestions` queries lean toward refactoring, performance, and readability.

Agent follow-ups follow the same thinking: inject L1/L3/L4 into the prompt first, then let tools fill in the rest. The built-in tools sit in two sandbox layers:

- The PR sandbox: `read_file`, `list_dir`, and `grep_patches` can only read the cached list of PR files, and anything that escapes it is refused.
- RAG search: `search_repo` calls `index.Retriever` within the `owner/repo` scope, recalling repository-wide chunks for a query. That lets the agent re-run a sharper query when the related-code section falls short. The tool is not registered when the retriever is missing or is a NoopRetriever, and the agent still has the other three.

The wiring point is `agent.RegisterDefaultsWithRAG` in `backend/internal/api/steer.go`.

## Deployment

The recommended shape is a Fly.io backend plus a Vercel frontend:

- The backend Docker image ships two binaries, `server` and `indexrepo`.
- A Fly volume is mounted at `/data` for the SQLite review history and the RAG DB.
- The frontend is a Next.js standalone build; on Vercel, `BACKEND_URL` rewrites `/api/*` to the Fly backend.
- SSE does not go through a Vercel server function. The browser connects straight to the backend through the rewrite, which avoids edge-function timeouts.

For the minimal set of deployment commands, see [`docs/DEPLOY.md`](./docs/DEPLOY.md). Note that parts of that document date from an earlier stage; where it conflicts, `backend/cmd/server/main.go`, `backend/fly.toml`, and this README win.

## Where this goes next

- **More reliable cross-file context**: RAG today is text chunks plus cosine similarity. The natural next step is tree-sitter, LSP, call graphs, and type information, upgrading "semantically similar" into "actually referenced".
- **Async indexing and queues**: manual reviews write PR hunks synchronously today, and container startup can pre-index in the background. Once this is multi-tenant, indexing belongs in a worker backed by Redis Streams, a Postgres job table, or a queue service, so it does not add latency to review requests.
- **A better vector store**: SQLite brute force is fine for the demo and small repositories; past roughly ten thousand chunks, sqlite-vss, pgvector, or Qdrant are worth the swap. The interfaces are already narrowed to `index.Retriever` and `index.Indexer`.
- **Smarter per-stage routing**: choosing a model per stage is already live (`stage_models`, see Model choice above). What's still missing is per-stage temperature tuning, and routing the risk stage to a reasoning model or a second verification pass — both need an evaluation set to prove the gain first.
- **An evaluation harness**: assemble a batch of PRs with ground truth and record false positives, false negatives, how often a suggestion can actually be applied, latency, and cost. Without evaluation it is hard to tell whether a model swap genuinely improved anything.
- **More agent tools**: today it is the three PR-sandbox tools plus RAG `search_repo`. Symbol definitions, test results, CI logs, and remote file reads (allowlisted and rate-limited) could follow, but every tool needs a permission boundary and a call budget.
- **Productizing the GitHub App**: the webhook spawns a goroutine directly today and only logs failures. A production version needs a queue, retries, idempotency keys, sticky comment updates, more slash commands, and a clearer installation state.
- **Running multiple instances**: PostgresStore and RedisCache are implemented. What is still missing is a migration strategy, backups, metrics, quotas, and a per-user or per-organization visibility model.

## Third-party dependencies

Backend:

- `gin-gonic/gin`: HTTP routing and middleware.
- `google/go-github/v66`: GitHub REST API.
- `mattn/go-sqlite3`: SQLite store and RAG DB.
- `jackc/pgx/v5`: Postgres store.
- `redis/go-redis/v9`: Redis cache.
- `golang-jwt/jwt/v5`: GitHub App JWTs.
- `caarlos0/env/v11`, `joho/godotenv`: configuration loading.
- `getsentry/sentry-go`, OpenTelemetry: observability entry points.

Frontend:

- `next` 16 + `react` 19.
- `tailwindcss` v4.
- `react-markdown` + `remark-gfm`.
- `react-diff-viewer-continued`.
- `highlight.js`.
- `lucide-react`.
- `class-variance-authority`, `clsx`, `tailwind-merge`.
- `vitest` (dev dependency): unit tests for the pure functions under `lib/`.

## Originality

The Go backend, the frontend components, the prompt templates, the SSE protocol, the L1-L4 context budgeting, the RAG retrieval wiring, the GitHub App and OAuth integration, and the agent tools were all implemented within this project.

Its architecture and product shape drew on the following:

- qodo-ai/pr-agent: splitting a review into multiple stages.
- CodeRabbit: risk grading and the shape of inline review comments.
- Greptile: cross-file context retrieval.
- Anthropic Claude Code Review: multi-turn verification and the tool-driven reviewer direction.

## License

[MIT](./LICENSE)

Developed by [@ecstasoy](https://github.com/ecstasoy) and [@Claude](https://github.com/claude).
