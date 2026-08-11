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

// AdoptResponse POST /api/review/:id/comment/:idx 成功返回
// HTMLURL 让前端给用户直接跳到 GitHub 上看新发的 comment
type AdoptResponse struct {
	OK        bool   `json:"ok"`
	CommentID int64  `json:"comment_id,omitempty"`
	HTMLURL   string `json:"html_url,omitempty"`
}

// PostAdoptComment POST /api/review/:id/comment/:idx
//
// 把缓存里第 idx 条 Suggestion 转成 GitHub PR review comment 发出去
// body 含 ```suggestion 代码块 → PR author 在 GitHub UI 一键 "Apply suggestion" commit
//
// 路径检查链：
//  1. session 存在（401）
//  2. review 存在（404）
//  3. idx 合法（400）
//  4. OAuth 配齐（503）
//  5. 用户有 comment 权限（403）
//  6. GitHub API 成功（502 + 透传 GitHub 错误信息）
func PostAdoptComment(d Deps) gin.HandlerFunc {
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

		// 解析 payload 拿 suggestion list
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

		// 权限校验：缺权限直接 403（带 reason）
		perm, err := d.OAuthClient.GetRepoPermission(ctx, s.AccessToken, rec.Owner, rec.Repo, s.Login)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "perm check failed: " + err.Error()})
			return
		}
		if !perm.CanComment() {
			c.JSON(http.StatusForbidden, gin.H{
				"error":      "无评论权限（需 triage/write/admin）",
				"permission": string(perm),
			})
			return
		}

		// 构造 comment body：title + body + ```suggestion 块 + 出处 footer
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
// :cid 是 GitHub PR review comment 的 databaseId（来自 PostAdoptComment 的返值）
// 用户在 InlineSuggestion 的「已发到 PR」按钮旁可以「× 撤回」
//
// 检查链：session → review 存在（用来推出 owner/repo）→ 调 GitHub DELETE
// 不再 verify 用户对 review 的所有权（comment 作者校验交给 GitHub 拒）
func DeleteAdoptComment(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		s := CurrentSession(c)
		if s == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
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

// buildSuggestionCommentBody 把一条 Suggestion 拼成 GitHub PR review comment 的 markdown
// 关键：```suggestion 块只放 patch.after，GitHub 会自动算出与原代码的 diff 并支持一键 Apply
// 缺 patch 时退化为纯文字建议（仍然有用，只是 PR author 要手动改）
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
