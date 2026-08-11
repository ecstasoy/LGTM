package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
)

// Models GET /api/models：返回可选模型白名单（L3 前端下拉数据源）。
// 注册表为 nil（理论上不会，main 总会构造）时返回空数组，前端据此隐藏选择器。
func Models(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Models == nil {
			c.JSON(http.StatusOK, []llm.ModelOption{})
			return
		}
		c.JSON(http.StatusOK, d.Models.Options())
	}
}
