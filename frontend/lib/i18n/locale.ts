// Locale primitives shared by the server layout and the client provider; no React imports allowed here.

export type Locale = "zh" | "en";

export const LOCALE_COOKIE = "lgtm-locale";
export const DEFAULT_LOCALE: Locale = "zh";

export function isLocale(value: unknown): value is Locale {
  return value === "zh" || value === "en";
}

// negotiate returns the highest-q supported tag from an Accept-Language header, or DEFAULT_LOCALE.
export function negotiate(acceptLanguage: string | null | undefined): Locale {
  if (!acceptLanguage) return DEFAULT_LOCALE;

  const tags = acceptLanguage
    .split(",")
    .map((part) => {
      const [rawTag, ...params] = part.trim().split(";");
      const qParam = params.map((p) => p.trim()).find((p) => p.startsWith("q="));
      const parsed = qParam ? Number.parseFloat(qParam.slice(2)) : 1;
      // A malformed q is treated as lowest priority rather than an error.
      return { tag: rawTag.trim().toLowerCase(), weight: Number.isNaN(parsed) ? 0 : parsed };
    })
    .filter((t) => t.tag !== "")
    .sort((a, b) => b.weight - a.weight);

  for (const { tag } of tags) {
    if (tag === "zh" || tag.startsWith("zh-")) return "zh";
    if (tag === "en" || tag.startsWith("en-")) return "en";
  }
  return DEFAULT_LOCALE;
}
