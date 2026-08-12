"use client";

import Link from "next/link";

import { useT } from "@/lib/i18n/context";

// Footer with copyright and project link, rendered globally in main layout.
export function Footer() {
  const t = useT();
  return (
    <footer className="mt-auto border-t border-border bg-surface px-4 py-4 text-center text-[11px] text-muted">
      <span>{t.footer.copyright}</span>
      <span className="mx-2 text-faint">·</span>
      <Link
        href="https://github.com/ecstasoy/LGTM"
        target="_blank"
        rel="noreferrer"
        className="hover:text-text"
      >
        GitHub
      </Link>
    </footer>
  );
}
