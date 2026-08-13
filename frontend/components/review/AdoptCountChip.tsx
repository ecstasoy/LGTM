"use client";

import { CheckCircle2 } from "lucide-react";

import { useAdopt } from "./AdoptContext";
import { useT } from "@/lib/i18n/context";

// AdoptCountChip 顶栏 chip：「采纳 N/M 项」
// N = 本地已发过 PR 的建议数；M = 总建议数
// 持久化在 localStorage 按 reviewId 隔离，刷新页面保留
// 没采纳时不渲染，避免空 chip 占位
export function AdoptCountChip() {
  const t = useT();
  const adopt = useAdopt();
  if (!adopt || !adopt.reviewId) return null;
  const n = adopt.adoptedIdxs.size;
  if (n === 0) return null;
  const total = adopt.suggestions.length;
  return (
    <span
      className="inline-flex h-[22px] shrink-0 items-center gap-1 rounded-full border border-ok-bg bg-ok-bg px-2 text-[11px] font-medium text-ok"
      title={t.review.adoptedChipTitle(n, total)}
    >
      <CheckCircle2 className="h-3 w-3" />
      {t.review.adoptedChipLabel(n, total)}
    </span>
  );
}
