package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Health is the old /health endpoint = liveness (no dependency checks). Kept for compatibility.
// Used by: container orchestrator liveness probes, Docker HEALTHCHECK, UptimeRobot
func Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Readiness checks whether downstream dependencies are usable. Any failure → 503, so the LB / k8s pulls it out of rotation.
// Checks:
// - store: Ping it when Deps.Store is non-nil
// - future: Cache.Ping / LLM provider health (the LLM is deliberately left out — third parties are slow and flaky,
// and readiness itself has to be fast and decisive)
//
// 1s timeout: never block the orchestrator; a DB that cannot be pinged within a second counts as unhealthy
func Readiness(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()

		checks := map[string]string{}
		allHealthy := true

		if d.Store != nil {
			if err := d.Store.Ping(ctx); err != nil {
				slog.Error("store ping failed", "err", err)
				checks["store"] = "fail"
				allHealthy = false
			} else {
				checks["store"] = "ok"
			}
		} else {
			checks["store"] = "disabled"
		}

		status := "ready"
		code := http.StatusOK
		if !allHealthy {
			status = "degraded"
			code = http.StatusServiceUnavailable
		}
		c.JSON(code, gin.H{"status": status, "checks": checks})
	}
}
