// shouldShowLocaleNotice decides whether a review's stored language should be called out to the
// user — the review-detail language notice and the history-table ZH/EN badge (Task 22) both defer
// to this one rule, so "is this review cross-locale" lives in exactly one place.
//
// reviewLocale is undefined for three distinct situations that must all read as "unknown, say
// nothing": a still-streaming review (freshly generated in uiLocale by construction, so it can
// never disagree), a cached detail that hasn't loaded yet, and — the case that matters most — a
// record written before this field existed. Such a pre-i18n row has no "locale" key in its stored
// payload JSON at all (see backend/internal/api/cache.go's cachedPayload.Locale), even though the
// store's reviews.locale *column* was backfilled to "zh" for every old row. That column never
// reaches the frontend; only the payload does. A false positive here — claiming a review is
// Chinese when we simply don't know — would be worse than showing no notice.
//
// The comparison is on locale *codes* ("zh" | "en"), never a displayed language name.
export function shouldShowLocaleNotice(
  reviewLocale: "zh" | "en" | undefined,
  uiLocale: "zh" | "en",
): reviewLocale is "zh" | "en" {
  return reviewLocale != null && reviewLocale !== uiLocale;
}
