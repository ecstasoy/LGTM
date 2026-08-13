package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/oauth"
)

// PermsResponse is what GET /api/perms?owner=&repo= returns
// The frontend drives the enabled state of the 💬 comment / ✅ commit buttons off it
type PermsResponse struct {
	// Authenticated reports whether the user is logged in; when false the other fields are false / empty
	Authenticated bool `json:"authenticated"`
	// Permission is GitHub's raw perm (admin/maintain/write/triage/read/none)
	// The frontend tooltip uses it to explain why a button is disabled
	Permission string `json:"permission,omitempty"`
	// CanComment needs triage permission at minimum to post a PR review comment
	CanComment bool `json:"can_comment"`
	// CanCommit needs write permission at minimum to push a commit
	// Note: this judges permission on the base repo only; a fork PR with maintainer_can_modify=false needs a separate check
	// Simplified for now: a fork PR is approximated by the base repo's permission
	CanCommit bool `json:"can_commit"`
	// Reason is why it is disabled; shown in the frontend tooltip
	Reason string `json:"reason,omitempty"`
	// ReasonCode is Reason's stable machine-readable counterpart (see errcode.go); empty when Reason carries a
	// dynamic upstream message (e.g. a GitHub API failure) that has no fixed code to key a translation off of.
	ReasonCode string `json:"reason_code,omitempty"`
}

// GetPerms GET /api/perms?owner=<>&repo=<>
// With no owner/repo it returns a 401-friendly empty response (the frontend button can still fall back to "copy markdown")
func GetPerms(oa *oauth.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		owner := c.Query("owner")
		repo := c.Query("repo")
		if owner == "" || repo == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "owner + repo query params required"})
			return
		}

		s := CurrentSession(c)
		if s == nil {
			c.JSON(http.StatusOK, PermsResponse{
				Authenticated: false,
				Reason:        "未登录；登录后可见可执行权限",
				ReasonCode:    CodeNotLoggedIn,
			})
			return
		}
		if oa == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OAuth not configured"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		perm, err := oa.GetRepoPermission(ctx, s.AccessToken, owner, repo, s.Login)
		if err != nil {
			// an API error (expired token / network) → conservatively report no permission
			c.JSON(http.StatusOK, PermsResponse{
				Authenticated: true,
				Permission:    string(oauth.PermNone),
				Reason:        "GitHub 权限查询失败：" + err.Error(),
			})
			return
		}

		resp := PermsResponse{
			Authenticated: true,
			Permission:    string(perm),
			CanComment:    perm.CanComment(),
			CanCommit:     perm.CanCommit(),
		}
		switch {
		case !resp.CanComment:
			resp.Reason = "对此仓库无评论权限（需 triage / write / admin）"
			resp.ReasonCode = CodeNoCommentPermission
		case !resp.CanCommit:
			resp.Reason = "对此仓库无 push 权限（需 write / admin）"
			resp.ReasonCode = CodeNoPushPermission
		}
		c.JSON(http.StatusOK, resp)
	}
}
