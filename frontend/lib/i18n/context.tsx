"use client";

import { createContext, useCallback, useContext, useMemo, useState } from "react";

import { LOCALE_COOKIE, type Locale } from "./locale";
import { en } from "./dictionaries/en";
import { zh, type Dict } from "./dictionaries/zh";

const DICTIONARIES: Record<Locale, Dict> = { zh, en };

interface I18nValue {
  locale: Locale;
  setLocale: (next: Locale) => void;
  t: Dict;
}

const I18nContext = createContext<I18nValue | null>(null);

// initialLocale comes from the server layout, so the first client render already matches the SSR output.
export function I18nProvider({
  initialLocale,
  children,
}: {
  initialLocale: Locale;
  children: React.ReactNode;
}) {
  const [locale, setLocaleState] = useState<Locale>(initialLocale);

  const setLocale = useCallback((next: Locale) => {
    setLocaleState(next);
    document.documentElement.lang = next === "zh" ? "zh-CN" : "en";
    // Not httpOnly: switching happens client-side and the server only reads it to pick the SSR language.
    document.cookie = `${LOCALE_COOKIE}=${next}; path=/; max-age=31536000; samesite=lax`;
  }, []);

  const value = useMemo<I18nValue>(
    () => ({ locale, setLocale, t: DICTIONARIES[locale] }),
    [locale, setLocale],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nValue {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useI18n must be called inside I18nProvider");
  return ctx;
}

export function useT(): Dict {
  return useI18n().t;
}

export function useLocale(): Locale {
  return useI18n().locale;
}
