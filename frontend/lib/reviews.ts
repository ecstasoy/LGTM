"use client";

import { ApiError } from "./errors";

// deleteReview: DELETE /api/reviews/:id.
// The backend rejects by ownership: 401 (not_logged_in) if unauthenticated, 403 (not_review_owner)
// if not the owner — both carry a stable `code` (see backend/internal/api/reviews.go), read out here
// so callers can resolve it through friendlyError instead of rendering the raw backend string.
export async function deleteReview(id: string): Promise<void> {
  const res = await fetch(`/api/reviews/${encodeURIComponent(id)}`, {
    method: "DELETE",
    credentials: "include",
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string; code?: string };
    throw new ApiError(data.error || `HTTP ${res.status}`, data.code);
  }
}
