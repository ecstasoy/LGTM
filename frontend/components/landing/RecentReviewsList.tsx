"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { ChevronRight, History, Trash2 } from "lucide-react";

import { listReviews } from "@/lib/api";
import type { ReviewSummary } from "@/lib/types";
import { useMe } from "@/lib/auth";
import { deleteReview } from "@/lib/reviews";
import { CIStatus } from "@/components/ui/ci-status";
import { useT } from "@/lib/i18n/context";
import type { Dict } from "@/lib/i18n/dictionaries/zh";
import { RiskPips } from "./RiskPips";

// ReviewSummary 在 lib/types.ts 还没含 ci / risk_counts（A3 加了后端但 type 未跟）
// 这里临时扩字段；下个清理 PR 把 lib/types.ts 同步
interface SummaryWithCounts extends ReviewSummary {
  ci?: string;
  risk_counts?: { high: number; medium: number; low: number };
}

const ZERO_COUNTS = { high: 0, medium: 0, low: 0 };

// RecentReviewsList 拉 /api/reviews?limit=4，渲染按 design 原型 4 条紧凑列表
// 失败 / 空状态用 design 的 muted 文字处理，不抛错也不显眼
export function RecentReviewsList() {
  const t = useT();
  const [items, setItems] = useState<SummaryWithCounts[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0); // 删除后 ++ 触发重拉
  const { me } = useMe();

  useEffect(() => {
    let cancelled = false;
    listReviews(4)
      .then((d) => {
        if (!cancelled) setItems(d as SummaryWithCounts[]);
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [nonce]);

  async function handleDelete(id: string, label: string) {
    if (!window.confirm(t.recentReviews.confirmDelete(label))) return;
    try {
      await deleteReview(id);
      setNonce((n) => n + 1);
    } catch (e) {
      window.alert(t.recentReviews.deleteFailed(e instanceof Error ? e.message : String(e)));
    }
  }

  const myLogin = me?.authenticated ? me.login : undefined;

  return (
    <section className="mt-11">
      <div className="mb-3 flex items-center">
        <History className="mr-[7px] h-[15px] w-[15px] text-muted" aria-hidden />
        <span className="text-sm font-semibold">{t.recentReviews.title}</span>
        <Link
          href="/history"
          className="ml-auto inline-flex items-center gap-1 text-xs text-muted hover:text-text"
        >
          {t.recentReviews.viewAll} <ChevronRight className="h-3 w-3" />
        </Link>
      </div>

      <div className="overflow-hidden rounded-lg border border-border bg-surface">
        <ListBody items={items} error={error} myLogin={myLogin} onDelete={handleDelete} t={t} />
      </div>
    </section>
  );
}

function ListBody({
  items,
  error,
  myLogin,
  onDelete,
  t,
}: {
  items: SummaryWithCounts[] | null;
  error: string | null;
  myLogin?: string;
  onDelete: (id: string, label: string) => void;
  t: Dict;
}) {
  if (error) {
    return <EmptyText>{t.recentReviews.loadFailed(error)}</EmptyText>;
  }
  if (items === null) {
    return <EmptyText>{t.recentReviews.loading}</EmptyText>;
  }
  if (items.length === 0) {
    return <EmptyText>{t.recentReviews.empty}</EmptyText>;
  }
  return (
    <>
      {items.map((item, i) => (
        <RecentRow
          key={item.id}
          item={item}
          isFirst={i === 0}
          myLogin={myLogin}
          onDelete={onDelete}
          t={t}
        />
      ))}
    </>
  );
}

function RecentRow({
  item,
  isFirst,
  myLogin,
  onDelete,
  t,
}: {
  item: SummaryWithCounts;
  isFirst: boolean;
  myLogin?: string;
  onDelete: (id: string, label: string) => void;
  t: Dict;
}) {
  // 删除按钮可见性：已登录 + (我是 owner OR 匿名遗留)
  // 匿名遗留（created_by 空）兼容 v1 旧记录，任何登录用户都能清
  const canDelete = !!myLogin && (!item.created_by || item.created_by === myLogin);
  return (
    <div
      className={`group relative flex items-center transition-colors hover:bg-surface-hover ${
        isFirst ? "" : "border-t border-border"
      }`}
    >
      <Link
        href={`/review/${item.id}`}
        className="flex min-w-0 flex-1 items-center gap-3 px-3.5 py-2.5"
      >
        <CIStatus status={item.ci || "pending"} />
        {/* owner/repo#pr 长名（如 freeCodeCamp/freeCodeCamp）也截断，避免挤掉标题 */}
        <code
          className="max-w-[180px] shrink-0 truncate font-mono text-xs text-text-2"
          title={`${item.owner}/${item.repo}#${item.pr}`}
        >
          {item.owner}/{item.repo}#{item.pr}
        </code>
        {/* title flex-1 + min-w-0 才能让 truncate 真的截（flex 子项默认 min-width: auto 防截断）*/}
        <span className="min-w-0 flex-1 truncate text-sm text-text" title={item.title}>
          {item.title || t.recentReviews.untitled}
        </span>
        {item.source === "webhook" ? (
          <span
            className="inline-flex h-[18px] shrink-0 items-center gap-0.5 rounded-full bg-accent-soft px-1.5 text-[10px] font-medium text-accent"
            title={t.recentReviews.webhookTitle}
          >
            {t.recentReviews.webhookBadge}
          </span>
        ) : null}
        <RiskPips counts={item.risk_counts ?? ZERO_COUNTS} />
        <span className="w-14 shrink-0 text-right text-xs text-faint">
          {formatRelative(item.created_at, t)}
        </span>
      </Link>
      {canDelete ? (
        <button
          type="button"
          onClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
            onDelete(item.id, `${item.owner}/${item.repo}#${item.pr}`);
          }}
          title={item.created_by ? t.recentReviews.deleteOwnTitle : t.recentReviews.deleteAnonymousTitle}
          className="mr-2 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted opacity-0 transition-opacity hover:bg-high-bg hover:text-high group-hover:opacity-100"
          aria-label={t.recentReviews.deleteAriaLabel}
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      ) : null}
    </div>
  );
}

function EmptyText({ children }: { children: React.ReactNode }) {
  return <p className="px-4 py-6 text-center text-sm text-muted">{children}</p>;
}

// formatRelative 简化版：以"刚刚 / N 分钟前 / N 小时前 / N 天前 / 日期"显示
// 不引 dayjs / date-fns，省一个依赖
function formatRelative(iso: string, t: Dict): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const delta = (Date.now() - d.getTime()) / 1000; // seconds
  if (delta < 60) return t.recentReviews.justNow;
  if (delta < 3600) return t.recentReviews.minutesAgo(Math.floor(delta / 60));
  if (delta < 86400) return t.recentReviews.hoursAgo(Math.floor(delta / 3600));
  if (delta < 7 * 86400) return t.recentReviews.daysAgo(Math.floor(delta / 86400));
  return d.toLocaleDateString(t.recentReviews.dateLocale, { month: "2-digit", day: "2-digit" });
}
