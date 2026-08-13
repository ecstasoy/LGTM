"use client";

import { useEffect, useRef, useState } from "react";
import type { Dict } from "./i18n/dictionaries/zh";

// PermsResponse: shape returned by /api/perms; mirrors the backend's api.PermsResponse.
export interface PermsResponse {
  authenticated: boolean;
  permission?: string;
  can_comment: boolean;
  can_commit: boolean;
  reason?: string;
}

// usePerms fetches the current user's permissions for the given repo; skips the fetch when owner/repo is empty.
// Not cached long-term: one fetch per review-page visit is enough, and auth changes are handled
// by the parent forcing a remount.
export function usePerms(
  owner: string | undefined,
  repo: string | undefined,
  t: Dict,
): {
  perms: PermsResponse | null;
  loading: boolean;
} {
  const [perms, setPerms] = useState<PermsResponse | null>(null);
  const [loading, setLoading] = useState(false);
  // Read via ref (not a useEffect dependency) so a locale switch mid-fetch doesn't retrigger the
  // request, but the eventual error fallback still uses whichever dictionary is current when it fires.
  const tRef = useRef(t);
  tRef.current = t;

  useEffect(() => {
    if (!owner || !repo) {
      setPerms(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    const q = `?owner=${encodeURIComponent(owner)}&repo=${encodeURIComponent(repo)}`;
    fetch("/api/perms" + q, { credentials: "include" })
      .then((r) => r.json() as Promise<PermsResponse>)
      .then((data) => {
        if (!cancelled) setPerms(data);
      })
      .catch(() => {
        if (!cancelled) {
          setPerms({
            authenticated: false,
            can_comment: false,
            can_commit: false,
            reason: tRef.current.errors.permsFetchFailed,
          });
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [owner, repo]);

  return { perms, loading };
}
