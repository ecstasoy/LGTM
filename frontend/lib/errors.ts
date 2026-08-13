import type { Dict } from "./i18n/dictionaries/zh";

// ApiError carries the backend's stable machine-readable `code` (see backend/internal/api/errcode.go)
// alongside the human-readable `message`, so a catch handler can pass the code through to friendlyError
// instead of pattern-matching the message text. code is undefined for failures that never reached a
// JSON error body (network errors, aborted fetches).
export class ApiError extends Error {
  code?: string;
  constructor(message: string, code?: string) {
    super(message);
    this.name = "ApiError";
    this.code = code;
  }
}

// Maps a raw backend / network / timeout message onto actionable copy.
// The `code` path is tried first (t.errors.byCode, keyed by backend/internal/api/errcode.go's
// constants); the raw-string branches below only run when no code was supplied or the code isn't
// in the dictionary yet, so network errors and not-yet-coded backend messages still resolve.
// The dictionary is passed in because lib modules cannot call React hooks.
export function friendlyError(raw: string, t: Dict, code?: string): string {
  if (code) {
    const byCode = t.errors.byCode[code];
    if (byCode) return byCode;
  }
  const m = raw.trim();
  if (!m) return t.errors.generic;

  if (m === "sse idle timeout" || m.includes("idle timeout")) return t.errors.sseTimeout;
  if (m === "Failed to fetch" || m.includes("NetworkError") || m === "Load failed") {
    return t.errors.network;
  }
  if (
    m.includes("invalid GitHub PR URL") ||
    m === "invalid request body" ||
    m === "url is required"
  ) {
    return t.errors.invalidPrUrl;
  }
  if (m.includes("fetch upstream failed")) return t.errors.fetchUpstream;
  return m;
}
