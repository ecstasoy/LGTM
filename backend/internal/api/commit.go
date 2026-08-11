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
// 流程：
//  1. 检查 session + perm.CanCommit
//  2. 同 G6b 一样 post comment with ```suggestion 块（GitHub apply 必须先有 review thread）
//  3. GraphQL 查 thread ID
//  4. GraphQL applyPullRequestReviewThreadSuggestion → 触发 commit
//  5. 返 {ok, comment_id, commit_sha, html_url}
//
// 失败码：401 / 404 / 403 / 502 / 422（thread 已 resolved 等）
//
// 跟 G6b 的区别：CanCommit 比 CanComment 严（≥ write），且对 fork PR 当前简化按 base repo 权限近似
// fork PR 严格 commit 权限 = base.write + maintainer_can_modify=true OR head.push
// 当前不二次校验，让 GitHub apply mutation 自己拒（FORBIDDEN）+ 把错误透传给前端
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

		// 给整条 commit 流 15s 预算（comment + thread query + apply mutation 三次 RTT）
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

		// 权限：评 commit 比 comment 严
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

		// 1) 先 post review comment（同 G6b body）
		body := buildSuggestionCommentBody(sg)
		cm, err := d.OAuthClient.PostPRComment(ctx, s.AccessToken, rec.Owner, rec.Repo, rec.PRNumber,
			body, rec.HeadSHA, sg.File, sg.Line)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "post comment failed: " + err.Error()})
			return
		}

		// 2) 找到刚发的 comment 对应的 thread ID
		threadID, err := d.OAuthClient.FindReviewThreadID(ctx, s.AccessToken, rec.Owner, rec.Repo, rec.PRNumber, cm.ID)
		if err != nil {
			// 已经 posted comment 了但 thread 找不到 → 给用户友好提示同时返 comment URL 让用户可手动 apply
			c.JSON(http.StatusOK, AdoptResponse{
				OK:        false,
				CommentID: cm.ID,
				HTMLURL:   cm.HTMLUrl,
			})
			return
		}

		// 3) 调 GraphQL apply mutation
		applyResult, err := d.OAuthClient.ApplyReviewThreadSuggestion(ctx, s.AccessToken, threadID)
		if err != nil {
			// comment 已发，apply 失败 → fork PR 未开放编辑 / 其它 GitHub 限制
			// 不算完全失败：用户至少能看到 comment + 手动 Apply
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

// AdoptCommitResponse /commit 端点返回；
// CommentPostedButCommitFailed=true 时 comment 已上 PR 但 apply 失败（fork 限制等）
// 此时 frontend 应提示用户去 GitHub 手动点 Apply
type AdoptCommitResponse struct {
	AdoptResponse
	CommitSHA                    string `json:"commit_sha,omitempty"`
	CommentPostedButCommitFailed bool   `json:"comment_posted_but_commit_failed,omitempty"`
	CommitFailReason             string `json:"commit_fail_reason,omitempty"`
}
