"use client";

import { Languages } from "lucide-react";

import { useI18n } from "@/lib/i18n/context";
import { cn } from "@/lib/utils";

// Sits next to ThemeToggle. No hydration placeholder needed: the initial locale comes from the server.
export function LocaleToggle({ className }: { className?: string }) {
  const { locale, setLocale, t } = useI18n();
  const next = locale === "zh" ? "en" : "zh";

  return (
    <button
      type="button"
      onClick={() => setLocale(next)}
      aria-label={t.nav.switchLocale}
      title={t.nav.switchLocale}
      className={cn(
        "inline-flex h-8 items-center gap-1 rounded-md border border-border px-2 text-xs font-medium text-muted transition-colors hover:bg-surface-hover hover:text-text",
        className,
      )}
    >
      <Languages className="h-4 w-4" />
      {next === "en" ? "EN" : "中"}
    </button>
  );
}
