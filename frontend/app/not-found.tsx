"use client";

import Link from "next/link";
import { Compass } from "lucide-react";

import { useT } from "@/lib/i18n/context";

// app/not-found.tsx: the global 404.
// Triggered by visiting a route that doesn't exist, or any Server Component calling notFound().
export default function NotFound() {
  const t = useT();
  return (
    <section className="mx-auto max-w-[480px] px-6 py-16 text-center">
      <div className="mb-4 inline-flex h-12 w-12 items-center justify-center rounded-full bg-surface-2 text-muted">
        <Compass className="h-6 w-6" />
      </div>
      <h1 className="text-lg font-semibold">{t.errors.notFoundTitle}</h1>
      <p className="mt-2 text-sm text-muted">
        {t.errors.notFoundBody}
      </p>
      <div className="mt-5 flex justify-center gap-2.5">
        <Link
          href="/"
          className="rounded-md bg-accent px-3.5 py-1.5 text-sm font-medium text-accent-fg hover:opacity-90"
        >
          {t.errors.backToHome}
        </Link>
        <Link
          href="/history"
          className="rounded-md border border-border-strong bg-surface px-3.5 py-1.5 text-sm text-text-2 hover:bg-surface-hover hover:text-text"
        >
          {t.errors.browseHistory}
        </Link>
      </div>
    </section>
  );
}
