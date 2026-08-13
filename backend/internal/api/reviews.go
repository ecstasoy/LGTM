package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

const (
	defaultListLimit = 20
	maxListLimit     = 100
)

// riskCounts is the risk tally by severity; drives the red / yellow / grey pips on the landing "recent reviews" card and the history table.
type riskCounts struct {
	High   int `json:"high"`
	Medium int `json:"medium"`
	Low    int `json:"low"`
}

// reviewListItem is one /api/reviews list entry: meta + CI + risk counts only, no full payload.
// Used by the landing "recent reviews" card and the dense /history table, so it is kept as small as possible.
type reviewListItem struct {
	ID         string     `json:"id"`
	Owner      string     `json:"owner"`
	Repo       string     `json:"repo"`
	PR         int        `json:"pr"`
	HeadSHA    string     `json:"head_sha"`
	Title      string     `json:"title,omitempty"`
	CreatedAt  string     `json:"created_at"`
	CI         string     `json:"ci,omitempty"`
	Lang       string     `json:"lang,omitempty"`       // the PR's primary language (what detectPrimaryLang returned); used by the /history language filter
	Source     string     `json:"source,omitempty"`     // "manual" / "webhook"; the frontend renders the ⚡ chip from this
	CreatedBy  string     `json:"created_by,omitempty"` // GitHub login; empty = anonymous leftover; the frontend gates the delete button on it
	RiskCounts riskCounts `json:"risk_counts"`
}

// reviewDetail is /api/reviews/:id: the full cachedPayload, exposed as-is,
// for the review page top bar and the three views (the cached instant-load path no longer has to go back to GitHub for meta).
type reviewDetail struct {
	reviewListItem
	Author       string               `json:"author,omitempty"`
	AuthorRole   string               `json:"author_role,omitempty"`
	State        string               `json:"state,omitempty"`
	Labels       []string             `json:"labels,omitempty"`
	BaseRef      string               `json:"base_ref,omitempty"`
	HeadRef      string               `json:"head_ref,omitempty"`
	PRCreatedAt  time.Time            `json:"pr_created_at,omitzero"`
	Stats        gh.Stats             `json:"stats,omitzero"`
	Checks       []gh.Check           `json:"checks,omitempty"`
	Files        []gh.File            `json:"files,omitempty"` // for rendering the Diff view; the list endpoint omits this large field
	Summary      string               `json:"summary"`
	Risks        json.RawMessage      `json:"risks,omitempty"`
	Suggestions  json.RawMessage      `json:"suggestions,omitempty"`
	BudgetReport *budgetReportPayload `json:"budget_report,omitempty"`
}

// countRisksBySeverity parses the raw JSON of the risks_done event and counts by severity.
// Returns the zero value on a parse failure (tolerates malformed older cache entries).
func countRisksBySeverity(raw json.RawMessage) riskCounts {
	if len(raw) == 0 {
		return riskCounts{}
	}
	var items []struct {
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return riskCounts{}
	}
	var c riskCounts
	for _, it := range items {
		switch it.Severity {
		case "high":
			c.High++
		case "medium":
			c.Medium++
		case "low":
			c.Low++
		}
	}
	return c
}

// ListReviews GET /api/reviews?limit=N — review history, ordered by created_at DESC.
// With no Store configured it returns 503 rather than an empty 200, so the frontend can tell "no history" from "feature not enabled".
// Visibility:
// - login required (anonymous visitors get 401)
// - a logged-in user sees only the records they created
// - anonymously submitted records (UserID=nil) appear in nobody's list, but the detail stays reachable via the review URL
//
// Rationale: past v2 the review itself will not require login (anonymous reviews stay possible), but an ownerless anonymous record has no business in anything called "history"
// Fallback for dev / unit tests with no Sessions: allow anonymous access and list everything (including nil UserID), which makes local debugging easier
func ListReviews(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "history disabled: store not configured"})
			return
		}
		// login required: the list carries PR title / repo / risk content and should expose nobody's submissions to an anonymous visitor
		s := CurrentSession(c)
		if d.Sessions != nil && s == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录后查看评审历史", "code": CodeHistoryLoginRequired})
			return
		}
		limit := parseLimit(c.Query("limit"))
		ctx := c.Request.Context()

		var records []*store.Record
		if s != nil {
			// logged-in user: only their own records; anonymous records are hidden from the list entirely
			mine, err := d.Store.List(ctx, &s.Login, limit)
			if err != nil {
				slog.Error("list reviews (mine)", "err", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			records = mine
		} else {
			// dev fallback: with no Sessions configured, return everything (including anonymous) for easier local debugging
			all, err := d.Store.List(ctx, nil, limit)
			if err != nil {
				slog.Error("list reviews (dev fallback)", "err", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			records = all
		}
		out := make([]reviewListItem, 0, len(records))
		for _, r := range records {
			it := reviewListItem{
				ID:        r.ID,
				Owner:     r.Owner,
				Repo:      r.Repo,
				PR:        r.PRNumber,
				HeadSHA:   r.HeadSHA,
				CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
			}
			if r.UserID != nil {
				it.CreatedBy = *r.UserID
			}
			// title / ci / risk counts come out of the payload; a parse failure leaves the zero values (older cache entries still work)
			var p cachedPayload
			if jerr := json.Unmarshal(r.Payload, &p); jerr == nil {
				it.Title = p.Title
				it.CI = p.CI
				it.Lang = p.Lang
				it.Source = p.Source
				it.RiskCounts = countRisksBySeverity(p.Risks)
			}
			out = append(out, it)
		}
		c.JSON(http.StatusOK, out)
	}
}

// DeleteReview DELETE /api/reviews/:id — delete one review record
// Permissions:
// - login required
// - record.UserID == nil (anonymous leftover) → any logged-in user may delete it (v1 compatibility)
// - record.UserID non-empty → only the owner may delete it
//
// RAG chunks are not cascaded (chunks are shared per owner/repo; deleting a review should not affect others)
func DeleteReview(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "history disabled"})
			return
		}
		s := CurrentSession(c)
		if s == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录", "code": CodeNotLoggedIn})
			return
		}
		id := c.Param("id")
		rec, err := d.Store.GetByID(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if rec == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "review not found"})
			return
		}
		// not an anonymous leftover → owner only
		if rec.UserID != nil && *rec.UserID != s.Login {
			c.JSON(http.StatusForbidden, gin.H{"error": "只能删除你创建的评审", "code": CodeNotReviewOwner})
			return
		}
		if err := d.Store.Delete(c.Request.Context(), id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// clear the agent follow-up session memory while we are here, so no orphan lingers in Redis for 7 days
		// failures only warn — the store transaction already completed, and leftover memory just takes up a little space
		if d.Memory != nil {
			if mErr := d.Memory.Reset(c.Request.Context(), id); mErr != nil {
				slog.Warn("delete review: reset memory failed", "err", mErr, "id", id)
			}
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// GetReview GET /api/reviews/:id — fetch a detail by id; a payload parse failure surfaces as a 500 rather than degrading to an "empty review".
func GetReview(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "history disabled: store not configured"})
			return
		}
		id := c.Param("id")
		rec, err := d.Store.GetByID(c.Request.Context(), id)
		if err != nil {
			slog.Error("get review", "err", err, "id", id)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if rec == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "review not found"})
			return
		}
		var p cachedPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			slog.Error("cached payload unmarshal", "err", err, "id", id)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "corrupted cache payload"})
			return
		}
		// visibility check: an anonymous leftover (UserID==nil) is visible to anyone; a non-empty UserID is owner-only
		// keeps someone from guessing an ID in the URL and peeking at another person's private review
		if rec.UserID != nil {
			sess := CurrentSession(c)
			if sess == nil || sess.Login != *rec.UserID {
				c.JSON(http.StatusNotFound, gin.H{"error": "review not found"})
				return
			}
		}
		createdBy := ""
		if rec.UserID != nil {
			createdBy = *rec.UserID
		}
		c.JSON(http.StatusOK, reviewDetail{
			reviewListItem: reviewListItem{
				ID:         rec.ID,
				Owner:      rec.Owner,
				Repo:       rec.Repo,
				PR:         rec.PRNumber,
				HeadSHA:    rec.HeadSHA,
				Title:      p.Title,
				CreatedAt:  rec.CreatedAt.UTC().Format(time.RFC3339),
				CI:         p.CI,
				Lang:       p.Lang,
				Source:     p.Source,
				CreatedBy:  createdBy,
				RiskCounts: countRisksBySeverity(p.Risks),
			},
			Files:        p.Files,
			Author:       p.Author,
			AuthorRole:   p.AuthorRole,
			State:        p.State,
			Labels:       p.Labels,
			BaseRef:      p.BaseRef,
			HeadRef:      p.HeadRef,
			PRCreatedAt:  p.PRCreatedAt,
			Stats:        p.Stats,
			Checks:       p.Checks,
			Summary:      p.Summary,
			Risks:        p.Risks,
			Suggestions:  p.Suggestions,
			BudgetReport: p.BudgetReport,
		})
	}
}

// parseLimit parses the "limit" query into an integer in [1, maxListLimit]; invalid or absent returns defaultListLimit.
func parseLimit(s string) int {
	if s == "" {
		return defaultListLimit
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return defaultListLimit
	}
	if n > maxListLimit {
		return maxListLimit
	}
	return n
}
