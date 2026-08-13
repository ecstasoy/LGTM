"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { History as HistoryIcon, Search, Trash2 } from "lucide-react";

import { listReviews } from "@/lib/api";
import { ApiError, friendlyError } from "@/lib/errors";
import type { ReviewSummary } from "@/lib/types";
import { cn } from "@/lib/utils";
import { useMe } from "@/lib/auth";
import { deleteReview } from "@/lib/reviews";
import { CIStatus, type CIStatusValue } from "@/components/ui/ci-status";
import { RiskPips } from "@/components/landing/RiskPips";
import { useLocale, useT } from "@/lib/i18n/context";
import { shouldShowLocaleNotice } from "@/lib/i18n/review-locale";
import type { Dict } from "@/lib/i18n/dictionaries/zh";

const ZERO_COUNTS = { high: 0, medium: 0, low: 0 } as const;

// HistoryPage: dense history table.
// 6-column grid mirrors the design prototype History.jsx: CI / repo+PR / title / risk / SHA / time.
// Toolbar: search box (matches repo+title substring) + language filter segmented control
// (aggregated dynamically from the current items' lang field).
// Clicking a row navigates to /review/[id], hitting cache for an instant load.
export default function HistoryPage() {
  const t = useT();
  const [items, setItems] = useState<ReviewSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [lang, setLang] = useState<string>("all");
  const [nonce, setNonce] = useState(0);
  const { me } = useMe();
  const myLogin = me?.authenticated ? me.login : undefined;

  useEffect(() => {
    let cancelled = false;
    // Fetch up to maxListLimit=100 and filter locally; list size stays manageable at this scale.
    listReviews(100)
      .then((d) => {
        if (!cancelled) setItems(d);
      })
      .catch((e) => {
        if (!cancelled) {
          setError(
            friendlyError(
              e instanceof Error ? e.message : String(e),
              t,
              e instanceof ApiError ? e.code : undefined,
            ),
          );
        }
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `t` intentionally omitted: this is a
    // one-shot fetch (resolves almost instantly), not a long-lived stream, so staleness risk from a
    // mid-flight locale toggle is negligible; re-adding it would refetch on every locale switch.
  }, [nonce]);

  async function handleDelete(id: string, label: string) {
    if (!window.confirm(t.history.confirmDelete(label))) return;
    try {
      await deleteReview(id);
      setNonce((n) => n + 1);
    } catch (e) {
      window.alert(
        t.history.deleteFailed(
          friendlyError(
            e instanceof Error ? e.message : String(e),
            t,
            e instanceof ApiError ? e.code : undefined,
          ),
        ),
      );
    }
  }

  // langs segmented-control values = ["all", ...every distinct non-empty lang in the current result set]
  const langs = useMemo<string[]>(() => {
    if (!items) return ["all"];
    const set = new Set<string>();
    for (const r of items) {
      if (r.lang) set.add(r.lang);
    }
    return ["all", ...Array.from(set).sort()];
  }, [items]);

  const rows = useMemo<ReviewSummary[]>(() => {
    if (!items) return [];
    const q = query.trim().toLowerCase();
    return items.filter((r) => {
      if (lang !== "all" && r.lang !== lang) return false;
      if (q === "") return true;
      const hay = `${r.owner}/${r.repo} ${r.title ?? ""}`.toLowerCase();
      return hay.includes(q);
    });
  }, [items, query, lang]);

  return (
    <section className="space-y-5">
      <header className="flex items-center gap-3">
        <HistoryIcon className="h-5 w-5 text-muted" />
        <h1 className="m-0 text-[22px] font-semibold tracking-[-0.01em]">{t.history.title}</h1>
        <span className="font-mono text-xs text-faint">
          {t.history.countLabel(items?.length ?? 0)}
        </span>
      </header>

      <div className="flex flex-wrap items-center gap-2.5">
        <div className="flex h-[34px] min-w-[220px] flex-1 items-center gap-2 rounded-md border border-border-strong bg-surface px-2.5">
          <Search className="h-[15px] w-[15px] text-muted" aria-hidden />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t.history.searchPlaceholder}
            className="min-w-0 flex-1 border-none bg-transparent text-sm text-text outline-none placeholder:text-muted"
          />
        </div>
        <div className="flex gap-[3px] rounded-md border border-border bg-surface p-[3px]">
          {langs.map((l) => {
            const active = lang === l;
            return (
              <button
                key={l}
                type="button"
                onClick={() => setLang(l)}
                className={cn(
                  "rounded-sm px-2.5 py-[5px] font-mono text-xs transition-colors",
                  active
                    ? "bg-surface-hover text-text"
                    : "text-muted hover:text-text",
                )}
              >
                {l}
              </button>
            );
          })}
        </div>
      </div>

      <div className="overflow-hidden rounded-lg border border-border bg-surface">
        <HeaderRow t={t} />
        <TableBody rows={rows} items={items} error={error} myLogin={myLogin} onDelete={handleDelete} t={t} />
      </div>
    </section>
  );
}

const GRID_COLS = "grid-cols-[28px_160px_1fr_130px_90px_70px]";

function HeaderRow({ t }: { t: Dict }) {
  return (
    <div
      className={cn(
        "grid items-center gap-3 border-b border-border bg-surface-2 px-4 py-2.5",
        "text-[10.5px] font-semibold uppercase tracking-wider text-muted",
        GRID_COLS,
      )}
    >
      <span>CI</span>
      <span>{t.history.colRepo}</span>
      <span>{t.history.colTitle}</span>
      <span>{t.history.colRisk}</span>
      <span>SHA</span>
      <span className="text-right">{t.history.colTime}</span>
    </div>
  );
}

function TableBody({
  rows,
  items,
  error,
  myLogin,
  onDelete,
  t,
}: {
  rows: ReviewSummary[];
  items: ReviewSummary[] | null;
  error: string | null;
  myLogin?: string;
  onDelete: (id: string, label: string) => void;
  t: Dict;
}) {
  if (error) {
    return <p className="px-4 py-6 text-center text-sm text-fail">{t.history.loadFailed(error)}</p>;
  }
  if (items === null) {
    return <p className="px-4 py-6 text-center text-sm text-muted">{t.history.loading}</p>;
  }
  if (items.length === 0) {
    return (
      <p className="px-4 py-8 text-center text-sm text-muted">
        {t.history.empty}
      </p>
    );
  }
  if (rows.length === 0) {
    return <p className="px-4 py-8 text-center text-sm text-muted">{t.history.noMatches}</p>;
  }
  return (
    <>
      {rows.map((r, i) => (
        <Row
          key={r.id}
          review={r}
          isFirst={i === 0}
          myLogin={myLogin}
          onDelete={onDelete}
          t={t}
        />
      ))}
    </>
  );
}

function Row({
  review,
  isFirst,
  myLogin,
  onDelete,
  t,
}: {
  review: ReviewSummary;
  isFirst: boolean;
  myLogin?: string;
  onDelete: (id: string, label: string) => void;
  t: Dict;
}) {
  // Delete button visibility: signed in + (I'm the owner OR it's a legacy anonymous record).
  const canDelete = !!myLogin && (!review.created_by || review.created_by === myLogin);
  // Badge visibility: only when this review's stored locale is known AND differs from the current
  // UI locale. Absent (pre-i18n record) never badges — see cachedPayload.Locale's comment.
  const uiLocale = useLocale();
  return (
    <div
      className={cn(
        "group relative flex items-center transition-colors hover:bg-surface-hover",
        isFirst ? "" : "border-t border-border",
      )}
    >
      <Link
        href={`/review/${review.id}`}
        className={cn(
          "grid flex-1 items-center gap-3 px-4 py-3",
          GRID_COLS,
        )}
      >
        <CIStatus status={(review.ci ?? "pending") as CIStatusValue} />
        <code className="truncate font-mono text-xs text-text-2">
          {review.owner}/{review.repo}
          <span className="text-faint">#{review.pr}</span>
        </code>
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-sm">{review.title || t.history.untitled}</span>
          {shouldShowLocaleNotice(review.locale, uiLocale) ? (
            <LocaleBadge locale={review.locale} />
          ) : null}
        </span>
        <RiskPips counts={review.risk_counts ?? ZERO_COUNTS} />
        <code className="font-mono text-xs text-faint">{review.head_sha.slice(0, 7)}</code>
        <span className="text-right text-xs text-faint">{formatRelative(review.created_at, t)}</span>
      </Link>
      {canDelete ? (
        <button
          type="button"
          onClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
            onDelete(review.id, `${review.owner}/${review.repo}#${review.pr}`);
          }}
          title={review.created_by ? t.history.deleteOwnTitle : t.history.deleteAnonymousTitle}
          className="mr-2 inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted opacity-0 transition-opacity hover:bg-high-bg hover:text-high group-hover:opacity-100"
          aria-label={t.history.deleteAriaLabel}
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      ) : null}
    </div>
  );
}

// LocaleBadge: marks a history row whose stored review locale differs from the current UI locale.
// Renders the locale *code* (ZH / EN), not a translated language name — matching the code, not a
// display string, is what the row's `review.locale !== uiLocale` comparison above already does.
function LocaleBadge({ locale }: { locale: "zh" | "en" }) {
  return (
    <span className="shrink-0 rounded-sm border border-border-strong bg-surface-2 px-1 py-[1px] font-mono text-[9px] font-semibold uppercase tracking-wider text-muted">
      {locale}
    </span>
  );
}

// formatRelative: just now / N minutes ago / N hours ago / N days ago / MM-DD
function formatRelative(iso: string, t: Dict): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const delta = (Date.now() - d.getTime()) / 1000;
  if (delta < 60) return t.history.justNow;
  if (delta < 3600) return t.history.minutesAgo(Math.floor(delta / 60));
  if (delta < 86400) return t.history.hoursAgo(Math.floor(delta / 3600));
  if (delta < 7 * 86400) return t.history.daysAgo(Math.floor(delta / 86400));
  return d.toLocaleDateString(t.history.dateLocale, { month: "2-digit", day: "2-digit" });
}
