"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Check, Plus } from "lucide-react";

import { listReviews } from "@/lib/api";
import type { ReviewSummary } from "@/lib/types";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n/context";
import type { Dict } from "@/lib/i18n/dictionaries/zh";

interface Props {
  activeId?: string; // 当前评审 ULID；streaming 模式传 undefined
}

// SessionList 256px 侧栏：session 视图模式替代常规 Sidebar。
// 拉最近评审 20 条，按 created_at 切分「今天 / 更早」两组；当前评审高亮 + 左侧 accent 竖条。
// 严格对齐 design 原型 SessionList，但用 Tailwind 替代原型的内联样式。
export function SessionList({ activeId }: Props) {
  const t = useT();
  const [items, setItems] = useState<ReviewSummary[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    listReviews(20)
      .then((d) => !cancelled && setItems(d))
      .catch(() => !cancelled && setItems([])); // Silent failure: sidebar stays empty, "New review" still works
    return () => {
      cancelled = true;
    };
  }, []);

  const groups = bucketByWhen(items ?? []);

  return (
    <aside className="absolute inset-y-0 left-0 z-40 flex h-full w-[84%] max-w-xs flex-col border-r border-border bg-surface lg:static lg:z-auto lg:w-[256px] lg:max-w-none lg:shrink-0">
      <div className="border-b border-border p-2.5">
        <Link
          href="/"
          className="flex h-9 w-full items-center justify-center gap-1.5 rounded-md bg-accent text-sm font-medium text-accent-fg hover:opacity-90"
        >
          <Plus className="h-4 w-4" />
          {t.agent.newReviewLink}
        </Link>
      </div>
      <div className="flex-1 overflow-y-auto px-2 pb-3 pt-1">
        {items === null ? (
          <p className="px-2 py-4 text-xs text-faint">{t.review.loading}</p>
        ) : items.length === 0 ? (
          <p className="px-2 py-4 text-xs text-muted">{t.agent.sessionListEmptyNote}</p>
        ) : (
          groups
            .filter((g) => g.items.length > 0)
            .map((g) => (
              <div key={g.key} className="mt-2">
                <div className="px-2 py-1 text-[10.5px] font-semibold uppercase tracking-wider text-faint">
                  {g.key === "today" ? t.agent.bucketLabelToday : t.agent.bucketLabelEarlier}
                </div>
                {g.items.map((h) => (
                  <SessionRow key={h.id} item={h} active={h.id === activeId} t={t} />
                ))}
              </div>
            ))
        )}
      </div>
    </aside>
  );
}

function SessionRow({ item, active, t }: { item: ReviewSummary; active: boolean; t: Dict }) {
  return (
    <Link
      href={`/review/${item.id}?view=session`}
      className={cn(
        "relative flex gap-2 rounded-md px-2 py-2 text-left transition-colors",
        active ? "bg-surface-hover" : "hover:bg-surface-hover",
      )}
    >
      {active ? (
        <span className="absolute left-0 top-2 bottom-2 w-[2.5px] rounded-sm bg-accent" />
      ) : null}
      <span className="mt-[1px] shrink-0 text-ok">
        <Check className="h-3.5 w-3.5" strokeWidth={2.4} />
      </span>
      <span className="min-w-0 flex-1">
        <code className="block truncate font-mono text-xs text-text-2">
          {item.owner}/{item.repo}
          <span className="text-faint">#{item.pr}</span>
        </code>
        <div className="mt-px truncate text-xs text-muted">
          {item.title || t.recentReviews.untitled}
        </div>
        <div className="mt-[3px] font-mono text-[10px] text-faint">
          {t.agent.completedAtPrefix}
          {formatWhen(item.created_at, t)}
        </div>
      </span>
    </Link>
  );
}

interface Bucket {
  key: "today" | "earlier";
  items: ReviewSummary[];
}

// bucketByWhen splits by created_at into "today" / "earlier" buckets; today = same calendar day (local tz)
function bucketByWhen(items: ReviewSummary[]): Bucket[] {
  const today: ReviewSummary[] = [];
  const earlier: ReviewSummary[] = [];
  const now = new Date();
  const todayMidnight = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  for (const it of items) {
    const t = new Date(it.created_at).getTime();
    if (Number.isFinite(t) && t >= todayMidnight) {
      today.push(it);
    } else {
      earlier.push(it);
    }
  }
  return [
    { key: "today", items: today },
    { key: "earlier", items: earlier },
  ];
}

function formatWhen(iso: string, t: Dict): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const delta = (Date.now() - d.getTime()) / 1000;
  if (delta < 60) return t.recentReviews.justNow;
  if (delta < 3600) return t.recentReviews.minutesAgo(Math.floor(delta / 60));
  if (delta < 86400) return t.recentReviews.hoursAgo(Math.floor(delta / 3600));
  if (delta < 7 * 86400) return t.recentReviews.daysAgo(Math.floor(delta / 86400));
  return d.toLocaleDateString(t.recentReviews.dateLocale, { month: "2-digit", day: "2-digit" });
}
