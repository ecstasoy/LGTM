package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/review"
)

// AdoptResponse is the success response of POST /api/review/:id/comment/:idx
// HTMLURL lets the frontend send the user straight to the new comment on GitHub
type AdoptResponse struct {
	OK        bool   `json:"ok"`
	CommentID int64  `json:"comment_id,omitempty"`
	HTMLURL   string `json:"html_url,omitempty"`
}

// PostAdoptComment POST /api/review/:id/comment/:idx
//
// Turns suggestion idx from the cache into a GitHub PR review comment and posts it
// The body contains a ```suggestion block → the PR author gets one-click "Apply suggestion" commit in the GitHub UI
//
// Check chain:
// 1. session exists (401)
// 2. review exists (404)
// 3. idx is in range (400)
// 4. OAuth is configured (503)
// 5. the user may comment (403)
// 6. the GitHub API call succeeded (502 + GitHub's error message passed through)
func PostAdoptComment(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		s := CurrentSession(c)
		if s == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录", "code": CodeNotLoggedIn})
			return
		}
		if d.OAuthClient == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OAuth not configured"})
			return
		}
		if d.Store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "store not configured"})
			return
		}

		id := c.Param("id")
		idxStr := c.Param("idx")
		idx, err := strconv.Atoi(idxStr)
		if err != nil || idx < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "idx must be non-negative integer"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()

		rec, err := d.Store.GetByID(ctx, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "store: " + err.Error()})
			return
		}
		if rec == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "review not found"})
			return
		}

		// parse the payload for the suggestion list
		var payload cachedPayload
		if err := json.Unmarshal(rec.Payload, &payload); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "payload: " + err.Error()})
			return
		}
		var suggestions []review.Suggestion
		if len(payload.Suggestions) > 0 {
			if err := json.Unmarshal(payload.Suggestions, &suggestions); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "suggestions parse: " + err.Error()})
				return
			}
		}
		if idx >= len(suggestions) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("idx %d out of range (have %d)", idx, len(suggestions))})
			return
		}
		sg := suggestions[idx]
		if sg.File == "" || sg.Line <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "suggestion 缺 file/line，无法定位到 PR diff", "code": CodeSuggestionNoAnchor})
			return
		}

		// permission check: missing permission is a straight 403 (with a reason)
		perm, err := d.OAuthClient.GetRepoPermission(ctx, s.AccessToken, rec.Owner, rec.Repo, s.Login)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "perm check failed: " + err.Error()})
			return
		}
		if !perm.CanComment() {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "无评论权限（需 triage/write/admin）",
				"code":       CodeNoCommentPermission,
				"permission": string(perm),
			})
			return
		}

		// build the comment body: title + body + ```suggestion block + provenance footer
		body := buildSuggestionCommentBody(sg)

		cm, err := d.OAuthClient.PostPRComment(ctx, s.AccessToken, rec.Owner, rec.Repo, rec.PRNumber,
			body, rec.HeadSHA, sg.File, sg.Line)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "post comment failed: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, AdoptResponse{OK: true, CommentID: cm.ID, HTMLURL: cm.HTMLUrl})
	}
}

// DeleteAdoptComment DELETE /api/review/:id/comment/:cid
// :cid is the databaseId of the GitHub PR review comment (as returned by PostAdoptComment)
// The user gets an "× withdraw" next to the "posted to PR" button on an InlineSuggestion
//
// Check chain: session → review exists (used to derive owner/repo) → call GitHub DELETE
// Ownership of the review is deliberately not verified here; GitHub rejects it if the caller did not author the comment
func DeleteAdoptComment(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		s := CurrentSession(c)
		if s == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录", "code": CodeNotLoggedIn})
			return
		}
		if d.OAuthClient == nil || d.Store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OAuth / store not configured"})
			return
		}

		id := c.Param("id")
		commentID, err := strconv.ParseInt(c.Param("cid"), 10, 64)
		if err != nil || commentID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cid must be positive integer"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		defer cancel()

		rec, err := d.Store.GetByID(ctx, id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "store: " + err.Error()})
			return
		}
		if rec == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "review not found"})
			return
		}

		if err := d.OAuthClient.DeletePRComment(ctx, s.AccessToken, rec.Owner, rec.Repo, commentID); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "delete comment failed: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// buildSuggestionCommentBody renders one Suggestion as GitHub PR review comment markdown
// The key part: the ```suggestion block holds only patch.after, and GitHub works out the diff against the original and offers one-click Apply
// With no patch it degrades to a plain text suggestion (still useful, the PR author just edits by hand)
func buildSuggestionCommentBody(s review.Suggestion) string {
	var sb strings.Builder
	sb.WriteString("**")
	sb.WriteString(s.Title)
	sb.WriteString("** ·  AI 建议（")
	sb.WriteString(s.Type)
	sb.WriteString("）\n\n")
	sb.WriteString(s.Body)
	if s.Patch != nil && s.Patch.After != "" {
		sb.WriteString("\n\n```suggestion\n")
		sb.WriteString(s.Patch.After)
		sb.WriteString("\n```")
	}
	sb.WriteString("\n\n<sub>— [LGTM](https://lgtm-alpha.vercel.app) 自动生成</sub>")
	return sb.String()
}
