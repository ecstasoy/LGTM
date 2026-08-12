package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/review"
)

// PostAdoptCommit POST /api/review/:id/commit/:idx
//
// Flow:
// 1. check session + perm.CanCommit
// 2. post a comment with a ```suggestion block, same as the comment endpoint (GitHub apply needs a review thread to exist first)
// 3. look up the thread ID over GraphQL
// 4. GraphQL applyPullRequestReviewThreadSuggestion → produces the commit
// 5. return {ok, comment_id, commit_sha, html_url}
//
// Failure codes: 401 / 404 / 403 / 502 / 422 (thread already resolved, etc.)
//
// Unlike the comment endpoint: CanCommit is stricter than CanComment (needs ≥ write), and for fork PRs it currently approximates using the base repo's permission
// Strictly, commit permission on a fork PR = base.write + maintainer_can_modify=true OR head.push
// That is not re-checked here; the GitHub apply mutation rejects it itself (FORBIDDEN) and the error is passed through to the frontend
func PostAdoptCommit(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		s := CurrentSession(c)
		if s == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
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
		idx, err := strconv.Atoi(c.Param("idx"))
		if err != nil || idx < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "idx must be non-negative integer"})
			return
		}

		// give the whole commit flow a 15s budget (comment + thread query + apply mutation is three round trips)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "suggestion 缺 file/line，无法定位到 PR diff"})
			return
		}
		if sg.Patch == nil || sg.Patch.After == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "suggestion 无 patch.after，无法生成 GitHub suggestion 块（用「评论」按钮可发纯文字建议）"})
			return
		}

		// permission: committing is stricter than commenting
		perm, err := d.OAuthClient.GetRepoPermission(ctx, s.AccessToken, rec.Owner, rec.Repo, s.Login)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "perm check failed: " + err.Error()})
			return
		}
		if !perm.CanCommit() {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "无 push 权限（需 write/admin）",
				"permission": string(perm),
			})
			return
		}

		// 1) post the review comment first (same body as the comment endpoint)
		body := buildSuggestionCommentBody(sg)
		cm, err := d.OAuthClient.PostPRComment(ctx, s.AccessToken, rec.Owner, rec.Repo, rec.PRNumber,
			body, rec.HeadSHA, sg.File, sg.Line)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "post comment failed: " + err.Error()})
			return
		}

		// 2) find the thread ID of the comment just posted
		threadID, err := d.OAuthClient.FindReviewThreadID(ctx, s.AccessToken, rec.Owner, rec.Repo, rec.PRNumber, cm.ID)
		if err != nil {
			// the comment is already posted but the thread is missing → give the user a clear message and return the comment URL so they can apply by hand
			c.JSON(http.StatusOK, AdoptResponse{
				OK:        false,
				CommentID: cm.ID,
				HTMLURL:   cm.HTMLUrl,
			})
			return
		}

		// 3) call the GraphQL apply mutation
		applyResult, err := d.OAuthClient.ApplyReviewThreadSuggestion(ctx, s.AccessToken, threadID)
		if err != nil {
			// the comment went through but apply failed → the fork PR does not allow edits, or some other GitHub restriction
			// not a total failure: the user can at least see the comment and Apply it manually
			c.JSON(http.StatusOK, AdoptCommitResponse{
				AdoptResponse: AdoptResponse{
					OK:        false,
					CommentID: cm.ID,
					HTMLURL:   cm.HTMLUrl,
				},
				CommentPostedButCommitFailed: true,
				CommitFailReason:             err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, AdoptCommitResponse{
			AdoptResponse: AdoptResponse{
				OK:        true,
				CommentID: cm.ID,
				HTMLURL:   cm.HTMLUrl,
			},
			CommitSHA: applyResult.CommitOID,
		})
	}
}

// AdoptCommitResponse is what the /commit endpoint returns;
// CommentPostedButCommitFailed=true means the comment reached the PR but apply failed (fork restriction, etc.)
// In that case the frontend should tell the user to go press Apply on GitHub
type AdoptCommitResponse struct {
	AdoptResponse
	CommitSHA                    string `json:"commit_sha,omitempty"`
	CommentPostedButCommitFailed bool   `json:"comment_posted_but_commit_failed,omitempty"`
	CommitFailReason             string `json:"commit_fail_reason,omitempty"`
}
