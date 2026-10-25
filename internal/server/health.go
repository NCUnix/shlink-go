package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/NCUnix/shlink-go/internal/api"
)

type healthLinks struct {
	About   string `json:"about"`
	Project string `json:"project"`
}

type healthResponse struct {
	Status  string      `json:"status"`
	Version string      `json:"version"`
	Links   healthLinks `json:"links"`
}

func (a *App) registerHealth() {
	a.Engine.GET("/rest/health", a.health)
	a.Engine.GET("/rest/v3/health", a.health)
}

func (a *App) health(c *gin.Context) {
	status := http.StatusOK
	state := "pass"
	if a.DB == nil || a.DB.PingContext(c.Request.Context()) != nil {
		status = http.StatusServiceUnavailable
		state = "fail"
	}
	c.Header("Content-Type", "application/health+json")
	c.JSON(status, healthResponse{
		Status:  state,
		Version: api.ShlinkVersion,
		Links: healthLinks{
			About:   "https://shlink.io",
			Project: "https://github.com/shlinkio/shlink",
		},
	})
}
