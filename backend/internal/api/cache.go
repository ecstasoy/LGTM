package api

import (
	"encoding/json"
	"net/http"
	"time"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
)

// cachedPayload is the cached content of a review.
// summary holds the accumulated full text; risks / suggestions hold the raw event data bytes from the stage,
// so replay is a straight write-back and stays decoupled from the review package's concrete types.
//
// PR meta (title/author/state/labels/refs/createdAt/stats/ci/checks) is copied from the fetcher's output at persist time,
// so the /history list + detail can rebuild everything the top bar / cards need without going back to GitHub.
// Every new field is omitempty: older cache entries lack them, and this keeps that JSON clean,
// with the frontend treating a missing field as unknown.
type cachedPayload struct {
	Title string `json:"title,omitempty"`
	// Source records how this review was triggered: "manual" for a user-pasted URL, "webhook" for a GitHub-pushed auto review
	// The frontend renders the ⚡ "auto" chip and fires the toast notification off this field
	Source     string `json:"source,omitempty"`
	Author     string `json:"author,omitempty"`
	AuthorRole string `json:"author_role,omitempty"`
	Lang       string `json:"lang,omitempty"` // the PR's primary language (majority vote over file extensions); used by the /history language filter
	// Locale is the review output language ("zh" | "en", see resolveLocale); pre-existing rows decode
	// as "" here regardless of the reviews.locale column's own "zh" backfill (store.ensureLocaleColumn) — the frontend never reads that column, only this field, and treats "" as unknown.
	Locale      string          `json:"locale,omitempty"`
	State       string          `json:"state,omitempty"`
	Labels      []string        `json:"labels,omitempty"`
	BaseRef     string          `json:"base_ref,omitempty"`
	HeadRef     string          `json:"head_ref,omitempty"`
	PRCreatedAt time.Time       `json:"pr_created_at,omitzero"` // when the PR was created on GitHub (as opposed to Record.CreatedAt, which is when the review record was created)
	Stats       gh.Stats        `json:"stats,omitzero"`
	CI          string          `json:"ci,omitempty"`
	Checks      []gh.Check      `json:"checks,omitempty"`
	Files       []gh.File       `json:"files,omitempty"` // file tree + patch the detail endpoint needs to replay the Diff view
	Summary     string          `json:"summary"`
	Risks       json.RawMessage `json:"risks"`
	Suggestions json.RawMessage `json:"suggestions"`
	// BudgetReport is the actual layered context token allocation (L1-L4); pointer + omitempty keeps the JSON clean for older cache entries without the field
	BudgetReport *budgetReportPayload `json:"budget_report,omitempty"`
}

// replayCached writes the cached content back in SSE protocol order; the caller must already have sent the first pr meta frame.
// Written by hand outside c.Stream, so the final Flush is manual.
// It deliberately sends no info / cached marker event: to the frontend, info means "short-circuit, hide sections", which would hide the cached content instead;
// the instant response is the cache signal, and a UI badge is left to a later change.
func replayCached(w http.ResponseWriter, p cachedPayload) {
	if p.BudgetReport != nil {
		writeSSE(w, "budget_report", p.BudgetReport)
	}
	if p.Summary != "" {
		// one delta frame is enough to assemble the whole summary (the frontend reducer does +=)
		writeSSE(w, "summary_delta", map[string]string{"delta": p.Summary})
	}
	if len(p.Risks) > 0 {
		writeSSERaw(w, "risks_done", p.Risks)
	}
	if len(p.Suggestions) > 0 {
		writeSSERaw(w, "suggestions_done", p.Suggestions)
	}
	writeSSE(w, "done", map[string]any{})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
