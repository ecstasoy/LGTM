"use client";

// deleteReview: DELETE /api/reviews/:id.
// The backend rejects by ownership: 401 if unauthenticated, 403 if not the owner.
// The caller should refresh its list on success.
export async function deleteReview(id: string): Promise<void> {
  const res = await fetch(`/api/reviews/${encodeURIComponent(id)}`, {
    method: "DELETE",
    credentials: "include",
  });
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { error?: string };
    throw new Error(data.error || `HTTP ${res.status}`);
  }
}
