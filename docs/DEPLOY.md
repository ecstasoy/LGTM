# LGTM 部署指南（Fly.io 后端 + Vercel 前端）

> 最小可行部署：**全免费档**够 demo。升级到 Postgres / Redis / OAuth / Sentry 见下方「升级路径」。
> 部署时长：约 **15 分钟人工 + 10–15 分钟首次构建**。

---

## 前置

| 项 | 怎么拿 | 必填？ |
|---|---|---|
| Fly.io 账号 + flyctl | https://fly.io/docs/hands-on/install-flyctl | ✅ 必填 |
| Vercel 账号 + CLI | `npm i -g vercel` 后 `vercel login` | ✅ 必填 |
| DeepSeek API key | https://platform.deepseek.com → 充值 ≥ ¥10 | ✅ 必填 |
| OpenAI API key | https://platform.openai.com → 充值 ≥ $5 | ✅ RAG 阶段必填（embedding） |
| GitHub PAT | https://github.com/settings/tokens → `repo:public` 即够 | ✅ 必填 |

> 不用 OpenAI 也能跑评审（LLM 走 DeepSeek 兼容），但 v3 RAG 用 embedding API；DeepSeek 没 embedding，必须用 OpenAI 或换其他兼容（豆包 / Voyage）。

---

## 第 1 步：后端部署到 Fly.io（约 8 分钟）

```bash
# 1. 进入 backend
cd backend

# 2. 用现有 fly.toml 创建 app（不立刻 deploy）
flyctl launch --no-deploy --copy-config

# 提示选 region：选 nrt（东京），跟 fly.toml 一致
# 提示创建 Postgres / Redis：先选 No，本指南后面升级路径再加

# 3. 创建持久卷（SQLite 文件用）
flyctl volumes create data --size 1 --region nrt
# 输出 "WARNING: single-volume" 是正常的；要高可用买第二个

# 4. 设 secrets
flyctl secrets set \
  OPENAI_API_KEY=sk-xxx-your-deepseek-key \
  GITHUB_TOKEN=ghp_your-pat
# 注意：OPENAI_API_KEY 这里填 DeepSeek key，因为 OPENAI_BASE_URL 已经在 fly.toml 设成 DeepSeek

# 5. 部署
flyctl deploy

# 等 ~10 分钟（首次镜像 build + push）

# 6. 验证
curl https://lgtm-backend.fly.dev/api/health
# → {"status":"ok"}

curl https://lgtm-backend.fly.dev/api/health/ready
# → {"status":"ready","checks":{"store":"ok"}}
```

后端域名：`https://lgtm-backend.fly.dev`

### 可选：默认语言

`DEFAULT_LOCALE`（`zh` / `en`，默认 `zh`）是 locale 三级回退链的最后一级：请求体 `locale` 字段 →
`Accept-Language` → `DEFAULT_LOCALE`。它同时是 webhook 自动评审唯一的语言来源——那条路径没有用户在场，
前两级都无从谈起。非敏感，跟 `PORT` 一样放 `fly.toml` 的 `[env]` 即可：

```toml
[env]
  DEFAULT_LOCALE = "zh"
```

前端不需要对应的环境变量：它按 cookie `lgtm-locale` 取，首次访问时由服务端读 `Accept-Language` 协商。

---

## 第 2 步：前端部署到 Vercel（约 5 分钟）

```bash
# 1. 进入 frontend
cd ../frontend

# 2. link 到 Vercel project（首次会问要不要建新项目）
vercel link
# 名字：lgtm-frontend
# Framework: Next.js（自动检测）

# 3. 设环境变量
vercel env add BACKEND_URL production
# 粘贴：https://lgtm-backend.fly.dev

# 4. 部署
vercel deploy --prod

# 等 ~3-5 分钟（pnpm install + next build + edge 部署）
```

前端域名：`https://lgtm-frontend.vercel.app`

---

## 第 3 步：端到端验证

1. 打开 `https://lgtm-frontend.vercel.app`
2. 粘贴一个公开 PR 链接，如 `https://github.com/golang/go/pull/12345`
3. 应该看到：
   - SSE 逐字流式输出总结
   - 风险 + 建议依次出现
   - 跳到 `/review/<ULID>` 落地（cached）
   - 历史页能看到这条记录

如果失败，看：

```bash
flyctl logs              # 后端实时日志
vercel logs <deployment> # 前端日志
```

---

## 常见问题

### Q: `flyctl deploy` 报 `failed to fetch image` / `pull access denied`

A: Fly 内部 build push 偶发；重试 `flyctl deploy --remote-only`。

### Q: `/api/health/ready` 返 503

A: 检查 store ping —— SQLite volume 没挂上。`flyctl volumes list` 看 data 卷是否 attached 到 machine。

### Q: 前端请求 `/api/review` 返 404

A: Vercel 环境变量 `BACKEND_URL` 没设；`vercel env ls` 确认。改完要 `vercel deploy --prod` 一次。

### Q: 偶发 `rate limit` 429

A: 正常防护。降速重试，或本机 `curl` 时设 `--header "X-Forwarded-For: <你的实 IP>"` 单独 IP 限流。

### Q: SSE 在 Vercel 上断开

A: Vercel 边缘函数对 SSE 超时。本项目走 Vercel rewrites 把 `/api/*` 透传到 Fly.io（不经 Vercel 边缘函数），SSE 走 Fly 直连 → 无超时。

---

## 升级路径

按需开启。每项独立，互不阻塞。

### 升级 1：Postgres 替 SQLite（推荐：多实例可用 + 数据持久 + 备份）

```bash
flyctl postgres create --name lgtm-pg --region nrt --vm-size shared-cpu-1x --volume-size 1
flyctl postgres attach lgtm-pg --app lgtm-backend
# 自动注入 DATABASE_URL secret；本项目读 POSTGRES_URL，二者一致即可
flyctl secrets set POSTGRES_URL="$(flyctl ssh console -C 'printenv DATABASE_URL')"
flyctl deploy
```

代码逻辑：`cfg.PostgresURL != "" → store.PostgresStore`（v3 后续 PR 实现）。

### 升级 2：Redis Cache（跨实例限流计数 + RAG 队列）

```bash
flyctl redis create --name lgtm-redis --region nrt
flyctl secrets set REDIS_URL="$(flyctl redis status lgtm-redis -j | jq -r '.private_url')"
flyctl deploy
```

代码：`cfg.RedisURL != "" → store.RedisCache`（v3 后续 PR 实现）。

### 升级 3：GitHub App + OAuth（webhook 自动评 + 用户登录）

照 [`docs/GITHUB_APP.md`](./GITHUB_APP.md) 申请 App，然后：

```bash
flyctl secrets set \
  GITHUB_APP_ID=123456 \
  GITHUB_OAUTH_CLIENT_ID=Iv1.xxx \
  GITHUB_OAUTH_CLIENT_SECRET=ghos_xxx \
  GITHUB_APP_WEBHOOK_SECRET=$(openssl rand -hex 32) \
  GITHUB_APP_PRIVATE_KEY="$(cat path/to/your-app.private-key.pem)"
flyctl deploy
```

去 GitHub App 设置页 → Webhook URL 填 `https://lgtm-backend.fly.dev/api/webhook/github`。

### 升级 4：Sentry（错误跟踪）

后端：

```bash
flyctl secrets set SENTRY_DSN=https://xxx@sentry.io/yyy
flyctl deploy
# DSN 非空时自动 init；代码已 ready（见 internal/observability）
```

前端：

```bash
vercel env add NEXT_PUBLIC_SENTRY_DSN production
# 粘贴前端项目 DSN（与后端不同项目）
vercel deploy --prod
```

### 升级 5：自有域名（替 *.fly.dev / *.vercel.app）

```bash
# 后端
flyctl certs create api.your-domain.com
# 拿到 CNAME 后到 DNS 配
# 前端
vercel domains add your-domain.com
# Vercel 自动签 cert
```

---

## 回滚

```bash
# 后端
flyctl releases             # 看历史 release ID
flyctl releases rollback v3 # 回到 v3

# 前端
vercel rollback             # 交互式选历史 deployment
```

---

## 监控

```bash
flyctl logs           # 实时日志
flyctl status         # machine 状态
flyctl metrics        # Prometheus 风格指标（自带）
flyctl dashboard      # 浏览器看图表
```

Vercel：`vercel logs` 或 dashboard。
