"use client";

import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n/context";

export type CIStatusValue = "passing" | "failing" | "pending";

const dotClass: Record<CIStatusValue, string> = {
  passing: "bg-ok",
  failing: "bg-fail",
  pending: "bg-pending animate-pulse-dot",
};

// CIStatus: a dot (8px by default) with an optional label; pending gets a pulse animation.
// Renders nothing for an invalid/empty status — callers decide the fallback, rather than
// rendering a misleading "unknown" state.
export function CIStatus({
  status,
  withLabel = false,
  className,
}: {
  status: CIStatusValue | string;
  withLabel?: boolean;
  className?: string;
}) {
  const t = useT();
  if (status !== "passing" && status !== "failing" && status !== "pending") {
    return null;
  }
  const labelText: Record<CIStatusValue, string> = {
    passing: t.ui.ciPassingLabel,
    failing: t.ui.ciFailingLabel,
    pending: t.ui.ciPendingLabel,
  };
  return (
    <span
      className={cn("inline-flex items-center gap-1.5", className)}
      aria-label={t.ui.ciStatusAriaLabel(labelText[status])}
    >
      <span className={cn("h-2 w-2 rounded-full", dotClass[status])} aria-hidden />
      {withLabel ? (
        <span className="text-xs text-muted">{labelText[status]}</span>
      ) : null}
    </span>
  );
}
