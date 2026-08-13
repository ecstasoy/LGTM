import { describe, expect, it } from "vitest";

import { shouldShowLocaleNotice } from "./review-locale";

describe("shouldShowLocaleNotice", () => {
  // The backward-compatibility case that matters most: a review persisted before the locale field
  // existed has no "locale" key in its stored payload at all, so the frontend receives undefined —
  // never a fabricated "zh" — regardless of what the store's reviews.locale column says. That must
  // read as "unknown", not "this review is Chinese", no matter the current UI locale.
  it("shows no notice for a review with no recorded locale (pre-i18n record), in either UI locale", () => {
    expect(shouldShowLocaleNotice(undefined, "en")).toBe(false);
    expect(shouldShowLocaleNotice(undefined, "zh")).toBe(false);
  });

  it("shows no notice when the review's locale matches the current UI locale", () => {
    expect(shouldShowLocaleNotice("zh", "zh")).toBe(false);
    expect(shouldShowLocaleNotice("en", "en")).toBe(false);
  });

  it("shows the notice when the review's locale differs from the current UI locale", () => {
    expect(shouldShowLocaleNotice("zh", "en")).toBe(true);
    expect(shouldShowLocaleNotice("en", "zh")).toBe(true);
  });
});
