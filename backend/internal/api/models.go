package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
)

// Models GET /api/models: returns the allowlist of selectable models (the data source for the L3 frontend dropdown).
// With a nil registry (which should not happen, main always builds one) it returns an empty array, and the frontend hides the selector.
func Models(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Models == nil {
			c.JSON(http.StatusOK, []llm.ModelOption{})
			return
		}
		c.JSON(http.StatusOK, d.Models.Options())
	}
}
