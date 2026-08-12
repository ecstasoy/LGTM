package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// MeResponse is what /api/me returns; when not logged in Authenticated=false and the rest is empty
// A later change will extend it with perms[] (permissions for the PR being viewed)
type MeResponse struct {
	Authenticated bool   `json:"authenticated"`
	Login         string `json:"login,omitempty"`
	UserID        int64  `json:"user_id,omitempty"`
	AvatarURL     string `json:"avatar_url,omitempty"`
	Name          string `json:"name,omitempty"`
}

// GetMe GET /api/me
// Lets the frontend check login state: signed out → show the Sign in button; signed in → show the avatar + sign out
func GetMe() gin.HandlerFunc {
	return func(c *gin.Context) {
		s := CurrentSession(c)
		if s == nil {
			c.JSON(http.StatusOK, MeResponse{Authenticated: false})
			return
		}
		c.JSON(http.StatusOK, MeResponse{
			Authenticated: true,
			Login:         s.Login,
			UserID:        s.UserID,
			AvatarURL:     s.AvatarURL,
			Name:          s.Name,
		})
	}
}
