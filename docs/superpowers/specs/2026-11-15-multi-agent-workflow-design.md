# 多 Agent 协作开发工作流设计

日期：2026-11-15
状态：已确认，待实现

## 意图

项目已导入 Orca。目标是用多个 agent 并行开发来提高开发效率。

现有流程里，commit、push、开 PR 都已由 agent 自主完成，唯一必须由人处理的环节是"合并前确认"。所以效率的来源是**并行产出**，约束是**不让送到人面前的确认变多变乱**。

## 目标

- 一个协调者同时推进多个互不重叠的小任务，每个任务产出一个独立 PR。
- 人只在一个地方介入：成批确认合并。
- 不管是哪个 agent 写的代码，都遵守和现在一样的项目规则（PR 模板、英文、只在本地执行、不写竞品名等）。
- 高风险 PR 可选加一道交叉审查（方案 B），审查者用与实现者不同的 agent。
- 不绑定某个 agent：协调者、实现者、审查者都可以是 Orca 支持的任意 agent（目前本机有 Claude 和 Codex）。

## 非目标

- 不做跨前后端大功能的自动拆解和依赖编排（方案 C）。大功能仍由人和协调者先讨论拆分。
- 不改 LGTM 产品本身的评审 agent 架构。
- 不引入云端或远程 worker，全部在本机 Orca 里跑。

## 反目标（技术上能跑，但这样就算失败）

- worker 自己合并 PR，或者绕过"合并前问人"。
- 几个 worker 改同一批文件，最后由人来解冲突。
- 出现叠加 PR（子 PR 的 base 是另一个未合并的分支），然后合并后提交变孤儿。
- 规则只对某一种 agent 生效，换一种 agent 就不知道了。
- 并行合入把一个坏 commit 送上 Fly。

## 约束（现状）

- **规则文件不在 worktree 里**：`AGENTS.md`、`docs/agent-application-completion-tracker.md`、`docs/agent-engineering-backlog.md`、`docs/self-hosting-readiness.md`、`docs/competitor-signals.md` 在主 checkout 里都是未跟踪文件。Orca 新建的 worktree 里没有它们。`competitor-signals.md` 含竞品名，永远不能提交。
- **不同 agent 读的东西不同**：项目规则目前大多在 Claude 的 auto-memory 里，只有 Claude 会话能看到。Codex 读 `AGENTS.md`；Claude Code 读 `CLAUDE.md`；其他 agent 各有自己的约定文件。
- **Tracker 是单个共享文件**：`AGENTS.md` 要求每个行为变更"在同一改动里"更新 tracker。但它是未跟踪的本地文件，多个 worker 并发写会互相覆盖。
- **main 没有机器把关**：无分支保护；`deploy.yml` 与 `ci.yml` 并行触发，不等 CI；`delete_branch_on_merge = false`。
- **Orca 仓库的 setup 脚本为空**（`hookSettings.scripts.setup = ""`）。新 worktree 不会自动装依赖，也拿不到上面那些本地文件。
- 本机已装 `claude` 与 `codex` CLI。

## 方案

| 方案 | 内容 | 取舍 |
|---|---|---|
| **A. 协调者 + 并行实现者** | 协调者拆任务，每个任务派给一个 Orca worker（独立 worktree、独立 PR）；协调者盯 CI 和 LGTM bot，成批交人确认合并 | 吞吐最高；要求任务拆到不重叠 |
| B. 实现 + 交叉审查 | 一个 agent 实现，另一个不同的 agent 审查，来回改到干净 | PR 质量更高、人审得更快；并行度低，token 约翻倍 |
| C. 完整 DAG 编排 | 规划 agent 拆大功能，按依赖派发，关键节点停下问人 | 适合大功能；天然产生叠加 PR，对当前待办太重 |

**已选：A 做主干，B 作为可选步骤**，只在高风险 PR 上启用。C 不做。

## 设计

### 1. 角色

- **人**：给出待办或目标；成批确认合并；回答协调者转来的、只有人能回答的问题。
- **协调者**：一个 agent 会话（Claude、Codex 或其他能运行 `orca` CLI 的 agent），在 Orca 里有自己的 worktree。负责拆任务、写任务 spec、`worker-start` 派发、处理 worker 的提问和升级、汇总结果、执行合并、合并后收尾。协调者本身不写产品代码。
- **实现者**：任意 agent 的 worker，用 `--agent <id> --worktree new-child --name <slug>` 派发。默认用哪个 agent，由人在开工时指定；没有指定时，用和协调者相同的 agent。一个 worker 对应一个任务、一个分支、一个 PR，从 worktree 创建一直做到 CI 和 bot 评审都通过。
- **审查者（可选）**：只读 worker，审一个 PR 的 diff，报告发现的问题，不推代码。必须用**与该 PR 实现者不同的 agent**，否则交叉审查就失去意义。
- 下文出现的 `ask`、`send`、`worker_done` 等都是 Orca 的编排机制，与 agent 种类无关。

### 2. 规则如何送到每个 worker

- 在主 checkout 维护本地规则文件 `AGENTS.md`（保持不提交），作为**唯一的规则来源**，把下面这些目前只存在于 Claude memory 里的协作规则补进去，让任何 agent 都能看到：
  - 本地执行，不用云端；
  - worker 永不合并；
  - 英文的注释、commit 和 PR 正文；
  - 一行 commit message；
  - 不加 `Co-Authored-By`；
  - 6 段 PR 模板（What/How/Testing/Impact/Checklist/Next）；
  - PR 不引用内部计划；
  - 不写竞品名；
  - 不做叠加 PR；
  - heredoc 里反引号不转义。
- 配置 Orca 仓库的 setup 脚本，在每个新 worktree 创建时：
  - 把 `AGENTS.md` 和上述 4 个 `docs/` 本地文件**符号链接**进 worktree；用符号链接，是为了所有 worktree 读到同一份最新内容；
  - 运行 `cd frontend && pnpm install --frozen-lockfile`；
  - 运行 `cd backend && go mod download`。
- 这些符号链接必须被 git 忽略，防止误提交。做法：把对应路径加进 `.git/info/exclude`，这个文件在所有 worktree 间共享，且不入库。
- 不读 `AGENTS.md` 的 agent，各放一个指向它的本地垫片文件（同样不提交，同样符号链接进每个 worktree）。目前只需要 Claude Code 的 `CLAUDE.md`，内容只有一行 `@AGENTS.md`。以后接入新 agent 时，按同样方式加它的约定文件（设计决定）。
- 协调者本身也遵守 `AGENTS.md`，包括其中"作为协调者时"一节（批量问人、串行合并、合并后写 tracker 和 notes）。所以换一种 agent 当协调者，行为不变。
- 协调者派发任务时，在每个 spec 开头声明"你是被派发的 Orca worker"。`AGENTS.md` 里专门有一节"作为 Orca worker 时"，写明：不合并；tracker 条目放在报告里；不写 `.claude/notes`。这一节覆盖 memory 里那些面向单人会话的规则。

### 3. Tracker 由协调者独占

- worker **不**直接写 tracker 和 backlog（符号链接后它们指向同一个文件，并发写会冲突）。
- worker 在 `worker_done` 报告里附上 tracker 需要的字段：变更、相关 backlog ID、验证证据、对评分的影响、对安全门槛的影响。
- PR 合并后，由协调者串行更新 tracker 和 backlog。
- **单人会话和协调者模式并存（已定）**：小修小补仍然由单个会话直接做 PR，成批的待办走协调者。所以 `AGENTS.md` 里的 tracker 条款按模式分开写：
  - 单人会话：保持现状，在同一个改动里更新 tracker；
  - 被派发的 worker：只在报告里提供 tracker 条目，不写文件；
  - 协调者：合并后把条目写入 tracker。
- 单人会话也可能是任意 agent，所以单人会话的规则（"合并前问人后自己合并""合并后写 notes"）同样写进 `AGENTS.md`，Claude memory 里的对应条目保留不动。`AGENTS.md` 因此分成三节：通用规则、作为 worker 时、作为协调者时。

### 4. 任务拆分规则

- 只并行**预计改动文件不相交**的任务。协调者在派发前列出每个任务预计会碰的文件或目录；有交集的排成串行。
- 有依赖的任务不叠加：等前一个合并后再派下一个，并以最新的 main 为 base。
- 并发上限默认 **3 个实现者**。

### 5. 单个任务的生命周期

1. 协调者写 spec，必须自包含：目标、要做的改动、允许编辑的范围、完成标准（具体到要跑哪些命令、看到什么结果）。
2. 用 `worker-start` 派发，`--agent claude --worktree new-child --name <slug>`。
3. 实现者：从最新 main 开分支 → 按步骤多次 commit → 本地跑 `go vet`、`go test -race` 和前端的 tsc/test/build，具体看改动范围 → push → 按模板开 PR。
4. 实现者等 CI 和 LGTM bot 的评审：
   - CI 失败就修；
   - bot 提出的问题要么修掉，要么在 PR 里回复为什么不修；
   - 两者都通过后，用 `worker_done` 报告：PR 链接、CI 状态、bot 的结论、tracker 条目、未解决的问题。

   "bot 通过"的定义（设计决定）：bot 只会给出 `COMMENTED`，不会给 pass/fail，而且每次 push 都会重新评审，所以按下面的规则判定：
   - 只看**最新 head commit** 上的那次 bot 评审。
   - 那次评审里的每条建议都必须有着落：要么已修复，要么在线程里回复了不修的理由。不能留无人处理的建议。
   - 为处理 bot 建议而追加的 push 最多 **2 轮**。2 轮后还有新建议的，在报告里列为"未解决"，交给人判断。
   - bot 有时不会评审（#130、#131、#139 都没有）。CI 跑绿后 **15 分钟**内没有 bot 评审，就照常报告完成，并注明"bot 未评审"。
5. 如果 spec 有歧义，或者需要人来拍板，实现者用 `ask` 问协调者。协调者能从 spec 或项目规则推断出答案的就直接回复；推断不出的，转给人。

### 6. 可选的交叉审查（方案 B）

- **触发条件**，满足任一即可：
  - 改动涉及 `backend/internal/api/` 下的 `webhook.go`、`auth.go`、`perms.go`、`steer.go`，或 `backend/internal/oauth/`，或 `backend/internal/store/` 下的 schema / index SQL；
  - 改动涉及 `.github/workflows`、`backend/Dockerfile`、`backend/fly.toml`；
  - 人明确要求。
- **流程**：
  - 实现者报告完成后，协调者派一个与实现者不同 agent 的 worker，只读地审这个 PR 的 diff；
  - 审查者的发现由协调者转给实现者（`send`），改完再走一次第 5 节第 4 步；
  - 每个 PR 最多审两轮。两轮后仍有分歧，就把双方的意见并排交给人。

### 7. 合并

- 协调者把已经全部通过的 PR 攒成一批，一次性问人：列出每个 PR 的标题、链接、一句话影响、CI 和 bot 的结论、是否经过交叉审查、审查者是哪个 agent。
- 人确认后，协调者**串行**合并：每合并一个，检查剩余 PR 是否落后于 main；落后的就更新分支、等 CI 重新跑绿，再合下一个。
- 批准在什么情况下仍然有效（设计决定）：
  - 更新分支只是从 main 合入、没有冲突、CI 重新跑绿，批准继续有效，可以直接合并。这一步引出的新一轮 bot 评审不必再处理。
  - 出现冲突、CI 变红、或需要改动 PR 自身的代码时，协调者用 `send` 把问题发回原实现者修复。该 PR 退出本批，修好后进入下一批，重新等人确认。
- 合并后协调者收尾：
  - 写 tracker 和 backlog（第 3 节）；
  - 按既有习惯写 `.claude/notes/PR-X.md`；
  - 让 Orca 回收该 worker 的 worktree。

### 8. 前置条件（作为本工作流的第一批任务）

在启用并行之前必须先完成：

1. 在 main 上开分支保护，强度为"中等"（已定）：
   - 把 `Backend (go test + vet)` 和 `Frontend (test + tsc + build)` 设为必过检查；
   - 要求分支与 main 同步后才能合并；
   - 规则对管理员也生效（协调者用的是人的管理员 token）。

   Vercel 维持现状：它直接从 main 部署，但 CI 和 Vercel 构建都会跑 `next build`，能编译失败的前端坏代码基本过不了。这是仓库设置，改之前要先经人确认。
2. `deploy.yml` 改成只在 CI 成功后运行。
3. 开启 `delete_branch_on_merge`（同样是仓库设置，需人确认）。

第 2 项本身可以作为这个工作流的第一个试点任务。

## 留给实现者决定

- `AGENTS.md` 里新增规则的具体措辞和排列。
- setup 脚本的具体写法，以及它放在 Orca 设置里还是放一个本地脚本由 Orca 调用。
- 协调者的任务 spec 模板长什么样。
- deploy 用 `workflow_run` 还是把两个 workflow 合并后用 `needs:`。
