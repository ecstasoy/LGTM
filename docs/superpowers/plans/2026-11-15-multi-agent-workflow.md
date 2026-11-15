# 多 Agent 协作开发工作流 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让任意 agent 担任协调者，在 Orca 里派最多 3 个任意 agent 的 worker 并行出 PR，人只在成批合并时介入一次。

**Architecture:** 规则只有一个来源：主 checkout 的本地 `AGENTS.md`，内部分三节（通用 / 作为 worker / 作为协调者）。Orca 每新建一个 worktree，就由 setup 脚本把它和几份本地文档软链接进去，并装好依赖。main 由 CI 必过检查加分支保护把关，Fly 部署改为等 CI 通过后才跑。最后用 3 个真实小任务试跑一遍，验证整套流程。

**Tech Stack:** GitHub Actions（`workflow_run`）、`gh api`、POSIX sh、Orca orchestration CLI（`/Applications/Orca.app/Contents/Resources/bin/orca`）。

**Spec:** `docs/superpowers/specs/2026-11-15-multi-agent-workflow-design.md`

## Global Constraints

- 并发上限 **3** 个实现者。
- worker 永不合并；只有协调者在人成批确认后合并，并且**串行**合并。
- 不允许叠加 PR：每个 PR 的 base 都是 `main`。
- 必过检查名（必须与 CI job 名逐字一致）：`Backend (go test + vet)`、`Frontend (test + tsc + build)`。
- 分支保护为"中等"强度：`strict: true`（分支必须与 main 同步），`enforce_admins: true`。Vercel 不改。
- 以下 6 个本地文件永不提交：`AGENTS.md`、`CLAUDE.md`、`docs/agent-application-completion-tracker.md`、`docs/agent-engineering-backlog.md`、`docs/self-hosting-readiness.md`、`docs/competitor-signals.md`。
- 任何文件、commit 和 PR 里都不写竞品名。
- commit message 一行、英文；不加 `Co-Authored-By`。PR 正文用英文 6 段模板（What/How/Testing/Impact/Checklist/Next），不引用内部计划。
- "bot 通过"按以下标准判定：
  - 只看最新 head commit 上的那次 bot 评审，其中每条建议都已修复或已回复不修的理由；
  - 为处理 bot 建议追加的 push 最多 2 轮；
  - CI 跑绿后 15 分钟内 bot 没有评审，就注明"bot 未评审"并照常交付。
- 交叉审查在以下情况触发，审查者必须是与实现者不同的 agent，每个 PR 最多审 2 轮：
  - 改动涉及 `backend/internal/api/{webhook,auth,perms,steer}.go`、`backend/internal/oauth/`、`backend/internal/store/*.sql`；
  - 改动涉及 `.github/workflows/`、`backend/Dockerfile`、`backend/fly.toml`；
  - 人明确要求。
- 改仓库设置（Task 2）前必须先问人。

## Review Focus

1. **只改 README 的 push 到 main**：CI 跑绿后，deploy 应被触发但**跳过**部署，不能每次都发版。由 Task 1 的路径过滤来保证。
2. **PR 上的 CI**（`event: pull_request`）跑完：绝不能触发部署。由 Task 1 的 `if` 条件来保证。
3. **新 worktree 里误跑了 `git add -A`**：6 个本地文件不能出现在暂存区。由 Task 4 的 exclude 来保证。
4. **setup 脚本重复运行**，或在已有真实文件的主 checkout 里运行：不能覆盖或破坏真实文件。由 Task 4 的幂等检查来保证。
5. **Codex worker 当实现者**：它看不到 Claude memory，但必须遵守同样的 PR 模板和"不合并"规则。由 Task 5 试跑中至少一个实现者用 Codex 来验证。

---

### Task 1: Fly 部署改为等 CI 通过

**Files:**
- Modify: `.github/workflows/deploy.yml`

**Interfaces:**
- Consumes: `.github/workflows/ci.yml` 的 workflow 名 `CI`（不改它）。
- Produces: 只有在 main 的 push 上 CI 成功后才部署；`workflow_dispatch` 仍可手动强制部署。

- [ ] **Step 1: 在 `ci-gated-deploy` 分支上改 `deploy.yml` 的触发方式**
  - 把 `on.push` 换成 `workflow_run: { workflows: [CI], types: [completed], branches: [main] }`，保留 `workflow_dispatch`。
  - job 级加 `if`：`github.event_name == 'workflow_dispatch' || (github.event.workflow_run.conclusion == 'success' && github.event.workflow_run.event == 'push')`。
  - checkout 用 `ref: ${{ github.event.workflow_run.head_sha || github.sha }}` 和 `fetch-depth: 2`。
  - 原来的 `paths` 过滤（`workflow_run` 不支持）改由 `.github/scripts/deploy-decision.sh` 决定，并配有测试 `deploy-decision_test.sh`。它用 `flyctl image show` 读出线上镜像标签里记录的已部署 commit：优先读我们自己打的 `LGTM_DEPLOY_SHA`，没有时退回 flyctl 自带的 `GH_SHA`。
    - 要部署的 commit 已经包含在线上版本里，就跳过，防止重跑旧 CI 时把线上回滚；
    - 从线上 commit 到这次 commit 之间，改动命中 `backend/`、`frontend/`、`docs/`、`.dockerignore` 或部署相关文件，就部署；
    - 读不到线上 commit，就部署；`workflow_dispatch` 一律部署。
    - deploy step 用 `--label LGTM_DEPLOY_SHA=<sha>` 给镜像打标签。
    - 不用 `HEAD^1` 的原因（最终审阅发现）：同一个 concurrency 组只保留一个排队中的 run，后来的只改 README 的 run 会顶掉排队中的后端部署；重跑旧 CI 会回滚线上；rebase 合并只看得到最后一个 commit。
  - `concurrency` 和 `permissions` 保持不变。把文件头注释更新为英文，写一行说明触发方式。

- [ ] **Step 2: 静态检查**

  运行：`actionlint .github/workflows/deploy.yml .github/workflows/ci.yml`
  期望：无输出，exit 0。

- [ ] **Step 3: commit、push、开 PR**

  ```bash
  git add .github/workflows/deploy.yml
  git commit -m "ci(deploy): deploy to Fly only after CI passes on main"
  ```
  然后 push，按 6 段模板开 PR。Testing 段写明：合并后去确认第一次 main 上的 Deploy run 的事件是 `workflow_run`。这个 PR 触发交叉审查，因为改了 `.github/workflows/`。

- [ ] **Step 4: 合并后验证**（合并前先问人）

  运行：`gh run list --workflow "Deploy backend to Fly.io" --limit 2 --json event,conclusion,headSha`
  期望：最新一条 `event` 为 `workflow_run`，`headSha` 等于合并 commit；它的开始时间晚于同一 sha 的 CI run 完成时间。

### Task 2: 仓库设置：分支保护和合并后删分支（先问人）

**Files:** 无（仓库设置）。

**Interfaces:**
- Consumes: Task 1 已合并（保护打开后，Task 1 自己的 PR 也要守规则；顺序上先合 Task 1 更省事）。
- Produces: main 必须通过两项 CI 且与 main 同步才能合并，管理员也不例外；合并后 head 分支自动删除，子 PR 自动改指 main。

- [ ] **Step 1: 把下面两条命令原样给人看，得到确认后再执行**

  ```bash
  gh api -X PUT repos/ecstasoy/LGTM/branches/main/protection --input - <<'EOF'
  {"required_status_checks":{"strict":true,"contexts":["Backend (go test + vet)","Frontend (test + tsc + build)"]},
   "enforce_admins":true,"required_pull_request_reviews":null,"restrictions":null}
  EOF
  gh api -X PATCH repos/ecstasoy/LGTM -F delete_branch_on_merge=true
  ```

- [ ] **Step 2: 验证**

  运行：`gh api repos/ecstasoy/LGTM/branches/main/protection --jq '{strict:.required_status_checks.strict,checks:.required_status_checks.contexts,admins:.enforce_admins.enabled}'; gh api repos/ecstasoy/LGTM --jq .delete_branch_on_merge`
  期望：`{"strict":true,"checks":["Backend (go test + vet)","Frontend (test + tsc + build)"],"admins":true}` 和 `true`。

### Task 3: 本地规则文件 `AGENTS.md` 和 `CLAUDE.md` 垫片

**Files:**（都在主 checkout 根目录，不提交）
- Modify: `AGENTS.md`
- Create: `CLAUDE.md`

**Interfaces:**
- Consumes: 现有 `AGENTS.md` 里的 tracker 条款（17 行），保留全部要求。
- Produces: 三个固定的二级标题：`## General rules`、`## When you are a dispatched Orca worker`、`## When you are the coordinator`。Task 5 的任务 spec 会按名字引用它们。

- [ ] **Step 1: 改写 `AGENTS.md`，正文用英文**
  - `## General rules`：
    - Global Constraints 里所有不分角色的条目（只在本地执行、英文、一行 commit、不加 trailer、6 段模板、不引用内部计划、不写竞品名、不做叠加 PR、`<<'EOF'` heredoc 里反引号不转义、不提交这 6 个本地文件）；
    - **单人会话**的 tracker 条款：原文保留；
    - 单人会话可以在人确认后自己合并，并在合并后写 `.claude/notes/PR-<n>.md`（中文、不提交）。
  - `## When you are a dispatched Orca worker`（判定方式：任务 prompt 里带有 Orca 的 Task/Dispatch ID）：
    - 不合并；不写 tracker、backlog 和 `.claude/notes`；
    - 在 `worker_done` 报告里给出：PR 链接、CI 状态、bot 结论、tracker 条目（日期 / 变更 / backlog ID / 验证证据 / 评分影响 / 安全门槛影响）、未解决的问题；
    - "bot 通过"的判定标准按 Global Constraints；
    - spec 有歧义时用 `ask`；
    - 只改 spec 允许改的范围。
  - `## When you are the coordinator`：
    - 任意 agent 都可以当协调者；实现者默认用人开工时指定的 agent，人没有指定时，用与协调者相同的 agent；
    - 并发上限 3；只并行预计改动文件不相交的任务，派发前列出每个任务会碰的文件；
    - 不叠加 PR；
    - 交叉审查的触发条件和"不同 agent"要求；
    - 成批问人时列出：标题、链接、一句话影响、CI 和 bot 结论、是否经过交叉审查、审查者是哪个 agent；
    - 串行合并；从 main 同步后无冲突且 CI 重新跑绿的，批准继续有效；出现冲突、CI 变红或要改代码的，`send` 回原 worker 修，退出本批；
    - 合并后写 tracker、backlog 和 `.claude/notes`；
    - 协调者不写产品代码；
    - 附一个任务 spec 模板，包含：目标、改动、允许编辑范围、完成标准（命令 + 期望输出）、是否需要交叉审查。

- [ ] **Step 2: 创建 `CLAUDE.md`，内容只有一行 `@AGENTS.md`**

- [ ] **Step 3: 验证**

  运行：`cd "$(git rev-parse --show-toplevel)" && grep -c '^## ' AGENTS.md && grep -nE 'merge|tracker|Co-Authored-By|stacked|15 min' AGENTS.md | head -20 && cat CLAUDE.md`
  期望：
  - `grep -c` 输出 `3`，三个 `## ` 标题正好是上面那三个；
  - 每个关键词都能找到对应行；
  - `CLAUDE.md` 的内容是 `@AGENTS.md`。

### Task 4: Orca setup 脚本与 git exclude

**Files:**（主 checkout，不提交）
- Create: `.claude/orca-setup.sh`（已被 `.gitignore` 的 `.claude/` 覆盖）
- Modify: `.git/info/exclude`

**Interfaces:**
- Consumes: Orca setup 时注入的环境变量：`ORCA_ROOT_PATH`（主 checkout）和 `ORCA_WORKTREE_PATH`（新 worktree）。Task 3 产出的 `AGENTS.md` 和 `CLAUDE.md`。
- Produces: 每个新 worktree 里有指向主 checkout 的 6 个软链接，`frontend/node_modules` 已装好，Go 模块已下载。

- [ ] **Step 1: 在 `.git/info/exclude` 里追加这 6 个路径**，每个都以 `/` 开头，比如 `/AGENTS.md`、`/docs/agent-engineering-backlog.md`。

- [ ] **Step 2: 写 `.claude/orca-setup.sh`**（POSIX sh，`set -eu`）
  - 两个变量任一缺失，或两者相等（说明在主 checkout 里），就打印提示后 exit 0。
  - 对 6 个文件逐个处理：
    - 源文件不存在：如果是 `AGENTS.md` 或 `CLAUDE.md`，打印 ERROR，并在链接和装依赖都做完后以非零状态退出，因为没有规则的 worker 不能悄悄启动；其余 4 个 docs 文件只跳过并打印；
    - 目标已经是指向同一源的软链接，跳过；
    - 目标是真实文件，打印警告，**不覆盖**；
    - 其他情况，执行 `ln -s`，必要时先 `mkdir -p docs`。
  - 然后运行 `(cd frontend && pnpm install --frozen-lockfile)` 和 `(cd backend && go mod download)`。任一失败只打印警告，不让脚本失败，这样 worker 仍然能启动。

- [ ] **Step 3: 静态检查**

  运行：`shellcheck .claude/orca-setup.sh`
  期望：无输出。

- [ ] **Step 4: 让人在 Orca 里配置 setup 钩子**

  Orca 的 CLI 没有设置钩子的命令，需要人自己操作：Orca 设置 → 本仓库 → Hooks → Setup script，填入一行：
  `sh "$ORCA_ROOT_PATH/.claude/orca-setup.sh"`
  同一页面把 agent 启动策略改成"等 setup 完成后再启动"（`setupAgentStartupPolicy: wait-for-setup`）。否则 agent 可能在 `AGENTS.md` 链接进来之前就已经启动，或者和 setup 里的 `pnpm install` 同时运行。
  然后运行 `orca repo show --repo id:<repo-id> --json`（`<repo-id>` 从 `orca repo list --json` 里查），确认 `hookSettings.scripts.setup` 已是这一行，且 `setupAgentStartupPolicy` 为 `wait-for-setup`。

- [ ] **Step 5: 用一个临时 worktree 做端到端验证**

  运行：`orca worktree create --repo id:<repo-id> --name setup-check --no-parent --setup run --json`。setup 跑完后，在新 worktree 里运行：
  `ls -l AGENTS.md CLAUDE.md docs/agent-*.md docs/self-hosting-readiness.md docs/competitor-signals.md && git add -A -n | grep -cE 'AGENTS|CLAUDE|agent-|self-hosting|competitor' ; test -d frontend/node_modules && echo deps-ok`
  期望：6 个都是指向主 checkout 的软链接；`grep -c` 输出 `0`；打印 `deps-ok`。
  再手动跑一次：`ORCA_ROOT_PATH=<主 checkout> ORCA_WORKTREE_PATH="$PWD" sh <主 checkout>/.claude/orca-setup.sh`，期望 6 个文件全部打印"跳过"。
  最后让 Orca 删除 `setup-check` 这个 worktree。

### Task 5: 试跑：协调者并行派 3 个真实小任务

**Files:**（由各 worker 修改，三个任务的文件互不相交）
- Worker A：`.github/workflows/ci.yml`。加 `permissions: contents: read`；加 `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: ${{ github.event_name == 'pull_request' }} }`；把头注释改成英文、去掉比赛措辞。**触发交叉审查。**
- Worker B：`frontend/package-lock.json`（删除）和 `frontend/package.json`（删掉 Next 16 已经跑不起来的 `lint` 脚本）。**用 Codex 当实现者**（对应 Review Focus 第 5 条）。
- Worker C：`backend/Dockerfile`。runner 镜像从 `alpine:3.20` 升级到当前受支持的 alpine 版本，具体版本由 worker 查证后写进 PR。**触发交叉审查。**

**Interfaces:**
- Consumes: Task 1 到 4 全部完成；`AGENTS.md` 里三节的标题（Task 3）。
- Produces: 3 个已合并的 PR、tracker 里新增的条目、3 份 `.claude/notes/PR-<n>.md`，以及一份试跑记录。

- [ ] **Step 1: 协调者为 A、B、C 各写一份 spec**，按 `AGENTS.md` 里的模板写，并明确列出允许编辑的文件。
- [ ] **Step 2: 派发**

  运行：`orca orchestration worker-start --spec "<spec>" --worktree new-child --name <slug> --agent <id>`。A 和 C 用 `claude`，B 用 `codex`。派发后协调者结束本轮，等 Orca 唤醒。

- [ ] **Step 3: 处理 `ask`、`escalation` 和 `worker_done`**
  - A 和 C 报告完成后，各派一个与其实现者不同 agent 的只读审查 worker（`--agent codex`）。审查者的发现用 `send` 转给原实现者。
- [ ] **Step 4: 成批问人**

  用一条消息列出 3 个 PR，字段按 `AGENTS.md` 协调者一节的要求。

- [ ] **Step 5: 人确认后串行合并，每合一个就检查剩余 PR 是否需要同步 main**

  验证：`gh pr list --state merged --limit 3 --json number,baseRefName --jq '.[].baseRefName'`
  期望：3 行都是 `main`。

- [ ] **Step 6: 收尾**
  - 写 tracker 和 backlog 条目、3 份 notes；
  - 用 `orca orchestration worker-list` 确认所有 worker 都已结算；
  - 在 PR 描述之外，把试跑中遇到的规则漏洞反馈给人，并相应修改 `AGENTS.md`。
