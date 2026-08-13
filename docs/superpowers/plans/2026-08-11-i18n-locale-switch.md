# i18n 中英切换实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让英语用户拿到全英文的 LGTM——界面文案与 LLM 生成的评审正文都是英文。

**Architecture:** 前端用 cookie 存 locale，`app/layout.tsx`（server component）读 cookie / `Accept-Language` 决定 SSR 语言，再通过 React Context 把类型化字典发给整棵组件树（所有页面已是 `"use client"`）。后端把 locale 沿 API → orchestrator → prompt 模板一路传下去，并将其纳入 `reviews` 表的唯一键，使同一 PR 的中英两份评审可以共存。

**Tech Stack:** Next.js 16 App Router / React 19 / TypeScript 5.7 / Tailwind v4；Go + Gin + SQLite/Postgres；新增 vitest（仅用于 `lib/i18n` 纯函数）。

**Spec:** `docs/superpowers/specs/2026-08-11-i18n-locale-switch-design.md`

## Global Constraints

- locale 值域固定为 `"zh" | "en"`；无法识别的输入一律落到默认值，不报错
- 统一用 **locale** 命名 i18n 概念。**禁止**用 `lang`——`backend/internal/api/lang.go` 已占用该词表示 PR 的编程语言
- cookie 名：`lgtm-locale`，`path=/`，`max-age=31536000`，`samesite=lax`，非 httpOnly
- 后端默认 locale 配置项：`LGTM_DEFAULT_LOCALE`，默认 `"zh"`
- 代码注释、commit message、PR 正文一律英文；一条注释默认一行陈述句
- 前端字典唯一真源是 `lib/i18n/dictionaries/zh.ts`；`en.ts` 声明为 `Dict = typeof zh`，漏翻即编译错误
- 不新增运行时 i18n 依赖（不引 next-intl / i18next）
- 每个 PR 合并前 `cd frontend && pnpm exec tsc --noEmit && pnpm build` 与 `cd backend && go test ./...` 全绿

---

## 文件结构

**新建（前端）**

| 文件 | 职责 |
| --- | --- |
| `frontend/lib/i18n/locale.ts` | `Locale` 类型、cookie 名、默认值、`isLocale`、`negotiate(acceptLanguage)`。纯函数，无 React 依赖，服务端与客户端共用 |
| `frontend/lib/i18n/locale.test.ts` | `negotiate` 的 vitest 用例 |
| `frontend/lib/i18n/dictionaries/zh.ts` | 中文字典 + 导出 `Dict` 类型 |
| `frontend/lib/i18n/dictionaries/en.ts` | 英文字典，类型受 `Dict` 约束 |
| `frontend/lib/i18n/context.tsx` | `I18nProvider` / `useI18n` / `useT` / `useLocale` |
| `frontend/components/LocaleToggle.tsx` | 语言切换按钮 |
| `frontend/vitest.config.ts` | vitest 配置（仅 `lib/**/*.test.ts`） |

**新建（后端）**

| 文件 | 职责 |
| --- | --- |
| `backend/internal/i18n/locale.go` | `Locale` 类型、`Normalize`、`FromAcceptLanguage`。前端 `negotiate` 的 Go 对应物 |
| `backend/internal/i18n/locale_test.go` | 上述两函数的用例 |
| `backend/internal/prompts/summary.en.tmpl` | 原生英文 summary prompt |
| `backend/internal/prompts/risks.en.tmpl` | 原生英文 risks prompt |
| `backend/internal/prompts/suggestions.en.tmpl` | 原生英文 suggestions prompt |
| `backend/internal/store/indexes.sql` | 从 `schema.sql` 拆出的索引 DDL，必须在补列之后执行 |
| `backend/internal/api/errcode.go` | 错误 code 常量 + `abortWithCode` 辅助函数 |

**重命名（后端）**

`summary.tmpl` → `summary.zh.tmpl`、`risks.tmpl` → `risks.zh.tmpl`、`suggestions.tmpl` → `suggestions.zh.tmpl`

**修改**

前端：`app/layout.tsx`、`app/(main)/layout.tsx`、`app/(main)/page.tsx`、`app/(main)/history/page.tsx`、`app/review/[id]/page.tsx`、`app/error.tsx`、`app/not-found.tsx`、`app/review/[id]/loading.tsx`、`components/NavBar.tsx`、`components/Footer.tsx`、`components/SuggestionList.tsx`、`components/auth/UserMenu.tsx`、`components/landing/*`、`components/review/*`、`components/ui/Toast.tsx`、`components/ui/badge.tsx`、`components/ui/ci-status.tsx`、`components/ui/file-status-badge.tsx`、`components/ui/spinner.tsx`、`components/ui/button.tsx`、`components/ui/avatar.tsx`、`lib/api.ts`、`lib/errors.ts`、`lib/format.ts`、`lib/perms.ts`、`lib/reviews.ts`、`lib/types.ts`、`package.json`

后端：`internal/config/config.go`、`internal/prompts/embed.go`、`internal/review/{summary,risks,suggestions,orchestrator}.go`、`internal/store/{store,sqlite,postgres,schema.sql,postgres_schema.sql}.go`、`internal/api/{review,steer,commit,comment,perms,auth,webhook}.go`

---

# PR 1 — 前端 i18n 地基 + 外壳

分支：`feat/i18n-frontend-core`（从 `main` 切出）

### Task 1: locale 纯函数与 vitest 接线

**Files:**
- Create: `frontend/lib/i18n/locale.ts`
- Create: `frontend/lib/i18n/locale.test.ts`
- Create: `frontend/vitest.config.ts`
- Modify: `frontend/package.json`
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: 无
- Produces: `type Locale = "zh" | "en"`、`const LOCALE_COOKIE = "lgtm-locale"`、`const DEFAULT_LOCALE: Locale`、`isLocale(v: unknown): v is Locale`、`negotiate(acceptLanguage: string | null | undefined): Locale`

前端目前没有测试设施（CI 只跑 `tsc --noEmit` + `pnpm build`）。`negotiate` 要解析 `Accept-Language` 的 q 权重，是本次唯一有真实分支逻辑的纯函数，值得一个测试运行器。vitest 只用于 `lib/**/*.test.ts`，不引入组件测试。

- [ ] **Step 1: 装 vitest 并加 test script**

```bash
cd frontend && pnpm add -D vitest@3
```

在 `package.json` 的 `scripts` 中加一行：

```json
"test": "vitest run"
```

- [ ] **Step 2: 写 vitest 配置**

创建 `frontend/vitest.config.ts`：

```ts
import { defineConfig } from "vitest/config";

// Only pure helpers under lib/ are unit-tested; components are covered by tsc and manual browser checks.
export default defineConfig({
  test: {
    include: ["lib/**/*.test.ts"],
    environment: "node",
  },
});
```

- [ ] **Step 3: 写失败的测试**

创建 `frontend/lib/i18n/locale.test.ts`：

```ts
import { describe, expect, it } from "vitest";

import { DEFAULT_LOCALE, isLocale, negotiate } from "./locale";

describe("negotiate", () => {
  it("picks zh for a Chinese-first header", () => {
    expect(negotiate("zh-CN,zh;q=0.9,en;q=0.8")).toBe("zh");
  });

  it("picks en for an English-first header", () => {
    expect(negotiate("en-US,en;q=0.9")).toBe("en");
  });

  it("orders by q weight, not by source order", () => {
    expect(negotiate("en;q=0.8,zh-CN;q=0.9")).toBe("zh");
  });

  it("is case-insensitive", () => {
    expect(negotiate("EN-GB")).toBe("en");
  });

  it("falls back to the default for unsupported languages", () => {
    expect(negotiate("fr-FR,de;q=0.9")).toBe(DEFAULT_LOCALE);
  });

  it("falls back to the default for missing or empty headers", () => {
    expect(negotiate(null)).toBe(DEFAULT_LOCALE);
    expect(negotiate("")).toBe(DEFAULT_LOCALE);
  });

  it("ignores a malformed q value instead of throwing", () => {
    expect(negotiate("en;q=abc,zh")).toBe("zh");
  });
});

describe("isLocale", () => {
  it("accepts supported values only", () => {
    expect(isLocale("zh")).toBe(true);
    expect(isLocale("en")).toBe(true);
    expect(isLocale("fr")).toBe(false);
    expect(isLocale(undefined)).toBe(false);
  });
});
```

- [ ] **Step 4: 跑测试确认失败**

Run: `cd frontend && pnpm test`
Expected: FAIL —— 报错找不到 `./locale` 模块

- [ ] **Step 5: 实现 locale.ts**

创建 `frontend/lib/i18n/locale.ts`：

```ts
// Locale primitives shared by the server layout and the client provider; no React imports allowed here.

export type Locale = "zh" | "en";

export const LOCALE_COOKIE = "lgtm-locale";
export const DEFAULT_LOCALE: Locale = "zh";

export function isLocale(value: unknown): value is Locale {
  return value === "zh" || value === "en";
}

// negotiate returns the highest-q supported tag from an Accept-Language header, or DEFAULT_LOCALE.
export function negotiate(acceptLanguage: string | null | undefined): Locale {
  if (!acceptLanguage) return DEFAULT_LOCALE;

  const tags = acceptLanguage
    .split(",")
    .map((part) => {
      const [rawTag, ...params] = part.trim().split(";");
      const qParam = params.map((p) => p.trim()).find((p) => p.startsWith("q="));
      const parsed = qParam ? Number.parseFloat(qParam.slice(2)) : 1;
      // A malformed q is treated as lowest priority rather than an error.
      return { tag: rawTag.trim().toLowerCase(), weight: Number.isNaN(parsed) ? 0 : parsed };
    })
    .filter((t) => t.tag !== "")
    .sort((a, b) => b.weight - a.weight);

  for (const { tag } of tags) {
    if (tag === "zh" || tag.startsWith("zh-")) return "zh";
    if (tag === "en" || tag.startsWith("en-")) return "en";
  }
  return DEFAULT_LOCALE;
}
```

- [ ] **Step 6: 跑测试确认通过**

Run: `cd frontend && pnpm test`
Expected: PASS，9 个用例全绿

- [ ] **Step 7: 把 test 接进 CI**

在 `.github/workflows/ci.yml` 的 `frontend` job 中，`pnpm exec tsc --noEmit` 步骤之后插入：

```yaml
      - name: Test
        run: pnpm test
```

- [ ] **Step 8: 提交**

```bash
git add frontend/lib/i18n/locale.ts frontend/lib/i18n/locale.test.ts frontend/vitest.config.ts frontend/package.json frontend/pnpm-lock.yaml .github/workflows/ci.yml
git commit -m "feat(i18n): add locale primitives and Accept-Language negotiation"
```

---

### Task 2: 字典骨架与类型约束

**Files:**
- Create: `frontend/lib/i18n/dictionaries/zh.ts`
- Create: `frontend/lib/i18n/dictionaries/en.ts`

**Interfaces:**
- Consumes: 无
- Produces: `export const zh`、`export type Dict = typeof zh`、`export const en: Dict`

本任务只放 PR 1 外壳所需的 key。review 工作区的 key 在 PR 2 追加。

- [ ] **Step 1: 写中文字典**

创建 `frontend/lib/i18n/dictionaries/zh.ts`：

```ts
// Source of truth for all UI copy. en.ts is typed against `Dict`, so a missing key fails the build.
// Entries that need interpolation are functions.
export const zh = {
  meta: {
    title: "LGTM — AI 辅助代码评审",
    description:
      "粘贴任意 GitHub PR 链接，30 秒拿到结构化评审：变更总结 / 风险识别 / 行内建议。",
    ogDescription: "粘贴任意 GitHub PR 链接，30 秒拿到结构化评审。",
  },
  nav: {
    home: "LGTM 首页",
    review: "评审",
    history: "历史",
    switchLocale: "切换到英文",
  },
  footer: {
    copyright: "© ecstasoy 2026",
  },
  landing: {
    heroTitleTop: "Looks good to me?",
    heroTitleBottom: "Looks good to you!",
    heroLeadPrefix: "粘个 PR 链接，",
    heroLeadMiddle: " 三十秒内给你",
    heroLeadOutputs: "总结 / 风险 / 行内建议",
    heroLeadSuffix: "，可一键发到原 PR。",
    urlPlaceholder: "https://github.com/owner/repo/pull/123",
    urlAriaLabel: "GitHub Pull Request URL",
    submit: "开始评审",
    modelPickerLabel: "选择评审模型",
    perStageOff: "分阶段选择模型",
    perStageOn: "分阶段（摘要 / 风险 / 建议 各自选模型）",
    stageModelAriaLabel: (stage: string) => `${stage}阶段的模型`,
    examplesLabel: "试试：",
  },
  stages: {
    summary: "摘要",
    risks: "风险",
    suggestions: "建议",
  },
};

export type Dict = typeof zh;
```

注意：不要加 `as const`。加了之后字符串会被推断成字面量类型，`en.ts` 无法赋不同的值。

- [ ] **Step 2: 写英文字典**

创建 `frontend/lib/i18n/dictionaries/en.ts`：

```ts
import type { Dict } from "./zh";

export const en: Dict = {
  meta: {
    title: "LGTM — AI-assisted code review",
    description:
      "Paste any GitHub PR link and get a structured review in 30 seconds: change summary, risk analysis, and inline suggestions.",
    ogDescription: "Paste any GitHub PR link and get a structured review in 30 seconds.",
  },
  nav: {
    home: "LGTM home",
    review: "Review",
    history: "History",
    switchLocale: "Switch to Chinese",
  },
  footer: {
    copyright: "© ecstasoy 2026",
  },
  landing: {
    heroTitleTop: "Looks good to me?",
    heroTitleBottom: "Looks good to you!",
    heroLeadPrefix: "Drop in a PR link and ",
    heroLeadMiddle: " gives you a ",
    heroLeadOutputs: "summary, risks, and inline suggestions",
    heroLeadSuffix: " in thirty seconds — postable back to the PR in one click.",
    urlPlaceholder: "https://github.com/owner/repo/pull/123",
    urlAriaLabel: "GitHub pull request URL",
    submit: "Start review",
    modelPickerLabel: "Choose review model",
    perStageOff: "Per-stage model",
    perStageOn: "Per-stage (summary / risks / suggestions choose separately)",
    stageModelAriaLabel: (stage: string) => `Model for the ${stage} stage`,
    examplesLabel: "Try:",
  },
  stages: {
    summary: "Summary",
    risks: "Risks",
    suggestions: "Suggestions",
  },
};
```

- [ ] **Step 3: 验证类型约束真的生效**

临时从 `en.ts` 里删掉 `nav.history` 一行，然后运行：

Run: `cd frontend && pnpm exec tsc --noEmit`
Expected: FAIL，报 `Property 'history' is missing in type ... but required in type ...`

把删掉的行加回去，再跑一次：

Run: `cd frontend && pnpm exec tsc --noEmit`
Expected: PASS

这一步是在验证本设计的核心安全网，不要跳过。

- [ ] **Step 4: 提交**

```bash
git add frontend/lib/i18n/dictionaries/
git commit -m "feat(i18n): add zh/en dictionaries with compile-time key parity"
```

---

### Task 3: I18nProvider 与 hooks

**Files:**
- Create: `frontend/lib/i18n/context.tsx`

**Interfaces:**
- Consumes: `Locale`、`LOCALE_COOKIE`（Task 1）；`zh`、`en`、`Dict`（Task 2）
- Produces: `I18nProvider({ initialLocale, children })`、`useI18n(): { locale, setLocale, t }`、`useT(): Dict`、`useLocale(): Locale`

- [ ] **Step 1: 实现 context**

创建 `frontend/lib/i18n/context.tsx`：

```tsx
"use client";

import { createContext, useCallback, useContext, useMemo, useState } from "react";

import { LOCALE_COOKIE, type Locale } from "./locale";
import { en } from "./dictionaries/en";
import { zh, type Dict } from "./dictionaries/zh";

const DICTIONARIES: Record<Locale, Dict> = { zh, en };

interface I18nValue {
  locale: Locale;
  setLocale: (next: Locale) => void;
  t: Dict;
}

const I18nContext = createContext<I18nValue | null>(null);

// initialLocale comes from the server layout, so the first client render already matches the SSR output.
export function I18nProvider({
  initialLocale,
  children,
}: {
  initialLocale: Locale;
  children: React.ReactNode;
}) {
  const [locale, setLocaleState] = useState<Locale>(initialLocale);

  const setLocale = useCallback((next: Locale) => {
    setLocaleState(next);
    document.documentElement.lang = next === "zh" ? "zh-CN" : "en";
    // Not httpOnly: switching happens client-side and the server only reads it to pick the SSR language.
    document.cookie = `${LOCALE_COOKIE}=${next}; path=/; max-age=31536000; samesite=lax`;
  }, []);

  const value = useMemo<I18nValue>(
    () => ({ locale, setLocale, t: DICTIONARIES[locale] }),
    [locale, setLocale],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nValue {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useI18n must be called inside I18nProvider");
  return ctx;
}

export function useT(): Dict {
  return useI18n().t;
}

export function useLocale(): Locale {
  return useI18n().locale;
}
```

- [ ] **Step 2: 类型检查**

Run: `cd frontend && pnpm exec tsc --noEmit`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add frontend/lib/i18n/context.tsx
git commit -m "feat(i18n): add I18nProvider and dictionary hooks"
```

---

### Task 4: 根布局服务端定语言

**Files:**
- Modify: `frontend/app/layout.tsx`

**Interfaces:**
- Consumes: `isLocale`、`negotiate`、`LOCALE_COOKIE`、`Locale`（Task 1）；`zh`、`en`（Task 2）；`I18nProvider`（Task 3）
- Produces: `resolveServerLocale(): Promise<Locale>`（本文件内私有）

这是整套设计的关键接缝：服务端把 locale 定死后再交给客户端，因此没有闪烁也没有水合不匹配。

- [ ] **Step 1: 改写 layout.tsx**

把 `frontend/app/layout.tsx` 中 `metadata` 常量与 `RootLayout` 替换为：

```tsx
import type { Metadata } from "next";
import { cookies, headers } from "next/headers";

import { I18nProvider } from "@/lib/i18n/context";
import { en } from "@/lib/i18n/dictionaries/en";
import { zh } from "@/lib/i18n/dictionaries/zh";
import { isLocale, LOCALE_COOKIE, negotiate, type Locale } from "@/lib/i18n/locale";

// An explicit cookie wins; otherwise negotiate from Accept-Language so first-time visitors land in their own language.
// Reading cookies() opts every route out of static prerendering, which costs nothing here: all page data is fetched client-side.
async function resolveServerLocale(): Promise<Locale> {
  const saved = (await cookies()).get(LOCALE_COOKIE)?.value;
  if (isLocale(saved)) return saved;
  return negotiate((await headers()).get("accept-language"));
}

export async function generateMetadata(): Promise<Metadata> {
  const locale = await resolveServerLocale();
  const t = locale === "en" ? en : zh;
  return {
    title: t.meta.title,
    description: t.meta.description,
    icons: {
      icon: [
        { url: "/brand/svg/favicon.svg", type: "image/svg+xml" },
        { url: "/brand/png/favicon-32.png", sizes: "32x32", type: "image/png" },
        { url: "/brand/png/favicon-16.png", sizes: "16x16", type: "image/png" },
      ],
      apple: [{ url: "/brand/png/apple-touch-icon-180.png", sizes: "180x180" }],
    },
    manifest: "/manifest.webmanifest",
    openGraph: {
      title: t.meta.title,
      description: t.meta.ogDescription,
      images: ["/brand/png/og-social.png"],
      type: "website",
    },
  };
}

export default async function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const locale = await resolveServerLocale();
  return (
    <html
      lang={locale === "en" ? "en" : "zh-CN"}
      data-theme="light"
      data-density="comfortable"
      suppressHydrationWarning
    >
      <head>
        <ThemeScript />
      </head>
      <body className={`${geistSans.variable} ${geistMono.variable}`}>
        <I18nProvider initialLocale={locale}>
          {children}
          <ToastContainer />
        </I18nProvider>
      </body>
    </html>
  );
}
```

保留文件里已有的 `geistSans` / `geistMono` 定义、`import "./globals.css"`、`ThemeScript` 与 `ToastContainer` 的 import，并把顶部关于根布局职责的中文注释改写成英文一行。

- [ ] **Step 2: 构建验证**

Run: `cd frontend && pnpm exec tsc --noEmit && pnpm build`
Expected: PASS。构建日志中 `/` 与 `/history` 会从静态标记变为动态（`ƒ`），这是预期结果

- [ ] **Step 3: 提交**

```bash
git add frontend/app/layout.tsx
git commit -m "feat(i18n): resolve locale server-side from cookie and Accept-Language"
```

---

### Task 5: LocaleToggle 组件

**Files:**
- Create: `frontend/components/LocaleToggle.tsx`
- Modify: `frontend/components/NavBar.tsx`

**Interfaces:**
- Consumes: `useI18n`（Task 3）
- Produces: `LocaleToggle({ className }: { className?: string })`

- [ ] **Step 1: 写组件**

创建 `frontend/components/LocaleToggle.tsx`：

```tsx
"use client";

import { Languages } from "lucide-react";

import { useI18n } from "@/lib/i18n/context";
import { cn } from "@/lib/utils";

// Sits next to ThemeToggle. No hydration placeholder needed: the initial locale comes from the server.
export function LocaleToggle({ className }: { className?: string }) {
  const { locale, setLocale, t } = useI18n();
  const next = locale === "zh" ? "en" : "zh";

  return (
    <button
      type="button"
      onClick={() => setLocale(next)}
      aria-label={t.nav.switchLocale}
      title={t.nav.switchLocale}
      className={cn(
        "inline-flex h-8 items-center gap-1 rounded-md border border-border px-2 text-xs font-medium text-muted transition-colors hover:bg-surface-hover hover:text-text",
        className,
      )}
    >
      <Languages className="h-4 w-4" />
      {next === "en" ? "EN" : "中"}
    </button>
  );
}
```

- [ ] **Step 2: 挂进 NavBar**

在 `frontend/components/NavBar.tsx` 中 import `LocaleToggle`，并把右侧容器改为：

```tsx
      <div className="ml-auto flex items-center gap-2">
        <UserMenu />
        <LocaleToggle />
        <ThemeToggle />
      </div>
```

- [ ] **Step 3: 构建验证**

Run: `cd frontend && pnpm exec tsc --noEmit && pnpm build`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add frontend/components/LocaleToggle.tsx frontend/components/NavBar.tsx
git commit -m "feat(i18n): add locale toggle to the nav bar"
```

---

### Task 6: 外壳文案接字典

**Files:**
- Modify: `frontend/components/NavBar.tsx`
- Modify: `frontend/components/Footer.tsx`

**Interfaces:**
- Consumes: `useT`（Task 3）；`t.nav.*`、`t.footer.*`（Task 2）
- Produces: 无新导出

`Footer` 目前是 server component 且被 `app/(main)/layout.tsx`（server component）渲染，用不了 hook。把它改成 `"use client"`——它只有两个静态节点，代价可以忽略。

- [ ] **Step 1: NavBar 换字典**

在 `NavBar` 内加 `const t = useT();`，替换三处字面量：

- `aria-label="LGTM 首页"` → `aria-label={t.nav.home}`
- `<NavLink href="/" ...>评审</NavLink>` → `{t.nav.review}`
- `<NavLink href="/history" ...>历史</NavLink>` → `{t.nav.history}`

- [ ] **Step 2: Footer 换字典**

`frontend/components/Footer.tsx` 顶部加 `"use client";`，import `useT`，把 `<span>© ecstasoy 2026</span>` 改为 `<span>{t.footer.copyright}</span>`。文件顶部中文注释改写为英文一行。

- [ ] **Step 3: 构建验证**

Run: `cd frontend && pnpm exec tsc --noEmit && pnpm build`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add frontend/components/NavBar.tsx frontend/components/Footer.tsx
git commit -m "feat(i18n): translate nav bar and footer copy"
```

---

### Task 7: landing 页文案

**Files:**
- Modify: `frontend/components/landing/HeroBanner.tsx`
- Modify: `frontend/components/landing/UrlInputCard.tsx`
- Modify: `frontend/components/landing/HowItWorks.tsx`
- Modify: `frontend/components/landing/RiskPips.tsx`
- Modify: `frontend/components/landing/RecentReviewsList.tsx`
- Modify: `frontend/app/(main)/page.tsx`
- Modify: `frontend/lib/api.ts`
- Modify: `frontend/lib/i18n/dictionaries/zh.ts`
- Modify: `frontend/lib/i18n/dictionaries/en.ts`

**Interfaces:**
- Consumes: `useT`（Task 3）
- Produces: `lib/api.ts` 的 `STAGES` 从 `{ key, label }[]` 变为 `STAGE_KEYS: StageKey[]`；label 改由 `t.stages[key]` 提供

**提取方法（本任务及后续所有文案任务通用）：**

1. 打开文件，逐个找出**用户可见**的中文字面量：JSX 文本节点、`aria-label`、`title`、`placeholder`、`alt`。**跳过注释**——注释按既定节奏机会性翻译，不在本次范围
2. 在 `zh.ts` 中按组件所属区域找到或新建命名空间（如 `landing`、`review`、`history`），加 key。key 用描述用途的 camelCase（`submitButton` 而非 `startReview1`）
3. 需要插值的写成函数：`(n: number) => \`已采纳 ${n} 条\``
4. 在 `en.ts` 中补对应条目
5. 组件内 `const t = useT();`，把字面量换成 `t.<ns>.<key>`
6. 若组件是 server component 但需要 hook，加 `"use client"`

**HeroBanner 的实际改法**（该文件是 server component，需加 `"use client"`）：

```tsx
"use client";

import { useT } from "@/lib/i18n/context";

// Hero heading plus a one-line lead. Title font size clamps to the viewport.
export function HeroBanner() {
  const t = useT();
  return (
    <header>
      <h1 className="m-0 mb-3.5 font-semibold leading-[1.1] tracking-[-0.02em] text-[clamp(28px,4.4vw,44px)]">
        {t.landing.heroTitleTop}
        <br />
        <span className="text-muted">{t.landing.heroTitleBottom}</span>
      </h1>
      <p className="m-0 mb-7 max-w-[540px] text-base leading-[1.6] text-text-2">
        {t.landing.heroLeadPrefix}
        <strong className="font-semibold text-text">LGTM</strong>
        {t.landing.heroLeadMiddle}
        <strong className="font-semibold text-text">{t.landing.heroLeadOutputs}</strong>
        {t.landing.heroLeadSuffix}
      </p>
    </header>
  );
}
```

- [ ] **Step 1: 处理 lib/api.ts 的 STAGES**

`STAGES` 目前把中文 label 硬编码在数据层，而数据层模块拿不到 hook。改为只导出顺序：

```ts
// Stage order for the per-stage model picker. Labels live in the i18n dictionaries, keyed by StageKey.
export const STAGE_KEYS: StageKey[] = ["summary", "risks", "suggestions"];
```

删除原 `STAGES` 常量。`UrlInputCard.tsx` 中原本 `STAGES.map((s) => ...)` 改为：

```tsx
              {STAGE_KEYS.map((key) => (
                <label key={key} className="flex flex-col gap-1 text-xs text-muted">
                  {t.stages[key]}
                  <select
                    value={stageModels?.[key] ?? model}
                    onChange={(e) => onStageModelChange?.(key, e.target.value)}
                    disabled={disabled}
                    aria-label={t.landing.stageModelAriaLabel(t.stages[key])}
                    className={cn(selectCls, "w-full")}
                  >
```

- [ ] **Step 2: 检查 STAGES 的其他引用**

Run: `cd frontend && grep -rn "STAGES" app components lib`
Expected: 只剩 `lib/api.ts` 的定义和 `UrlInputCard.tsx` 的使用；若 `app/(main)/page.tsx` 也引用了，一并改成 `STAGE_KEYS`

- [ ] **Step 3: 按上述方法处理五个 landing 组件与 page.tsx**

`UrlInputCard.tsx` 已有 key（Task 2 中的 `landing.*`）直接接上。`HowItWorks.tsx`、`RiskPips.tsx`、`RecentReviewsList.tsx`、`app/(main)/page.tsx` 的文案按方法第 2 步新建 key，命名空间分别用 `landing`（前两个）与 `recentReviews`、`home`。

- [ ] **Step 4: 确认 landing 链路无残留中文字面量**

Run: `cd frontend && grep -nP '(?<![/*])\s*["'"'"'`>][^"'"'"'`<]*[\x{4e00}-\x{9fff}]' components/landing/*.tsx "app/(main)/page.tsx"`
Expected: 只剩注释行；任何 JSX 文本或属性值里的中文都要回到 Step 3 处理

- [ ] **Step 5: 构建验证**

Run: `cd frontend && pnpm exec tsc --noEmit && pnpm test && pnpm build`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add frontend/components/landing frontend/app frontend/lib
git commit -m "feat(i18n): translate landing page copy and move stage labels into dictionaries"
```

---

### Task 8: history 页文案

**Files:**
- Modify: `frontend/app/(main)/history/page.tsx`
- Modify: `frontend/app/(main)/layout.tsx`
- Modify: `frontend/components/auth/UserMenu.tsx`
- Modify: `frontend/lib/i18n/dictionaries/zh.ts`
- Modify: `frontend/lib/i18n/dictionaries/en.ts`

**Interfaces:**
- Consumes: `useT`（Task 3）
- Produces: 字典新增 `history` 与 `userMenu` 两个命名空间

- [ ] **Step 1: 按 Task 7 的提取方法处理三个文件**

`history/page.tsx` 有 57 处中文（含注释），`UserMenu.tsx` 有 25 处。命名空间用 `history` 与 `userMenu`。`app/(main)/layout.tsx` 只有注释是中文，把注释改写成英文即可，不需要加 key。

- [ ] **Step 2: 确认无残留**

Run: `cd frontend && grep -nP '(?<![/*])\s*["'"'"'`>][^"'"'"'`<]*[\x{4e00}-\x{9fff}]' "app/(main)/history/page.tsx" components/auth/UserMenu.tsx`
Expected: 只剩注释行

- [ ] **Step 3: 构建验证**

Run: `cd frontend && pnpm exec tsc --noEmit && pnpm test && pnpm build`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add frontend/app frontend/components/auth frontend/lib/i18n
git commit -m "feat(i18n): translate history page and user menu copy"
```

---

### Task 9: PR 1 浏览器验证与开 PR

**Files:** 无代码改动

- [ ] **Step 1: 起 dev server**

用 preview 工具（不要用 Bash 跑 dev server）：`preview_start` 传 `{name: "frontend"}`。若 `.claude/launch.json` 不存在则先创建：

```json
{
  "version": "0.0.1",
  "configurations": [
    { "name": "frontend", "runtimeExecutable": "pnpm", "runtimeArgs": ["--dir", "frontend", "dev"], "port": 3000 }
  ]
}
```

- [ ] **Step 2: 中文态走查**

`read_page` 确认首页 NavBar 显示"评审 / 历史"、hero 中文、`<html lang="zh-CN">`。`read_console_messages` 确认无水合警告。

- [ ] **Step 3: 切英文并验证**

点击 `LocaleToggle`，`read_page` 确认导航变 "Review / History"、hero 变英文。`javascript_tool` 执行 `document.documentElement.lang` 应返回 `"en"`，`document.cookie` 应含 `lgtm-locale=en`。

- [ ] **Step 4: 验证刷新后保持且无闪烁**

`navigate` 到当前 URL 重新加载，确认仍是英文；`read_console_messages` 确认无 hydration mismatch 报错。这是 cookie 方案相对 localStorage 的核心收益，必须实测。

- [ ] **Step 5: 截图存档**

`computer {action: "screenshot"}` 各存一张中英文首页。

- [ ] **Step 6: 开 PR**

```bash
git push -u origin feat/i18n-frontend-core
```

PR 标题：`feat(i18n): add locale switching infrastructure and translate the app shell`
PR 正文用项目固定的六段英文模板（What / How / Testing / Impact / Checklist / Next）。

---

# PR 2 — review 工作区文案

分支：`feat/i18n-review-workspace`（PR 1 合并后从 `main` 切出）

### Task 10: review 页主体与顶栏

**Files:**
- Modify: `frontend/app/review/[id]/page.tsx`
- Modify: `frontend/app/review/[id]/loading.tsx`
- Modify: `frontend/components/review/ReviewTopBar.tsx`
- Modify: `frontend/components/review/Sidebar.tsx`
- Modify: `frontend/components/review/StageChip.tsx`
- Modify: `frontend/lib/i18n/dictionaries/zh.ts`
- Modify: `frontend/lib/i18n/dictionaries/en.ts`

**Interfaces:**
- Consumes: `useT`（Task 3）
- Produces: 字典新增 `review` 命名空间

- [ ] **Step 1: 按 Task 7 的提取方法处理五个文件**

`page.tsx` 120 处中文、`ReviewTopBar.tsx` 41 处、`Sidebar.tsx` 18 处、`StageChip.tsx` 11 处、`loading.tsx` 6 处（含注释）。全部归入 `review` 命名空间。

`loading.tsx` 与 `StageChip.tsx` 是 server component，需要 hook 的话加 `"use client"`。

- [ ] **Step 2: 确认无残留**

Run: `cd frontend && grep -nP '(?<![/*])\s*["'"'"'`>][^"'"'"'`<]*[\x{4e00}-\x{9fff}]' "app/review/[id]/page.tsx" "app/review/[id]/loading.tsx" components/review/ReviewTopBar.tsx components/review/Sidebar.tsx components/review/StageChip.tsx`
Expected: 只剩注释行

- [ ] **Step 3: 构建验证**

Run: `cd frontend && pnpm exec tsc --noEmit && pnpm test && pnpm build`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add frontend/app/review frontend/components/review frontend/lib/i18n
git commit -m "feat(i18n): translate review page shell and top bar"
```

---

### Task 11: 评审结果面板

**Files:**
- Modify: `frontend/components/review/SummaryCard.tsx`
- Modify: `frontend/components/review/RisksList.tsx`
- Modify: `frontend/components/review/InlineSuggestion.tsx`
- Modify: `frontend/components/review/AdoptContext.tsx`
- Modify: `frontend/components/review/AdoptCountChip.tsx`
- Modify: `frontend/components/SuggestionList.tsx`
- Modify: `frontend/lib/i18n/dictionaries/zh.ts`
- Modify: `frontend/lib/i18n/dictionaries/en.ts`

**Interfaces:**
- Consumes: `useT`（Task 3）
- Produces: 字典 `review` 命名空间新增条目

`InlineSuggestion.tsx`（114 处）与 `AdoptContext.tsx`（72 处）是本任务重头。采纳计数一类文案写成插值函数，例如 `adoptedCount: (n: number) => \`已采纳 ${n} 条\`` / `` (n) => `${n} adopted` ``。

- [ ] **Step 1: 按 Task 7 的提取方法处理六个文件**

- [ ] **Step 2: 确认无残留**

Run: `cd frontend && grep -nP '(?<![/*])\s*["'"'"'`>][^"'"'"'`<]*[\x{4e00}-\x{9fff}]' components/review/SummaryCard.tsx components/review/RisksList.tsx components/review/InlineSuggestion.tsx components/review/AdoptContext.tsx components/review/AdoptCountChip.tsx components/SuggestionList.tsx`
Expected: 只剩注释行

- [ ] **Step 3: 构建验证**

Run: `cd frontend && pnpm exec tsc --noEmit && pnpm test && pnpm build`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add frontend/components frontend/lib/i18n
git commit -m "feat(i18n): translate summary, risks and inline suggestion panels"
```

---

### Task 12: Agent 会话与 diff 视图

**Files:**
- Modify: `frontend/components/review/AgentSessionView.tsx`
- Modify: `frontend/components/review/AgentPanel.tsx`
- Modify: `frontend/components/review/SessionList.tsx`
- Modify: `frontend/components/review/DiffView.tsx`
- Modify: `frontend/components/review/FileDiff.tsx`
- Modify: `frontend/lib/i18n/dictionaries/zh.ts`
- Modify: `frontend/lib/i18n/dictionaries/en.ts`

**Interfaces:**
- Consumes: `useT`（Task 3）
- Produces: 字典新增 `agent` 与 `diff` 两个命名空间

`AgentSessionView.tsx` 有 273 处中文，是全项目最多的单文件，但其中大部分是注释。只提取用户可见串。

- [ ] **Step 1: 按 Task 7 的提取方法处理五个文件**

- [ ] **Step 2: 确认无残留**

Run: `cd frontend && grep -nP '(?<![/*])\s*["'"'"'`>][^"'"'"'`<]*[\x{4e00}-\x{9fff}]' components/review/AgentSessionView.tsx components/review/AgentPanel.tsx components/review/SessionList.tsx components/review/DiffView.tsx components/review/FileDiff.tsx`
Expected: 只剩注释行

- [ ] **Step 3: 构建验证**

Run: `cd frontend && pnpm exec tsc --noEmit && pnpm test && pnpm build`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add frontend/components/review frontend/lib/i18n
git commit -m "feat(i18n): translate agent session and diff views"
```

---

### Task 13: 数据层与通用组件的用户可见串

**Files:**
- Modify: `frontend/lib/errors.ts`
- Modify: `frontend/lib/format.ts`
- Modify: `frontend/lib/perms.ts`
- Modify: `frontend/lib/reviews.ts`
- Modify: `frontend/lib/types.ts`
- Modify: `frontend/lib/sse.ts`
- Modify: `frontend/lib/notifications.ts`
- Modify: `frontend/components/ui/Toast.tsx`
- Modify: `frontend/components/ui/badge.tsx`
- Modify: `frontend/components/ui/ci-status.tsx`
- Modify: `frontend/components/ui/file-status-badge.tsx`
- Modify: `frontend/components/ui/spinner.tsx`
- Modify: `frontend/components/ui/button.tsx`
- Modify: `frontend/components/ui/avatar.tsx`
- Modify: `frontend/app/error.tsx`
- Modify: `frontend/app/not-found.tsx`
- Modify: `frontend/lib/i18n/dictionaries/zh.ts`
- Modify: `frontend/lib/i18n/dictionaries/en.ts`

**Interfaces:**
- Consumes: `Dict`（Task 2）、`useT`（Task 3）
- Produces: `friendlyError(raw: string, t: Dict): string`（签名新增第二参数）；字典新增 `errors`、`ui`、`notifications` 命名空间

`lib/` 下的模块不是组件，用不了 hook。规则：**这些函数改为接收字典作为参数**，由调用方（组件）从 `useT()` 取好传进去。不要在 `lib/` 里读 cookie 或造第二套 locale 解析。

`friendlyError` 的目标形态：

```ts
import type { Dict } from "./i18n/dictionaries/zh";

// Maps raw backend / network / timeout messages onto actionable copy.
// The dictionary is passed in because lib modules cannot call React hooks.
export function friendlyError(raw: string, t: Dict): string {
  const m = raw.trim();
  if (!m) return t.errors.generic;

  if (m === "sse idle timeout" || m.includes("idle timeout")) return t.errors.sseTimeout;
  if (m === "Failed to fetch" || m.includes("NetworkError") || m === "Load failed") {
    return t.errors.network;
  }
  if (
    m.includes("invalid GitHub PR URL") ||
    m === "invalid request body" ||
    m === "url is required"
  ) {
    return t.errors.invalidPrUrl;
  }
  if (m.includes("fetch upstream failed")) return t.errors.fetchUpstream;
  return m;
}
```

原注释中"后端已中文化的 403/404 原样透出"这条在 PR 3 加 `code` 后由 Task 21 收口；本任务只改签名，保持最后一行 `return m` 不变。

- [ ] **Step 1: 改 friendlyError 签名并更新全部调用点**

Run: `cd frontend && grep -rn "friendlyError" app components lib`
逐个调用点改为 `friendlyError(raw, t)`，其中 `t` 来自组件内的 `useT()`。

- [ ] **Step 2: 按同样规则处理 format.ts / perms.ts / reviews.ts / types.ts / sse.ts / notifications.ts**

凡是返回用户可见字符串的导出函数，都加一个 `t: Dict` 参数。纯数据结构（如 `types.ts` 的 interface）只需把中文注释改英文。

- [ ] **Step 3: 处理 ui 组件与错误页**

`components/ui/*` 与 `app/error.tsx`、`app/not-found.tsx` 按 Task 7 的提取方法处理。`app/not-found.tsx` 是 server component，加 `"use client"`。

- [ ] **Step 4: 确认无残留**

Run: `cd frontend && grep -nP '(?<![/*])\s*["'"'"'`>][^"'"'"'`<]*[\x{4e00}-\x{9fff}]' lib/*.ts components/ui/*.tsx app/error.tsx app/not-found.tsx`
Expected: 只剩注释行

- [ ] **Step 5: 构建验证**

Run: `cd frontend && pnpm exec tsc --noEmit && pnpm test && pnpm build`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add frontend/lib frontend/components/ui frontend/app
git commit -m "feat(i18n): pass dictionaries into lib helpers and translate shared UI"
```

---

### Task 14: PR 2 浏览器验证与开 PR

**Files:** 无代码改动

- [ ] **Step 1: 起 dev server 并跑通一条真实评审**

`preview_start {name: "frontend"}`，在首页粘 `https://github.com/ecstasoy/LGTM/pull/93` 并提交，等评审跑完。

- [ ] **Step 2: 中英各走查一遍评审页**

在评审页切到 EN，`read_page` 确认顶栏、侧栏、摘要卡、风险列表、行内建议、Agent 面板的界面文案全为英文（评审正文此时仍是中文，属预期——PR 3/4 才接后端）。`read_console_messages` 确认无报错。

- [ ] **Step 3: 截图存档**

`computer {action: "screenshot"}` 各存一张中英文评审页。

- [ ] **Step 4: 开 PR**

```bash
git push -u origin feat/i18n-review-workspace
```

PR 标题：`feat(i18n): translate the review workspace`

---

# PR 3 — 后端 locale 链路

分支：`feat/i18n-backend`（PR 2 合并后从 `main` 切出）

### Task 15: 后端 locale 原语与配置

**Files:**
- Create: `backend/internal/i18n/locale.go`
- Create: `backend/internal/i18n/locale_test.go`
- Modify: `backend/internal/config/config.go`

**Interfaces:**
- Consumes: 无
- Produces: `i18n.Locale`（string 底层类型）、`i18n.ZH`、`i18n.EN`、`i18n.Normalize(v string, def Locale) Locale`、`i18n.FromAcceptLanguage(header string, def Locale) Locale`、`config.Config.DefaultLocale string`

- [ ] **Step 1: 写失败的测试**

创建 `backend/internal/i18n/locale_test.go`：

```go
package i18n_test

import (
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/i18n"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		def  i18n.Locale
		want i18n.Locale
	}{
		{"zh", i18n.ZH, i18n.ZH},
		{"en", i18n.ZH, i18n.EN},
		{"EN", i18n.ZH, i18n.EN},
		{"fr", i18n.ZH, i18n.ZH},
		{"", i18n.ZH, i18n.ZH},
		{"", i18n.EN, i18n.EN},
	}
	for _, c := range cases {
		if got := i18n.Normalize(c.in, c.def); got != c.want {
			t.Errorf("Normalize(%q, %q) = %q, want %q", c.in, c.def, got, c.want)
		}
	}
}

func TestFromAcceptLanguage(t *testing.T) {
	cases := []struct {
		header string
		want   i18n.Locale
	}{
		{"zh-CN,zh;q=0.9,en;q=0.8", i18n.ZH},
		{"en-US,en;q=0.9", i18n.EN},
		{"en;q=0.8,zh-CN;q=0.9", i18n.ZH}, // q weight wins over source order
		{"EN-GB", i18n.EN},
		{"fr-FR,de;q=0.9", i18n.ZH},
		{"", i18n.ZH},
		{"en;q=abc,zh", i18n.ZH},
	}
	for _, c := range cases {
		if got := i18n.FromAcceptLanguage(c.header, i18n.ZH); got != c.want {
			t.Errorf("FromAcceptLanguage(%q) = %q, want %q", c.header, got, c.want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test ./internal/i18n/...`
Expected: FAIL —— 包不存在

- [ ] **Step 3: 实现 locale.go**

创建 `backend/internal/i18n/locale.go`：

```go
// Package i18n resolves the review output locale. It is the Go counterpart of frontend/lib/i18n/locale.ts.
// Naming note: "locale" means UI / review language here. The unrelated notion of a PR's programming
// language lives in internal/api/lang.go.
package i18n

import (
	"sort"
	"strconv"
	"strings"
)

type Locale string

const (
	ZH Locale = "zh"
	EN Locale = "en"
)

// Normalize maps arbitrary input onto a supported locale, falling back to def rather than erroring.
func Normalize(v string, def Locale) Locale {
	switch Locale(strings.ToLower(strings.TrimSpace(v))) {
	case ZH:
		return ZH
	case EN:
		return EN
	default:
		return def
	}
}

// FromAcceptLanguage returns the highest-q supported tag in the header, or def.
func FromAcceptLanguage(header string, def Locale) Locale {
	if strings.TrimSpace(header) == "" {
		return def
	}

	type weighted struct {
		tag    string
		weight float64
	}
	var tags []weighted
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag := strings.ToLower(strings.TrimSpace(fields[0]))
		if tag == "" {
			continue
		}
		weight := 1.0
		for _, p := range fields[1:] {
			p = strings.TrimSpace(p)
			if !strings.HasPrefix(p, "q=") {
				continue
			}
			// A malformed q is treated as lowest priority rather than an error.
			if q, err := strconv.ParseFloat(strings.TrimPrefix(p, "q="), 64); err == nil {
				weight = q
			} else {
				weight = 0
			}
		}
		tags = append(tags, weighted{tag: tag, weight: weight})
	}
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].weight > tags[j].weight })

	for _, t := range tags {
		switch {
		case t.tag == "zh" || strings.HasPrefix(t.tag, "zh-"):
			return ZH
		case t.tag == "en" || strings.HasPrefix(t.tag, "en-"):
			return EN
		}
	}
	return def
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd backend && go test ./internal/i18n/...`
Expected: PASS

- [ ] **Step 5: 加配置项**

在 `backend/internal/config/config.go` 的 `Config` 结构体中加：

```go
	// DefaultLocale is the single fallback for review output language: the last tier of the API's
	// body > Accept-Language > default chain, and the value used by the webhook path, which has no user request.
	DefaultLocale string `env:"LGTM_DEFAULT_LOCALE" envDefault:"zh"`
```

- [ ] **Step 6: 提交**

```bash
git add backend/internal/i18n backend/internal/config/config.go
git commit -m "feat(i18n): add backend locale primitives and LGTM_DEFAULT_LOCALE"
```

---

### Task 16: prompt 分语言

**Files:**
- Rename: `backend/internal/prompts/summary.tmpl` → `summary.zh.tmpl`
- Rename: `backend/internal/prompts/risks.tmpl` → `risks.zh.tmpl`
- Rename: `backend/internal/prompts/suggestions.tmpl` → `suggestions.zh.tmpl`
- Create: `backend/internal/prompts/summary.en.tmpl`
- Create: `backend/internal/prompts/risks.en.tmpl`
- Create: `backend/internal/prompts/suggestions.en.tmpl`
- Modify: `backend/internal/prompts/embed.go`
- Modify: `backend/internal/prompts/prompts_test.go`

**Interfaces:**
- Consumes: `i18n.Locale`（Task 15）
- Produces: `prompts.ParseFor(stage string, locale i18n.Locale) (*template.Template, error)`

- [ ] **Step 1: 写失败的测试**

在 `backend/internal/prompts/prompts_test.go` 中追加：

```go
func TestParseForCoversEveryStageAndLocale(t *testing.T) {
	for _, stage := range []string{"summary", "risks", "suggestions"} {
		for _, locale := range []i18n.Locale{i18n.ZH, i18n.EN} {
			if _, err := prompts.ParseFor(stage, locale); err != nil {
				t.Errorf("ParseFor(%q, %q): %v", stage, locale, err)
			}
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test ./internal/prompts/...`
Expected: FAIL —— `ParseFor` 未定义

- [ ] **Step 3: 重命名中文模板并实现 ParseFor**

```bash
cd backend/internal/prompts
git mv summary.tmpl summary.zh.tmpl
git mv risks.tmpl risks.zh.tmpl
git mv suggestions.tmpl suggestions.zh.tmpl
```

在 `embed.go` 中加：

```go
// ParseFor compiles the template for one stage in one locale, e.g. ("summary", EN) -> summary.en.tmpl.
// Callers must normalize the locale first; unknown values simply miss the embed FS and error out.
func ParseFor(stage string, locale i18n.Locale) (*template.Template, error) {
	return Parse(fmt.Sprintf("%s.%s.tmpl", stage, locale))
}
```

- [ ] **Step 4: 写三份原生英文 prompt**

逐个打开 `*.zh.tmpl`，把每条指令用地道英文重写为 `*.en.tmpl`。**不要机器直译**——尤其注意中文里"≤ 80 字"这类计量单位在英文中没有对应物，应改写为 "under 40 words"。JSON schema 的字段名、`{{...}}` 模板变量、Markdown 结构必须与中文版逐一对应，只有自然语言指令换语言。

改完后逐对比对，确认两个版本的 `{{ }}` 变量集合完全一致：

Run: `cd backend/internal/prompts && for s in summary risks suggestions; do echo "== $s =="; diff <(grep -o '{{[^}]*}}' $s.zh.tmpl | sort -u) <(grep -o '{{[^}]*}}' $s.en.tmpl | sort -u); done`
Expected: 三组 diff 均为空

- [ ] **Step 5: 跑测试确认通过**

Run: `cd backend && go test ./internal/prompts/...`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add backend/internal/prompts
git commit -m "feat(i18n): split review prompts into per-locale templates"
```

---

### Task 17: stage 与 agent 的 System 串

**Files:**
- Modify: `backend/internal/review/summary.go`
- Modify: `backend/internal/review/risks.go`
- Modify: `backend/internal/review/suggestions.go`
- Modify: `backend/internal/review/orchestrator.go`
- Modify: `backend/internal/api/steer.go`

**Interfaces:**
- Consumes: `i18n.Locale`（Task 15）、`prompts.ParseFor`（Task 16）
- Produces: 三个 stage 结构体新增 `Locale i18n.Locale` 字段；`Orchestrator` 新增 `Locale i18n.Locale` 字段

- [ ] **Step 1: 把硬编码 System 串换成按 locale 取值**

`summary.go:35` 的 `System: "你是一位 code reviewer，回答请使用中文 Markdown。"` 改为从包级 map 取：

```go
// systemByLocale carries the persona and the output-language instruction; the stage template carries the task.
var systemByLocale = map[i18n.Locale]string{
	i18n.ZH: "你是一位 code reviewer，回答请使用中文 Markdown。",
	i18n.EN: "You are a code reviewer. Answer in English Markdown.",
}
```

`risks.go:43` 与 `suggestions.go:52` 的 `"你是一位 code reviewer，仅按要求输出严格 JSON。"` 同法处理，英文版为 `"You are a code reviewer. Emit strict JSON exactly as specified, nothing else."`。三个文件各自持有自己的 map，不要提到公共包——它们的措辞会分别演进。

`api/steer.go:202` 的 agent system prompt 同法处理。

- [ ] **Step 2: 三个 stage 换用 ParseFor**

把 `prompts.Parse("summary.tmpl")` 改为 `prompts.ParseFor("summary", s.Locale)`，risks / suggestions 同理。三个 stage 结构体各加一个 `Locale i18n.Locale` 字段。

- [ ] **Step 3: Orchestrator 透传 locale**

`Orchestrator` 加 `Locale i18n.Locale` 字段，构造各 stage 时传入。

- [ ] **Step 4: 编译并跑测试**

Run: `cd backend && go build ./... && go test ./...`
Expected: PASS。若已有 stage 测试因新字段为零值而挂，在测试中显式设 `Locale: i18n.ZH` 保持原行为

- [ ] **Step 5: 提交**

```bash
git add backend/internal/review backend/internal/api/steer.go
git commit -m "feat(i18n): select stage and agent system prompts by locale"
```

---

### Task 18: reviews 表加 locale 并纳入唯一键

**Files:**
- Modify: `backend/internal/store/schema.sql`
- Create: `backend/internal/store/indexes.sql`
- Modify: `backend/internal/store/postgres_schema.sql`
- Modify: `backend/internal/store/store.go`
- Modify: `backend/internal/store/sqlite.go`
- Modify: `backend/internal/store/postgres.go`
- Modify: `backend/internal/store/sqlite_test.go`

**Interfaces:**
- Consumes: 无
- Produces: `store.Record.Locale string`；`Store.Get(ctx context.Context, owner, repo string, pr int, headSHA, locale string) (*Record, error)`（签名新增末位参数）

这是本 PR 风险最高的一步：不改唯一键的话，英语用户会静默命中中文缓存，且英文版写不进去。

`schema.sql` 每次启动重放（`CREATE TABLE IF NOT EXISTS`），而 SQLite 没有 `ADD COLUMN IF NOT EXISTS`，所以补列不能写在 `schema.sql` 里。改为三段式启动：建表 → 补列 → 建索引。

- [ ] **Step 1: 写失败的测试**

在 `backend/internal/store/sqlite_test.go` 中追加：

```go
func TestZhAndEnReviewsCoexist(t *testing.T) {
	s, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	ctx := context.Background()

	zh := &store.Record{
		ID: "rec-zh", Owner: "o", Repo: "r", PRNumber: 1, HeadSHA: "sha",
		Locale: "zh", Payload: json.RawMessage(`{"summary":"zh"}`), CreatedAt: time.Now(),
	}
	en := &store.Record{
		ID: "rec-en", Owner: "o", Repo: "r", PRNumber: 1, HeadSHA: "sha",
		Locale: "en", Payload: json.RawMessage(`{"summary":"en"}`), CreatedAt: time.Now(),
	}
	if err := s.Put(ctx, zh); err != nil {
		t.Fatalf("put zh: %v", err)
	}
	if err := s.Put(ctx, en); err != nil {
		t.Fatalf("put en: %v", err)
	}

	got, err := s.Get(ctx, "o", "r", 1, "sha", "en")
	if err != nil {
		t.Fatalf("get en: %v", err)
	}
	if got == nil || got.ID != "rec-en" {
		t.Fatalf("get en returned %+v, want rec-en", got)
	}

	got, err = s.Get(ctx, "o", "r", 1, "sha", "zh")
	if err != nil {
		t.Fatalf("get zh: %v", err)
	}
	if got == nil || got.ID != "rec-zh" {
		t.Fatalf("get zh returned %+v, want rec-zh", got)
	}
}

func TestSchemaApplyIsIdempotentOnLegacyDatabases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	// Build a pre-i18n database: reviews without a locale column.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE reviews (
		id TEXT PRIMARY KEY, user_id TEXT, owner TEXT NOT NULL, repo TEXT NOT NULL,
		pr_number INTEGER NOT NULL, head_sha TEXT NOT NULL, payload BLOB NOT NULL,
		created_at INTEGER NOT NULL)`)
	if err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Opening twice must both migrate cleanly and stay idempotent.
	for i := range 2 {
		s, err := store.NewSQLiteStore(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if err := s.Put(context.Background(), &store.Record{
			ID: fmt.Sprintf("rec-%d", i), Owner: "o", Repo: "r", PRNumber: 1,
			HeadSHA: fmt.Sprintf("sha-%d", i), Locale: "en",
			Payload: json.RawMessage(`{}`), CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
}
```

按需补 `database/sql`、`encoding/json`、`fmt`、`path/filepath`、`time` 的 import，driver import 与文件中现有写法保持一致。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test ./internal/store/...`
Expected: FAIL —— `Record` 无 `Locale` 字段、`Get` 参数个数不符

- [ ] **Step 3: 拆 schema，索引单独成文件**

从 `schema.sql` 中删掉三个 `CREATE INDEX` 语句，并给 `reviews` 加列：

```sql
-- v1 schema; adding users / comments tables in v2 will not break this.

CREATE TABLE IF NOT EXISTS reviews (
    id          TEXT PRIMARY KEY,
    user_id     TEXT,                       -- nullable in v1; filled in after OAuth in v2
    owner       TEXT NOT NULL,
    repo        TEXT NOT NULL,
    pr_number   INTEGER NOT NULL,
    head_sha    TEXT NOT NULL,
    locale      TEXT NOT NULL DEFAULT 'zh', -- review output language, part of the cache identity
    payload     BLOB NOT NULL,              -- serialized review result bytes
    created_at  INTEGER NOT NULL            -- Unix timestamp (nanoseconds)
);
```

创建 `backend/internal/store/indexes.sql`。索引 DDL 必须在补列之后执行，因此单独成文件：

```sql
-- Applied after the locale column is guaranteed to exist.
-- locale is part of both unique keys so a zh review and an en review of the same PR can coexist.
DROP INDEX IF EXISTS idx_reviews_public_unique;
DROP INDEX IF EXISTS idx_reviews_user_unique;

CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_public_unique
    ON reviews(owner, repo, pr_number, head_sha, locale)
    WHERE user_id IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_reviews_user_unique
    ON reviews(user_id, owner, repo, pr_number, head_sha, locale)
    WHERE user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_reviews_user
    ON reviews(user_id, created_at DESC);
```

`postgres_schema.sql` 做同样的拆分与加列；Postgres 支持 `ADD COLUMN IF NOT EXISTS`，可直接写在建表脚本之后。

- [ ] **Step 4: SQLite 三段式启动**

在 `sqlite.go` 中加 embed 与迁移函数：

```go
//go:embed indexes.sql
var indexesSQL string

// ensureLocaleColumn backfills reviews.locale on databases created before i18n.
// SQLite has no ADD COLUMN IF NOT EXISTS, so probe the table first.
func ensureLocaleColumn(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(reviews)")
	if err != nil {
		return fmt.Errorf("inspect reviews: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notNull    int
			dfltValue  sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &primaryKey); err != nil {
			return fmt.Errorf("scan reviews column: %w", err)
		}
		if name == "locale" {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, "ALTER TABLE reviews ADD COLUMN locale TEXT NOT NULL DEFAULT 'zh'")
	if err != nil {
		return fmt.Errorf("add reviews.locale: %w", err)
	}
	return nil
}
```

在 `NewSQLiteStore` 中，把原来单次 `ExecContext(ctx, schemaSQL)` 改为顺序三步：`schemaSQL` → `ensureLocaleColumn` → `indexesSQL`。顺序不能变：索引引用了 locale 列。

- [ ] **Step 5: 改 Record 与 Get 签名**

`store.go` 的 `Record` 加 `Locale string`，`Store` 接口的 `Get` 加末位 `locale string` 参数并更新注释。`sqlite.go` / `postgres.go` 的 `Get` / `Put` / `List` / `GetByID` 的 SQL 语句加上 `locale` 列。

- [ ] **Step 6: 跑测试确认通过**

Run: `cd backend && go test ./internal/store/...`
Expected: PASS

- [ ] **Step 7: 全量编译与测试**

Run: `cd backend && go build ./... && go test ./...`
Expected: 编译报错指出所有 `Store.Get` 调用点（主要在 `internal/api/review.go`），逐个补上 locale 实参后 PASS

- [ ] **Step 8: 提交**

```bash
git add backend/internal/store
git commit -m "feat(i18n): make locale part of the review cache identity"
```

---

### Task 19: API locale 协商与错误 code

**Files:**
- Create: `backend/internal/api/errcode.go`
- Modify: `backend/internal/api/review.go`
- Modify: `backend/internal/api/steer.go`
- Modify: `backend/internal/api/commit.go`
- Modify: `backend/internal/api/comment.go`
- Modify: `backend/internal/api/perms.go`
- Modify: `backend/internal/api/auth.go`
- Modify: `backend/internal/api/webhook.go`
- Modify: `backend/internal/api/reviews.go`（计划修订，Task 14 验收发现）
- Modify: `backend/internal/api/review_test.go`

**Interfaces:**
- Consumes: `i18n.Normalize`、`i18n.FromAcceptLanguage`（Task 15）；`config.Config.DefaultLocale`（Task 15）
- Produces: `api.resolveLocale(c *gin.Context, bodyLocale string, def i18n.Locale) i18n.Locale`；`errcode.go` 中的 code 常量

- [ ] **Step 1: 写失败的测试**

在 `backend/internal/api/review_test.go` 中追加对 `resolveLocale` 的表驱动测试，覆盖三级回退：

```go
func TestResolveLocalePrefersBodyThenHeaderThenDefault(t *testing.T) {
	cases := []struct {
		name       string
		bodyLocale string
		header     string
		def        i18n.Locale
		want       i18n.Locale
	}{
		{"body wins over header", "en", "zh-CN,zh;q=0.9", i18n.ZH, i18n.EN},
		{"header used when body empty", "", "en-US,en;q=0.9", i18n.ZH, i18n.EN},
		{"default used when both empty", "", "", i18n.ZH, i18n.ZH},
		{"unknown body value falls through to header", "fr", "en-US", i18n.ZH, i18n.EN},
		{"unknown body and header fall to default", "fr", "de-DE", i18n.EN, i18n.EN},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/reviews", nil)
			if c.header != "" {
				req.Header.Set("Accept-Language", c.header)
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = req

			if got := resolveLocale(ctx, c.bodyLocale, c.def); got != c.want {
				t.Errorf("resolveLocale = %q, want %q", got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test ./internal/api/... -run TestResolveLocale`
Expected: FAIL —— `resolveLocale` 未定义

- [ ] **Step 3: 实现 resolveLocale**

在 `review.go` 中加：

```go
// resolveLocale walks the request body > Accept-Language > configured default chain.
// An unrecognized value at any tier falls through to the next rather than erroring.
func resolveLocale(c *gin.Context, bodyLocale string, def i18n.Locale) i18n.Locale {
	if l := i18n.Normalize(bodyLocale, ""); l != "" {
		return l
	}
	return i18n.FromAcceptLanguage(c.GetHeader("Accept-Language"), def)
}
```

`i18n.Normalize` 传空 def 表示"没认出来",由调用方继续往下走。

- [ ] **Step 4: 请求体加 locale 字段并接线**

`review.go:106` 的匿名请求体结构加 `Locale string \`json:"locale"\``。把解析出的 locale 传给 `Orchestrator`、`Store.Get`、`Store.Put`（写入 `Record.Locale`）。per-stage / SSE 变体与 `steer.go` 同法处理。

`webhook.go` 无用户请求，直接用 `i18n.Normalize(cfg.DefaultLocale, i18n.ZH)`。

- [ ] **Step 5: 把 agent 完成消息改成结构化帧（计划修订，Task 10 评审发现）**

`internal/api/steer.go:253` 现在发的是格式化中文散文：

```go
fmt.Sprintf("Agent 完成（%d 步）：%s", result.Steps, result.Output)
```

而前端 `app/review/[id]/page.tsx:554` 用正则 `/^Agent 完成（\d+ 步）：(.*)$/s` 去**解析这段中文**取出正文。一旦本任务把这条消息翻成英文，正则永久失配：`InfoBanner` 落进 `!m` 分支，"Agent reply" 标题消失，agent 回复里的 Markdown（代码块、列表）退化成纯文本原样显示。不崩溃，但静默降级，且类型检查和测试都抓不到。

不要把正则改成同时匹配中英文——那是把耦合又加固一层。改成结构化帧，一次性拆掉耦合：

```go
writeSSE(c.Writer, "agent_reply", map[string]any{
    "steps":  result.Steps,
    "output": result.Output,
})
```

前端据此渲染，标题从自己的字典取（`t.review.agentReplyLabel` 已存在），`output` 直接喂给 ReactMarkdown。**前端那一半在 Task 21 做**——本任务只改后端并保留旧的 `info` 帧一并发送，这样 PR 3 单独合并时前端仍然工作；Task 21 接上新帧后再由它删掉旧帧。

- [ ] **Step 6: 加错误 code**

创建 `backend/internal/api/errcode.go`：

```go
package api

// Stable machine-readable error codes. The frontend renders copy from its own dictionary keyed by these;
// the human-readable "error" field stays as-is for curl and non-browser consumers.
const (
	CodeNotLoggedIn        = "not_logged_in"
	CodeOAuthNotConfigured = "oauth_not_configured"
	CodeUnknownModel       = "unknown_model"
	CodePRNotFound         = "pr_not_found"
	CodeGitHubForbidden    = "github_forbidden"
	CodeNoPushPermission   = "no_push_permission"
	CodeNoCommentPermission = "no_comment_permission"
	CodeSuggestionNoAnchor = "suggestion_missing_anchor"
	CodeSuggestionNoPatch  = "suggestion_missing_patch"
	CodeEmptyPR            = "empty_pr"
	CodeHistoryLoginRequired = "history_login_required"
	CodeNotReviewOwner       = "not_review_owner"
)
```

`reviews.go` 是 Task 14 浏览器验收补进来的：未登录访问 history 时，英文界面上会渲染出 `Failed to load: 请先登录后查看评审历史`——外壳翻了，插值进去的后端串没翻。它不属于「LLM 正文保持原语言」那条豁免，是通用鉴权错误。

给每一处返回中文文案的错误响应加上对应 `code` 字段，`error` 字段保持原值不动。逐个映射：

| 位置 | code |
| --- | --- |
| `auth.go:44` | `CodeOAuthNotConfigured` |
| `commit.go:34`、`comment.go:41`、`comment.go:135` | `CodeNotLoggedIn` |
| `commit.go:85`、`comment.go:93` | `CodeSuggestionNoAnchor` |
| `commit.go:89` | `CodeSuggestionNoPatch` |
| `commit.go:101` | `CodeNoPushPermission` |
| `comment.go:105` | `CodeNoCommentPermission` |
| `review.go:137` | `CodeUnknownModel` |
| `review.go:154` | `CodePRNotFound` |
| `review.go:156` | `CodeGitHubForbidden` |
| `review.go:176` | `CodeEmptyPR` |
| `reviews.go:109` | `CodeHistoryLoginRequired` |
| `reviews.go:178` | `CodeNotLoggedIn` |
| `reviews.go:193` | `CodeNotReviewOwner` |

`perms.go` 的 `Reason` 字段同理加一个并列的 `ReasonCode` 字段，取值用同一批常量。

- [ ] **Step 7: 跑测试确认通过**

Run: `cd backend && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add backend/internal/api
git commit -m "feat(i18n): negotiate request locale and emit a structured agent-reply frame"
```

---

### Task 20: PR 3 端到端手工验证与开 PR

**Files:** 无代码改动

- [ ] **Step 1: 起后端，用中文请求跑一条评审**

```bash
cd backend && go run ./cmd/server
```

另开一个终端：

```bash
curl -N -X POST localhost:8080/api/reviews -H 'Content-Type: application/json' -d '{"url":"https://github.com/ecstasoy/LGTM/pull/93","locale":"zh"}'
```

Expected: SSE 流中摘要为中文

- [ ] **Step 2: 同一条 PR 用英文请求**

```bash
curl -N -X POST localhost:8080/api/reviews -H 'Content-Type: application/json' -d '{"url":"https://github.com/ecstasoy/LGTM/pull/93","locale":"en"}'
```

Expected: 走完整 LLM 流程（**没有**命中中文缓存），摘要为英文。这一条直接验证唯一键改动是否真的生效——若返回中文，说明 locale 没进缓存键

- [ ] **Step 3: 再跑一次英文请求确认缓存命中**

重复 Step 2 的命令。
Expected: 秒回英文结果（命中缓存）

- [ ] **Step 4: 验证 Accept-Language 回退**

```bash
curl -N -X POST localhost:8080/api/reviews -H 'Content-Type: application/json' -H 'Accept-Language: en-US,en;q=0.9' -d '{"url":"https://github.com/ecstasoy/LGTM/pull/93"}'
```

Expected: 英文结果

- [ ] **Step 5: 开 PR**

```bash
git push -u origin feat/i18n-backend
```

PR 标题：`feat(i18n): generate reviews in the requested locale`

---

# PR 4 — 端到端接通

分支：`feat/i18n-wire-up`（PR 3 合并后从 `main` 切出）

> 若本分支的 base 指向 `feat/i18n-backend` 而非 `main`，**合并前必须先 retarget 到 main**，否则提交会被孤立。

### Task 21: 前端提交携带 locale，错误按 code 查字典

**Files:**
- Modify: `frontend/lib/api.ts`
- Modify: `frontend/lib/errors.ts`
- Modify: `frontend/app/(main)/page.tsx`
- Modify: `frontend/lib/i18n/dictionaries/zh.ts`
- Modify: `frontend/lib/i18n/dictionaries/en.ts`

**Interfaces:**
- Consumes: `useLocale`（Task 3）、`friendlyError(raw, t)`（Task 13）、后端 `code` 常量与 `agent_reply` 帧（Task 19）
- Produces: `friendlyError(raw: string, t: Dict, code?: string): string`（新增可选第三参数）；字典新增 `errors.byCode: Record<string, string>`

**另需完成（计划修订，Task 10 评审发现）：** 消费 Task 19 新增的 `agent_reply` 结构化 SSE 帧，删掉 `app/review/[id]/page.tsx:554` 那个解析中文散文的正则 `/^Agent 完成（\d+ 步）：(.*)$/s`，并让 Task 19 里为兼容而保留的旧 `info` 帧一并下线。标题用已有的 `t.review.agentReplyLabel`，`output` 直接交给 ReactMarkdown。做完后前端不再依赖任何后端产出的自然语言文本的形状。

- [ ] **Step 1: 字典加 code 映射**

在 `zh.ts` 的 `errors` 命名空间下加：

```ts
    byCode: {
      not_logged_in: "请先登录",
      oauth_not_configured: "GitHub OAuth 未配置",
      unknown_model: "未知模型",
      pr_not_found: "PR 不存在或为私有仓库",
      github_forbidden: "GitHub 拒绝访问（速率限制或权限不足）",
      no_push_permission: "无 push 权限（需 write / admin）",
      no_comment_permission: "无评论权限（需 triage / write / admin）",
      suggestion_missing_anchor: "该建议缺少文件或行号，无法定位到 PR diff",
      suggestion_missing_patch: "该建议没有可提交的改动，请改用「评论」按钮发纯文字建议",
      empty_pr: "该 PR 无可评审的文件改动",
    } as Record<string, string>,
```

`en.ts` 对应：

```ts
    byCode: {
      not_logged_in: "Sign in first",
      oauth_not_configured: "GitHub OAuth is not configured",
      unknown_model: "Unknown model",
      pr_not_found: "PR not found, or the repository is private",
      github_forbidden: "GitHub denied the request (rate limited or insufficient permissions)",
      no_push_permission: "No push permission (write or admin required)",
      no_comment_permission: "No comment permission (triage, write, or admin required)",
      suggestion_missing_anchor: "This suggestion has no file or line, so it cannot be anchored to the PR diff",
      suggestion_missing_patch: "This suggestion has no committable change — use the Comment button to post it as plain text",
      empty_pr: "This PR has no reviewable file changes",
    } as Record<string, string>,
```

`as Record<string, string>` 是必要的：否则 `typeof zh` 会把 key 集合锁死成字面量联合，后端加新 code 时前端要同步改两个文件才能编译。

- [ ] **Step 2: friendlyError 优先按 code 取文案**

```ts
export function friendlyError(raw: string, t: Dict, code?: string): string {
  if (code) {
    const byCode = t.errors.byCode[code];
    if (byCode) return byCode;
  }
  const m = raw.trim();
  if (!m) return t.errors.generic;
  // ... 其余分支保持 Task 13 的形态不变
  return m;
}
```

同时把原文件顶部"后端已中文化的 403/404 原样透出"那句注释改写为英文，说明 code 路径优先、无 code 时才回退到原始串。

- [ ] **Step 3: api.ts 解析并透出 code**

`lib/api.ts` 中解析错误响应的地方，把 `code` 字段一并读出并沿调用链传到 `friendlyError` 的第三参数。

- [ ] **Step 4: 提交评审时带上 locale**

`lib/api.ts` 中发起评审的函数增加 `locale: Locale` 参数，写进请求体：

```ts
    body: JSON.stringify({ url, model, stage_models: stageModels, locale }),
```

`app/(main)/page.tsx` 调用处用 `const locale = useLocale();` 取值传入。

- [ ] **Step 5: 构建验证**

Run: `cd frontend && pnpm exec tsc --noEmit && pnpm test && pnpm build`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add frontend/lib frontend/app
git commit -m "feat(i18n): send the active locale with review requests and localize error codes"
```

---

### Task 22: 评审语言回流与跨语言提示

**Files:**
- Modify: `backend/internal/api/review.go`
- Modify: `frontend/lib/types.ts`
- Modify: `frontend/app/review/[id]/page.tsx`
- Modify: `frontend/app/(main)/history/page.tsx`
- Modify: `frontend/lib/i18n/dictionaries/zh.ts`
- Modify: `frontend/lib/i18n/dictionaries/en.ts`

**Interfaces:**
- Consumes: `useLocale`、`useT`（Task 3）；`store.Record.Locale`（Task 18）
- Produces: `cachedPayload` 新增 `locale` 字段；前端 review 类型新增 `locale?: "zh" | "en"`

- [ ] **Step 1: 后端把 locale 写进 payload**

`review.go` 中的 `cachedPayload` 结构加 `Locale string \`json:"locale"\``，落库时填入本次请求解析出的 locale，`replayCached` 回放时一并送出。历史记录里没有该字段的旧行反序列化后为空串，前端据此当作"未知"处理、不显示提示。

- [ ] **Step 2: 前端类型加字段**

`lib/types.ts` 中评审结果类型加 `locale?: "zh" | "en";`。

- [ ] **Step 3: 字典加提示文案**

`zh.ts` 的 `review` 命名空间加：

```ts
    generatedInOtherLocale: (language: string) => `本评审生成于${language}，正文保持原样。`,
    languageNameZh: "中文",
    languageNameEn: "英文",
```

`en.ts`：

```ts
    generatedInOtherLocale: (language: string) =>
      `This review was generated in ${language}; the text is shown as written.`,
    languageNameZh: "Chinese",
    languageNameEn: "English",
```

- [ ] **Step 4: 评审页渲染提示条**

在 `app/review/[id]/page.tsx` 摘要卡上方加：

```tsx
  const locale = useLocale();
  const reviewLocale = result?.locale;
  const showLocaleNotice = reviewLocale != null && reviewLocale !== "" && reviewLocale !== locale;
```

```tsx
      {showLocaleNotice ? (
        <p className="mb-3 rounded-md border border-border bg-surface-2 px-3 py-2 text-xs text-muted">
          {t.review.generatedInOtherLocale(
            reviewLocale === "zh" ? t.review.languageNameZh : t.review.languageNameEn,
          )}
        </p>
      ) : null}
```

- [ ] **Step 5: history 列表同法标注**

在 history 列表项上，当条目 locale 与当前 UI locale 不同时，加一个小 badge 显示 `ZH` 或 `EN`。

- [ ] **Step 6: 构建与测试**

Run: `cd frontend && pnpm exec tsc --noEmit && pnpm test && pnpm build`
Run: `cd backend && go build ./... && go test ./...`
Expected: 均 PASS

- [ ] **Step 7: 提交**

```bash
git add backend/internal/api/review.go frontend
git commit -m "feat(i18n): surface the language a review was generated in"
```

---

### Task 23: 全链路双语走查与开 PR

**Files:** 无代码改动

- [ ] **Step 1: 同时起前后端**

后端 `cd backend && go run ./cmd/server`；前端用 `preview_start {name: "frontend"}`。

- [ ] **Step 2: 中文全流程**

保持中文，提交 `https://github.com/ecstasoy/LGTM/pull/93`，等评审完成。`read_page` 确认界面与评审正文均为中文。

- [ ] **Step 3: 英文全流程**

切到 EN，提交同一条 PR。确认走了完整 LLM 流程且**摘要 / 风险 / 建议正文均为英文**。这是整个项目的验收点。

- [ ] **Step 4: 跨语言提示验证**

保持 EN，从 history 打开 Step 2 生成的中文评审。确认正文仍是中文，且顶部出现 "This review was generated in Chinese" 提示条。

- [ ] **Step 5: 错误路径验证**

在 EN 下提交一个不存在的 PR（如 `https://github.com/ecstasoy/LGTM/pull/999999`）。
Expected: 报错显示英文 "PR not found, or the repository is private"，而非中文

- [ ] **Step 6: 截图存档**

`computer {action: "screenshot"}` 存：英文评审页、跨语言提示条、英文错误态。

- [ ] **Step 7: 开 PR**

```bash
git push -u origin feat/i18n-wire-up
```

PR 标题：`feat(i18n): deliver English reviews end to end`

若 base 不是 `main`，先执行：

```bash
gh pr edit --base main
```

---

## 自查记录

**Spec 覆盖核对**

| Spec 章节 | 对应任务 |
| --- | --- |
| cookie 存储、SSR 协商 | Task 1、4 |
| 文件结构（`lib/i18n/*`） | Task 1、2、3、5 |
| `t.nav.history` 取词形态 | Task 2、3 |
| 切换写 cookie + state | Task 3、5 |
| 评审缓存唯一键含 locale | Task 18 |
| schema 加列 + 幂等 ALTER | Task 18 |
| prompt 双文件分语言 | Task 16 |
| 四处 System 串 | Task 17 |
| locale 三级回退 | Task 19 |
| `LGTM_DEFAULT_LOCALE` | Task 15、19 |
| webhook 用默认 locale | Task 19 |
| 错误响应加 code、前端查字典 | Task 19、21 |
| `Result.locale` 回流 + 提示条 | Task 22 |
| 前端类型约束即 CI 门禁 | Task 2 Step 3、CI 接线于 Task 1 Step 7 |
| 后端四组 Go 测试 | Task 16（ParseFor）、18（共存 + 幂等）、19（三级回退） |
| 不断言 LLM 输出语言 | Task 20、23 手工验证 |
| PR 拆分与 base retarget | 四个 PR 分节 + Task 23 Step 7 |

**类型一致性核对**：`Locale` / `LOCALE_COOKIE` / `DEFAULT_LOCALE` / `isLocale` / `negotiate`（Task 1 定义，Task 3、4 使用）；`Dict`（Task 2 定义，Task 3、13、21 使用）；`useT` / `useLocale` / `useI18n`（Task 3 定义，Task 5–13、21、22 使用）；`i18n.Locale` / `Normalize` / `FromAcceptLanguage`（Task 15 定义，Task 16、17、19 使用）；`prompts.ParseFor`（Task 16 定义，Task 17 使用）；`Record.Locale` / `Store.Get` 六参签名（Task 18 定义，Task 19 使用）；`friendlyError` 二参 → 三参（Task 13 → Task 21）；`STAGE_KEYS` 取代 `STAGES`（Task 7）。
