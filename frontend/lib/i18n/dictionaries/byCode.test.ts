import { describe, expect, it } from "vitest";

import { en } from "./en";
import { zh } from "./zh";

// errors.byCode is annotated `as Record<string, string>`, which puts it outside the `Dict = typeof zh`
// guard: unlike every other namespace, a key added to one dictionary alone still compiles. That is the
// namespace a backend change extends (one entry per backend/internal/api/errcode.go constant), and the
// author of that change may never open en.ts — so the missing entry surfaces only as friendlyError
// falling back to generic copy at runtime. This test is the guard TypeScript can't be.
describe("errors.byCode", () => {
  it("has the same key set in both dictionaries", () => {
    expect(Object.keys(en.errors.byCode).sort()).toEqual(Object.keys(zh.errors.byCode).sort());
  });

  it("has a non-empty string for every key in both dictionaries", () => {
    for (const [code, text] of [...Object.entries(zh.errors.byCode), ...Object.entries(en.errors.byCode)]) {
      expect(text, code).toBeTruthy();
    }
  });
});
