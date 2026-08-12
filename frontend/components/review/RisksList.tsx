"use client";

import { AlertTriangle, ArrowUpRight } from "lucide-react";

import type { Risk } from "@/lib/types";
import { CategoryBadge, SeverityBadge, type Category } from "@/components/ui/badge";
import { useT } from "@/lib/i18n/context";

interface Props {
  risks: Risk[];
  // 点击单条 risk 跳到 Diff 视图对应行；page.tsx 的 pickRisk 实现
  // 没传则 row 不可点击（兼容历史调用）
  onPickRisk?: (risk: Risk) => void;
}

// 排序键：severity 高 → 低；同 severity 按 confidence 高 → 低
const SEV_RANK: Record<Risk["severity"], number> = { high: 0, medium: 1, low: 2 };

// RisksList 风险识别面板：标题 + 数量；按 severity → confidence 排序
// 点击 row 跳 Diff 视图 + 滚到对应行（onPickRisk 不传则纯展示）
export function RisksList({ risks, onPickRisk }: Props) {
  const t = useT();
  if (risks.length === 0) return null;

  const sorted = [...risks].sort((a, b) => {
    const sevDiff = SEV_RANK[a.severity] - SEV_RANK[b.severity];
    if (sevDiff !== 0) return sevDiff;
    return b.confidence - a.confidence;
  });

  return (
    <section className="rounded-lg border border-border bg-surface">
      <header className="flex items-center justify-between border-b border-border px-4 py-3">
        <div className="flex items-center gap-2.5">
          <span className="inline-flex h-7 w-7 items-center justify-center rounded-md bg-accent-soft text-accent">
            <AlertTriangle className="h-4 w-4" />
          </span>
          <h2 className="text-sm font-semibold">
            {t.review.risksPanelTitle} <span className="text-muted">{t.review.riskCount(sorted.length)}</span>
          </h2>
        </div>
        {onPickRisk ? <span className="text-xs text-faint">{t.review.risksClickHint}</span> : null}
      </header>
      <ul className="divide-y divide-border">
        {sorted.map((r, i) => (
          <RiskRow key={i} risk={r} onPick={onPickRisk} t={t} />
        ))}
      </ul>
    </section>
  );
}

function RiskRow({
  risk,
  onPick,
  t,
}: {
  risk: Risk;
  onPick?: (r: Risk) => void;
  t: ReturnType<typeof useT>;
}) {
  const clickable = !!onPick && risk.line != null;
  const inner = (
    <>
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <SeverityBadge severity={risk.severity} />
        <CategoryBadge category={risk.category as Category} />
        <code className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-text-2">
          {risk.file}
          {risk.line ? `:${risk.line}` : ""}
        </code>
        <span className="font-mono text-muted">conf {(risk.confidence * 100).toFixed(0)}%</span>
        {clickable ? (
          <ArrowUpRight className="ml-auto h-3 w-3 text-muted opacity-0 transition-opacity group-hover:opacity-100" />
        ) : null}
      </div>
      <p className="mt-2 text-sm leading-relaxed text-text">{risk.reason}</p>
    </>
  );
  if (clickable) {
    return (
      <li>
        <button
          type="button"
          onClick={() => onPick!(risk)}
          className="group w-full cursor-pointer px-4 py-3 text-left transition-colors hover:bg-surface-hover"
          title={t.review.riskJumpToTitle(risk.file, risk.line!)}
        >
          {inner}
        </button>
      </li>
    );
  }
  return <li className="px-4 py-3">{inner}</li>;
}
