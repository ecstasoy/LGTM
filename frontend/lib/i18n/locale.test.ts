import { describe, expect, it } from "vitest";

import { DEFAULT_LOCALE, isLocale, negotiate } from "./locale";

describe("negotiate", () => {
  it("picks zh for a Chinese-first header", () => {
    expect(negotiate("zh-CN,zh;q=0.9,en;q=0.8")).toBe("zh");
  });

  it("picks en for an English-first header", () => {
    expect(negotiate("en-US,en;q=0.9")).toBe("en");
  });

  it("orders by q weight, not by source order", () => {
    expect(negotiate("en;q=0.8,zh-CN;q=0.9")).toBe("zh");
  });

  it("is case-insensitive", () => {
    expect(negotiate("EN-GB")).toBe("en");
  });

  it("falls back to the default for unsupported languages", () => {
    expect(negotiate("fr-FR,de;q=0.9")).toBe(DEFAULT_LOCALE);
  });

  it("falls back to the default for missing or empty headers", () => {
    expect(negotiate(null)).toBe(DEFAULT_LOCALE);
    expect(negotiate("")).toBe(DEFAULT_LOCALE);
  });

  it("ignores a malformed q value instead of throwing", () => {
    expect(negotiate("en;q=abc,zh")).toBe("zh");
  });
});

describe("isLocale", () => {
  it("accepts supported values only", () => {
    expect(isLocale("zh")).toBe(true);
    expect(isLocale("en")).toBe(true);
    expect(isLocale("fr")).toBe(false);
    expect(isLocale(undefined)).toBe(false);
  });
});
