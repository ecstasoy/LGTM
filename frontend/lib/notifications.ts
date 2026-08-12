"use client";

import { useEffect, useRef, useState } from "react";

// Notification: a row from the backend's /api/notifications; matches the shape of api.Notification.
export interface Notification {
  id: string;
  review_id: string;
  owner: string;
  repo: string;
  pr: number;
  title?: string;
  source: string; // "webhook"
  created_at: string;
}

// useNotifications polls for new webhook notifications.
//
// First tick: a silent baseline — just records the latest entry's ID in sinceRef, does NOT
// push it into newOnes or pop a toast.
// Later ticks: only fetch entries since that baseline; genuinely new arrivals pop a toast.
//
// Purpose: refreshing the page / reopening the tab shouldn't dump the last 7 days of backlog
// into an avalanche of toasts.
// Trade-off: notifications that arrive while the user is away aren't shown on return (unless
// sinceRef is persisted to localStorage, which this version doesn't do).
//
// Only polls once the user is signed in (relies on the cookie; the backend returns empty when signed out).
// Could move to SSE/WebSocket push later; 15s polling is fine for a demo.
export function useNotifications(intervalMs = 15000): {
  newOnes: Notification[];
  consume: () => void;
} {
  const [newOnes, setNewOnes] = useState<Notification[]>([]);
  const sinceRef = useRef<string | null>(null);
  const initialBaselineRef = useRef<boolean>(true); // marks the first tick, which only records the baseline
  const intervalRef = useRef<number | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function tick() {
      try {
        const q = sinceRef.current
          ? `?since=${encodeURIComponent(sinceRef.current)}`
          : "";
        const res = await fetch("/api/notifications" + q, {
          credentials: "include",
        });
        if (!res.ok) return;
        const list = (await res.json()) as Notification[];
        if (cancelled) return;

        // First tick: just record the baseline, no toast (avoids the refresh avalanche).
        if (initialBaselineRef.current) {
          initialBaselineRef.current = false;
          if (list.length > 0) {
            sinceRef.current = list[0].id;
          }
          return;
        }

        if (list.length === 0) return;
        // The backend returns the list newest-first; [0] is the latest.
        sinceRef.current = list[0].id;
        setNewOnes((prev) => [...list, ...prev]);
      } catch {
        // Network down / not signed in returns 200 empty; fail silently.
      }
    }

    // Tick once immediately to get the baseline (pops no toasts).
    void tick();
    intervalRef.current = window.setInterval(tick, intervalMs);

    return () => {
      cancelled = true;
      if (intervalRef.current !== null) {
        window.clearInterval(intervalRef.current);
      }
    };
  }, [intervalMs]);

  function consume() {
    setNewOnes([]);
  }

  return { newOnes, consume };
}
