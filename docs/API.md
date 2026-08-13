# API 接口文档

> 后端 base URL：`http://localhost:8080`（本地默认；通过 `PORT` env 改端口）
> 前端 dev server 把 `/api/*` 重写到后端，浏览器调 `http://localhost:3000/api/*` 等价。

---

## POST /api/review

提交 PR URL，**预检通过后切到 SSE 流**，按帧推送各 stage 事件。

### 请求

```http
POST /api/review
Content-Type: application/json

{
  "url": "https://github.com/owner/repo/pull/123",
  "locale": "en"
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `url` | string | 是 | GitHub PR 链接；允许带 `/files` 后缀和末尾斜杠；前后空白自动 trim |
| `locale` | string | 否 | 评审正文语言，`"zh"` / `"en"`。三级回退：请求体 → `Accept-Language` → `DEFAULT_LOCALE`；无法识别的值一律落回 `DEFAULT_LOCALE`，不报错。locale 是评审缓存唯一键的一部分，中英文各存一条 |

### 响应

**两段语义**：

- **预检失败**（URL 错 / body 错 / GitHub 拉取失败）→ 普通 JSON 响应（4xx / 5xx），无 SSE
- **预检通过**（GitHub 已返 PR 数据）→ `200 OK` + `Content-Type: text/event-stream`，按 SSE 帧推送

### SSE 帧格式

每帧形如：

```
event: <type>
data: <JSON>

```

（两个换行符结尾。）

### 事件类型

| event type | data schema | 出现时机 |
|---|---|---|
| `pr` | `{ id, owner, repo, pr, url, head_sha, title }` | 首帧，GitHub 拉取成功后立刻发，让前端先渲头部 |
| `summary_delta` | `{ "delta": "增量文本" }` | summary 阶段一帧 markdown 输出，多帧拼接成完整 markdown |
| `risks_done` | `[{ file, line?, severity, category, confidence, reason }]` | risks 阶段完成（要么有 risks 要么空数组） |
| `suggestions_done` | `[{ file, line?, type, title, body, patch? }]` | suggestions 阶段完成 |
| `info` | `{ "message": "...", "code"?: "...", "stage"?: "..." }` | 状态提示（如 PR 无可评审改动 `empty_pr`）；`code` 见下方「错误码」 |
| `agent_reply` | `{ "steps": 3, "output": "markdown" }` | agent 模式的最终回答，结构化字段（不要去解析 `info` 的散文） |
| `error` | `{ "stage": "summary\|risks", "message": "...", "code"?: "..." }` | 某 stage 中途失败；不中止整条流 |
| `done` | `{}` | 所有 stage 完成，连接即将关闭 |

**risks 项字段**：

| 字段 | 类型 | 说明 |
|---|---|---|
| `file` | string | 文件路径 |
| `line` | int | 行号（可选；不确定时省略） |
| `severity` | string | `high` / `medium` / `low` |
| `category` | string | `bug` / `security` / `perf` / `style` / `other` |
| `confidence` | float | 0-1，LLM 自评把握度；前端 ≥ 0.9 默认展开 |
| `reason` | string | 中文说明，≤ 80 字 |

### 预检错误（不发 SSE）

除下表的通用参数错误外，所有面向用户的错误响应都额外带一个稳定的 `code` 字段（如
`{"error":"PR 不存在或为私有仓库","code":"pr_not_found"}`）。`error` 是中文散文，只服务 curl 与非浏览器
消费者；浏览器端一律按 `code` 从前端字典取本地化文案。全部取值见
`backend/internal/api/errcode.go`。

| Status | 触发条件 | 响应体 |
|---|---|---|
| 400 | 请求 body 非合法 JSON | `{"error":"invalid request body"}` |
| 400 | `url` 字段为空 | `{"error":"url is required"}` |
| 400 | `url` 不是合法 GitHub PR 链接 | `{"error":"invalid GitHub PR URL"}` |
| 502 | GitHub API 调用失败（网络、404、速率限制） | `{"error":"fetch upstream failed","detail":"..."}` |

### 性能与限制

- 单 PR 文件上限 **100**（一页拉到上限，超出由 prctx 层裁剪后续 PR 处理）
- **首字节延迟 < 200ms**（`pr` 元信息帧立刻发），summary 一字一字流出，UX 远好于同步等 25s
- 默认 `LLM_PROVIDER=mock` 不调真实 LLM，无 key 也能跑（演示用，risks 阶段会发 `error` event）

### 客户端示例

**curl**（看原始 SSE 输出）：

```bash
curl -N -X POST http://localhost:8080/api/review \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://github.com/golang/go/pull/12345"}'
```

`-N` 关掉 curl 的输出缓冲，让帧实时显示。

**JavaScript fetch + ReadableStream**（`EventSource` 只支持 GET，POST + SSE 必须手动解析）：

```javascript
const res = await fetch("/api/review", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ url }),
});
const reader = res.body.getReader();
const decoder = new TextDecoder();
let buf = "";
while (true) {
  const { done, value } = await reader.read();
  if (done) break;
  buf += decoder.decode(value, { stream: true });
  const frames = buf.split("\n\n");
  buf = frames.pop();
  for (const f of frames) {
    // 解析 event: / data: 行，按 type dispatch
  }
}
```

完整封装见 `frontend/lib/sse.ts` 的 `streamReview`。

---

## POST /api/review/:id/steer

对已完成的评审追加引导：重跑某个 stage，或跑 agent 深挖。同样是 SSE。

```http
POST /api/review/abc123/steer
Content-Type: application/json

{ "text": "重点看并发安全", "stage": "risks", "mode": "stage", "locale": "en" }
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `text` | string | 是 | 用户引导文本 |
| `mode` | string | 否 | `"stage"`（默认，重跑 stage）/ `"agent"`（跑 ReAct 循环 + 工具调用） |
| `stage` | string | `mode=stage` 时是 | `"risks"` / `"suggestions"`；`mode=agent` 忽略 |
| `locale` | string | 否 | 同 `POST /api/review`，回退链一致 |

事件类型复用上面那张表；`risks_done` / `suggestions_done` 在这里改名为 `steered_risks_done` /
`steered_suggestions_done`，agent 模式额外发 `tool_call_start` / `tool_call_done` / `agent_reply`。

---

## GET /api/perms

查当前用户对某仓库的权限，驱动前端「💬 评论 / ✅ 提交」按钮的可用态与禁用原因。

```http
GET /api/perms?owner=ecstasoy&repo=LGTM
```

```json
{
  "authenticated": true,
  "permission": "read",
  "can_comment": false,
  "can_commit": false,
  "reason": "对此仓库无评论权限（需 triage / write / admin）",
  "reason_code": "no_comment_permission"
}
```

`reason` 是中文散文，`reason_code` 是它的稳定机读对应物（同一套 `errcode.go` 取值），前端优先按
`reason_code` 查字典。GitHub 权限查询失败那一支带的是上游动态报错，没有固定 code，此时 `reason_code`
缺省，前端原样展示。

---

## i18n 契约（改后端错误 / SSE 文案前先读这四条）

前端把所有 UI 文案集中在 `frontend/lib/i18n/dictionaries/{zh,en}.ts`，后端只发机读 code。这套分工靠下面
四条规则维持，其中三条编译器查不出来：

1. **`errcode.go` 每加一个常量，两本字典的 `errors.byCode` 都要加条目。** `byCode` 标了
   `as Record<string, string>`，是唯一逃出 `Dict = typeof zh` 类型检查的命名空间——只加中文不会报错。
   `frontend/lib/i18n/dictionaries/byCode.test.ts` 卡两边 key 集合相等；漏了会让测试红，而不是让用户看到中文。
2. **凡是用户会读到的文本，一律过 `friendlyError(message, t, code)`，不要直接渲染后端字符串。** 后端散文
   永远是中文；带了 code 但字典里没有时，`friendlyError` 退回通用文案，不会把中文漏到英文界面。
3. **SSE 帧里的散文不是机读值。** 前端不许 `startsWith` / 正则去解析后端文案来判定状态——需要什么值就
   在帧里加字段（`agent_reply` 的 `steps` / `output` 就是为此从 `info` 散文里拆出来的）。
4. **`frontend/lib/i18n/locale.ts` 的 `negotiate()` 和 `backend/internal/i18n/locale.go` 的
   `FromAcceptLanguage` 必须同步改。** 两边各自解析 `Accept-Language`，行为分叉会导致「英文界面 + 中文评审」。

---

## GET /api/health

存活探针。前端 / 监控用来确认后端已起。

```http
GET /api/health
```

**成功** `200 OK`

```json
{ "status": "ok" }
```

---

## GET /api/reviews

历史评审分页列表。**未实现**，后续 PR 落地（依赖 store 模块接通）。

预期形态：

```http
GET /api/reviews?limit=20&cursor=<ulid>
```

返回最近 `limit` 条 review 摘要（按 created_at desc），每项含 risks 严重度计数 `{high, medium, low}`。

---

## GET /api/reviews/:id

按 id 取单条评审详情。**未实现**，后续 PR 落地。

预期形态：

```http
GET /api/reviews/abc123def456
```

返回完整的 `summary` + `risks[]` + `suggestions[]`。

`?live=1` 模式时改走 SSE 推送（与 `POST /api/review` 同一套事件协议）。
