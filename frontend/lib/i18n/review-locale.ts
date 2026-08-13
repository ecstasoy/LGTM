// shouldShowLocaleNotice is the one shared rule behind the review-detail notice and the history
// ZH/EN badge: true only when reviewLocale is known and differs (by code, never by display name)
// from uiLocale — undefined (streaming, still loading, or a pre-i18n record with no locale in its
// payload, regardless of the store column's "zh" backfill) always reads as unknown, never as "zh".
export function shouldShowLocaleNotice(
  reviewLocale: "zh" | "en" | undefined,
  uiLocale: "zh" | "en",
): reviewLocale is "zh" | "en" {
  return reviewLocale != null && reviewLocale !== uiLocale;
}
