import type { Dict } from "./i18n/dictionaries/zh";

// Maps raw backend / network / timeout messages onto actionable copy.
// The dictionary is passed in because lib modules cannot call React hooks.
export function friendlyError(raw: string, t: Dict): string {
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
