"use client";

import { CornerDownLeft, ExternalLink, GitPullRequest, SlidersHorizontal, Sparkle } from "lucide-react";

import { cn } from "@/lib/utils";
import { STAGES, type ModelOption, type StageKey } from "@/lib/api";

// 示例 chip：用本仓自己的 PR（一定能访问 + 装了 LGTM App，可演示采纳按钮）
// 早期 golang/go + fastapi/fastapi 因 token rate limit / 私权问题被移除
const EXAMPLES = [
  {
    label: "ecstasoy/PR-Review-Assistant",
    url: "https://github.com/ecstasoy/PR-Review-Assistant/pull/93",
    desc: "Go · webhook + slash command",
  },
  {
    label: "vercel/next.js",
    url: "https://github.com/vercel/next.js/pull/71402",
    desc: "Rust · turbopack",
  },
] as const;

// 校验 PR URL 形状（协议起头，owner/repo/pull/正整数编号），允许 /files 后缀和末尾斜杠
const PR_URL_REGEX = /^https:\/\/github\.com\/[^/]+\/[^/]+\/pull\/[1-9]\d*(?:\/[^\s]*)?$/;

export function isValidPrUrl(url: string): boolean {
  return PR_URL_REGEX.test(url.trim());
}

interface Props {
  value: string;
  onChange: (next: string) => void;
  onSubmit: (url: string) => void;
  disabled?: boolean;
  // L3：可选模型；≤1 项时不渲染选择器（部署未配多模型时 UI 不变）
  models?: ModelOption[];
  model?: string;
  onModelChange?: (key: string) => void;
  // L3 分阶段：perStage 开启时按阶段各选一个模型，否则用 model 统一
  perStage?: boolean;
  onTogglePerStage?: () => void;
  stageModels?: Record<StageKey, string>;
  onStageModelChange?: (stage: StageKey, key: string) => void;
}

// 下拉框统一样式（主选择器 + 分阶段小选择器共用）
const selectCls =
  "h-9 shrink-0 rounded-md border border-border bg-surface-2 px-2 text-xs text-text-2 outline-none hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-50";

// UrlInputCard URL 输入条 + 示例 chips
// 卡片样式：surface + border-strong + shadow-md（对齐原型）
export function UrlInputCard({
  value,
  onChange,
  onSubmit,
  disabled,
  models = [],
  model = "",
  onModelChange,
  perStage = false,
  onTogglePerStage,
  stageModels,
  onStageModelChange,
}: Props) {
  const valid = isValidPrUrl(value);
  const showPicker = models.length > 1;

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (valid && !disabled) onSubmit(value.trim());
  }

  return (
    <div>
      <form onSubmit={submit}>
        <div className="flex items-center gap-2 rounded-lg border border-border-strong bg-surface p-2 shadow-md">
          <GitPullRequest className="ml-1.5 h-5 w-5 shrink-0 text-muted" aria-hidden />
          <input
            type="text"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            placeholder="https://github.com/owner/repo/pull/123"
            aria-label="GitHub Pull Request URL"
            aria-invalid={value.trim() !== "" && !valid}
            spellCheck={false}
            disabled={disabled}
            className="min-w-0 flex-1 border-none bg-transparent px-0.5 py-1.5 font-mono text-sm text-text outline-none placeholder:text-faint disabled:opacity-60"
          />
          {showPicker && !perStage ? (
            <select
              value={model}
              onChange={(e) => onModelChange?.(e.target.value)}
              disabled={disabled}
              aria-label="选择评审模型"
              title="选择评审模型"
              className={selectCls}
            >
              {models.map((m) => (
                <option key={m.key} value={m.key}>
                  {m.label}
                </option>
              ))}
            </select>
          ) : null}
          <button
            type="submit"
            disabled={!valid || disabled}
            className={cn(
              "inline-flex h-10 items-center gap-2 rounded-md px-4 text-sm font-medium transition-colors",
              "bg-accent text-accent-fg hover:opacity-90",
              "disabled:cursor-not-allowed disabled:opacity-50",
            )}
          >
            <Sparkle className="h-[15px] w-[15px]" />
            开始评审
            <CornerDownLeft className="h-[13px] w-[13px] opacity-80" />
          </button>
        </div>
      </form>

      {showPicker ? (
        <div className="mt-2.5">
          <button
            type="button"
            onClick={onTogglePerStage}
            disabled={disabled}
            aria-pressed={perStage}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs transition-colors disabled:opacity-50",
              perStage ? "text-accent" : "text-muted hover:text-text-2",
            )}
          >
            <SlidersHorizontal className="h-[13px] w-[13px]" aria-hidden />
            {perStage ? "分阶段（摘要 / 风险 / 建议 各自选模型）" : "分阶段选择模型"}
          </button>
          {perStage ? (
            <div className="mt-1.5 grid grid-cols-3 gap-2 rounded-lg border border-border bg-surface-2 p-2.5">
              {STAGES.map((s) => (
                <label key={s.key} className="flex flex-col gap-1 text-xs text-muted">
                  {s.label}
                  <select
                    value={stageModels?.[s.key] ?? model}
                    onChange={(e) => onStageModelChange?.(s.key, e.target.value)}
                    disabled={disabled}
                    aria-label={`${s.label}阶段的模型`}
                    className={cn(selectCls, "w-full")}
                  >
                    {models.map((m) => (
                      <option key={m.key} value={m.key}>
                        {m.label}
                      </option>
                    ))}
                  </select>
                </label>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}

      <div className="mt-4 flex flex-wrap items-center gap-2.5">
        <span className="whitespace-nowrap text-xs text-muted">试试：</span>
        {EXAMPLES.map((ex) => (
          <button
            key={ex.url}
            type="button"
            disabled={disabled}
            onClick={() => onChange(ex.url)}
            className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface px-3 py-1 font-mono text-xs text-text-2 transition-colors hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-50"
          >
            <ExternalLink className="h-[11px] w-[11px] text-faint" aria-hidden />
            {ex.label}
            <span className="text-faint">· {ex.desc}</span>
          </button>
        ))}
      </div>
    </div>
  );
}
