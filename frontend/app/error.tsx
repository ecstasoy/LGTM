"use client";

import { useEffect } from "react";
import Link from "next/link";
import { AlertTriangle } from "lucide-react";

import { useT } from "@/lib/i18n/context";

// app/error.tsx: the App Router's route-level error boundary.
// Catches any uncaught throw / promise rejection from a Client Component;
// does not replace the review page's own stage-level error banner — that has its own handling above this boundary.
export default function GlobalError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  const t = useT();

  useEffect(() => {
    // eslint-disable-next-line no-console
    console.error("Unhandled UI error", error);
  }, [error]);

  return (
    <section className="mx-auto max-w-[480px] px-6 py-16 text-center">
      <div className="mb-4 inline-flex h-12 w-12 items-center justify-center rounded-full bg-high-bg text-high">
        <AlertTriangle className="h-6 w-6" />
      </div>
      <h1 className="text-lg font-semibold">{t.errors.pageErrorTitle}</h1>
      <p className="mt-2 text-sm text-muted">
        {error.message || t.errors.pageErrorUnknown}
        {error.digest ? (
          <span className="ml-2 font-mono text-faint">[{error.digest}]</span>
        ) : null}
      </p>
      <div className="mt-5 flex justify-center gap-2.5">
        <button
          type="button"
          onClick={() => reset()}
          className="rounded-md bg-accent px-3.5 py-1.5 text-sm font-medium text-accent-fg hover:opacity-90"
        >
          {t.review.retry}
        </button>
        <Link
          href="/"
          className="rounded-md border border-border-strong bg-surface px-3.5 py-1.5 text-sm text-text-2 hover:bg-surface-hover hover:text-text"
        >
          {t.errors.backToHome}
        </Link>
      </div>
    </section>
  );
}
