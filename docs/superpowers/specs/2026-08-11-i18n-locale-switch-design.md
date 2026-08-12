# 中英切换（i18n）设计

日期：2026-08-11
状态：已确认，待实现

## 目标

让英语用户能完整使用 LGTM。「完整」的定义是界面文案和 LLM 生成的评审正文都是英文——只翻界面而评审摘要仍是中文，对英语用户没有实际价值。

## 已确认的产品决策

| 问题 | 决策 |
| --- | --- |
| i18n 覆盖到哪一层 | UI 文案 + LLM 输出都要英文 |
| 首次访问默认语言 | 跟随浏览器语言（服务端读 `Accept-Language`） |
| 切到英文后看历史中文评审 | 正文保持原样，只有 UI 外壳变英文；页面给一行提示说明该评审生成时的语言 |

## 术语

统一用 **locale** 指界面/评审语言（值域 `"zh" | "en"`）。

不用 `lang`：`backend/internal/api/lang.go` 已经占用了这个词，指的是 PR 的**编程语言**识别（Go / TypeScript / Python），与 i18n 无关。两个概念在同一个 codebase 里同名会持续制造误读。

## 一、前端 locale 机制

### 存储：cookie，不是 localStorage

主题用 localStorage 是对的——主题只是 CSS 变量，`ThemeScript` 在水合前改一个 DOM 属性就完事。文案不适用同一套：服务端渲染时读不到 localStorage，若 Provider 在服务端一律默认中文、客户端再纠正，英语用户会看到整页中文闪烁，或触发全站水合不匹配。

cookie 一次解决三件事：

- `app/layout.tsx`（server component）用 `await cookies()` 读到 locale，SSR 直接渲染正确语言，零闪烁零 mismatch
- 首次访问无 cookie 时，服务端读 `Accept-Language` 做协商，在服务端落实「跟随浏览器语言」，同样没有闪烁
- `generateMetadata` 也能读到 cookie，页面 `title` / `description` 一并本地化

代价：`cookies()` 会让路由退出静态预渲染。本项目所有页面都是 `"use client"`、数据全部客户端从 API 拉取，没有真正静态的内容，因此可以接受。

### 文件结构

```
lib/i18n/locale.ts            Locale 类型、cookie 名 lgtm-locale、negotiate(acceptLanguage)
lib/i18n/dictionaries/zh.ts   唯一真源，嵌套对象
lib/i18n/dictionaries/en.ts   const en: Dict = {...}，其中 type Dict = typeof zh
lib/i18n/context.tsx          "use client"：I18nProvider / useLocale / useT
components/LocaleToggle.tsx   摆在 ThemeToggle 旁边
```

### 取词形态

`useT()` 直接返回字典对象本身，按属性取值：

```tsx
const t = useT();
<span>{t.nav.history}</span>
<span>{t.review.adoptedCount(3)}</span>   // 需要插值的条目就是函数
```

没有字符串 key、没有运行时查表、没有新依赖。`en.ts` 声明为 `typeof zh` 之后，漏翻一个 key 就是编译错误。

### 切换动作

写 `document.cookie` + 更新 context state。当场换语言不刷新页面；cookie 只服务下一次 SSR。

### 前提验证

`app/` 下所有页面（landing / history / review）都是 `"use client"`，因此 Context 在整棵组件树可用，不需要引入 `app/[locale]/` 路由段。

## 二、后端 locale 链路

### 评审缓存唯一键（必须先解决）

`reviews` 表上两个 partial unique index 是 `(owner, repo, pr_number, head_sha)`，`Store.Get` 按此查询。若不改动：

- 一条 PR 已有中文评审后，英语用户提交同一条 PR 会静默命中缓存拿到中文结果
- 因为唯一约束，英文版根本写不进去

所以 `locale` 必须进唯一键，不是可选项。

### Schema 变更

`internal/store/schema.sql` 与 `postgres_schema.sql`：

- `reviews` 加 `locale TEXT NOT NULL DEFAULT 'zh'`
- `DROP INDEX IF EXISTS` 两个唯一索引，带上 `locale` 重建

项目没有 migration 框架，schema 是每次启动 `CREATE TABLE IF NOT EXISTS` 幂等重放的，老库拿不到新列。需要在 `NewSQLiteStore` 里补一句幂等的 `ALTER TABLE reviews ADD COLUMN locale`（SQLite 吞掉 duplicate column 错误；Postgres 用 `ADD COLUMN IF NOT EXISTS`）。

对应地 `store.Record` 加 `Locale string`，`Store.Get` 签名加 locale 参数。

### Prompt 分语言：双文件，不用模板变量

- 现有三个模板重命名为 `summary.zh.tmpl` / `risks.zh.tmpl` / `suggestions.zh.tmpl`
- 另写三个原生英文版 `summary.en.tmpl` / `risks.en.tmpl` / `suggestions.en.tmpl`
- `prompts.Parse` 之上加一层 `ParseFor(stage, locale)` 拼文件名

不用 `{{.Locale}}` 占位符拼接，因为这些 prompt 正文本身是中文写的，且带有「≤ 80 字」这类在英文里没有对应物的约束（字 vs. words vs. characters）。变量拼出来的会是中文骨架套英文皮的劣质 prompt。此处 DRY 让位于 prompt 质量。

### 硬编码 System 串

四处改为按 locale 取的 map：

- `internal/review/summary.go:35`
- `internal/review/risks.go:43`
- `internal/review/suggestions.go:52`
- `internal/api/steer.go:202`（Agent 对话）

### locale 如何传入

`POST /api/reviews` 及 per-stage / SSE 变体，三级回退：

```
请求体 locale 字段 → Accept-Language 头 → LGTM_DEFAULT_LOCALE
```

前端每次提交带上当前 locale。Agent 对话（steer）同理。

`LGTM_DEFAULT_LOCALE`（默认 `zh`）是全局唯一的兜底配置：既作为上述三级回退的最后一级，也直接用于 webhook 自动评审——那条路径没有用户在场，请求体和 `Accept-Language` 都无从谈起。

无法识别的 locale 值（既非 `zh` 也非 `en`）一律落到 `LGTM_DEFAULT_LOCALE`，不报错。

### 回流到前端

`review.Result` 带上 `locale` 字段。review 页与 history 列表在 `result.locale !== 当前 UI locale` 时显示一行克制的提示（例如 "This review was generated in Chinese."），兑现「历史评审保持原样」的决策。

## 三、测试策略

### 前端

前端没有单测设施，CI（`.github/workflows/ci.yml`）只跑 `tsc --noEmit` + `pnpm build`。`en: Dict = typeof zh` 的类型约束因此就是真实的 CI 门禁：漏翻直接让 CI 变红。不额外引入测试框架。

**明确不做**：用 grep 卡「源码里不许有中文字面量」。注释仍是中文且按既定节奏机会性翻译，这类门禁会持续误报。

**手工验证**：用 preview 起 dev server，中英各走一遍 landing → 提交 → review 页并截图。

### 后端

补四组 Go 测试：

1. `ParseFor` 覆盖 3 stage × 2 locale 均可解析
2. store 往返：同一 PR 的 zh / en 两条记录能共存且 `Get` 各取各的（唯一键改动的核心回归）
3. schema 重复应用 + `ALTER` 幂等不报错
4. API 三级回退：body > `Accept-Language` > default

**明确不做**：断言 LLM 实际输出语言。那是模型行为，写成测试既 flaky 又需要真实 API 调用；手工验证一次即可。

## 四、PR 拆分

| # | 范围 | 可独立验证的效果 |
| --- | --- | --- |
| 1 | 前端 i18n 地基 + 外壳：`lib/i18n/*`、Provider、cookie SSR 协商、`LocaleToggle`、NavBar / Footer / landing / history 文案 | 切到 EN 后首页全英文 |
| 2 | review 工作区文案：review 页及全部子组件，加 `lib/errors.ts` / `notifications.ts` / `sse.ts` 中的用户可见串 | 切到 EN 后评审页界面全英文 |
| 3 | 后端 locale 链路：prompts 分语言、System 串、schema `locale` 列 + 唯一键重建、`Store.Get` 签名、API 三级回退 | 带 `locale=en` 请求可拿到英文评审，且不与中文缓存冲突 |
| 4 | 端到端接通：前端提交带 locale、`Result.locale` 回流、跨语言提示条 | 英语用户提交 PR 拿到英文评审；看旧中文评审有提示 |

PR 3 与 PR 4 是 stack 关系（4 依赖 3 的 API）。**若 PR 4 的 base 指向 PR 3 的分支，合并前必须先 retarget 到 main**，否则提交会被孤立。

## 已排除的方案

**`next-intl` + `app/[locale]/` 路由段。** 工业标准做法，SSR 正确、`/en/review/123` 可分享、SEO 友好。代价是把所有路由挪进 `[locale]/`、加 middleware 做语言协商（需与现有 `/api/:path*` rewrite 排队）、每个 `router.push` / `<Link>` 都要 locale-aware，并新增一个依赖。而它的独占收益（SSR、SEO）对一个全 client component、核心页面需登录才有内容的产品基本吃不到。

若 v2 真要做 SEO 落地页，届时单独把 landing 迁到 `[locale]/` 即可，不影响本设计的字典层。

**只翻外壳、review 工作区保持中文。** 与「UI + LLM 都英文」的决策矛盾。
