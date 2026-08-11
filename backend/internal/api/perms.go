package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/oauth"
)

// PermsResponse GET /api/perms?owner=&repo= 返回字段
// 前端拿它驱动「💬 评论」/「✅ 提交」按钮的 enable 状态
type PermsResponse struct {
	// Authenticated 用户是否已登录；未登录其它字段为 false / 空
	Authenticated bool `json:"authenticated"`
	// Permission GitHub 返回的原始 perm（admin/maintain/write/triage/read/none）
	// 前端 tooltip 用来解释为啥按钮 disable
	Permission string `json:"permission,omitempty"`
	// CanComment 至少 triage 权限即可发 PR review comment
	CanComment bool `json:"can_comment"`
	// CanCommit 至少 write 权限可以 push commit
	// 注意：仅对 base repo 的权限判定；fork PR 时若 maintainer_can_modify=false 还得另判
	// 当前 v3 简化：fork PR 暂时按 base repo 权限近似（详见 G6c 实现）
	CanCommit bool `json:"can_commit"`
	// Reason disable 原因；前端 tooltip 显示
	Reason string `json:"reason,omitempty"`
}

// GetPerms GET /api/perms?owner=<>&repo=<>
// 不需要 owner/repo 时返 401-friendly 空响应（前端 button 仍可 fallback 到「复制 markdown」）
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
			// API 错（token 失效 / 网络）→ 保守按无权限返
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
		case !resp.CanCommit:
			resp.Reason = "对此仓库无 push 权限（需 write / admin）"
		}
		c.JSON(http.StatusOK, resp)
	}
}
