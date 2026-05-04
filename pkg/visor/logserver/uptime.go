// Package logserver — pkg/visor/logserver/uptime.go: gin shim for the
// pkg/serviceuptime HTTP handlers. Wires /uptime/* onto the visor's
// existing logserver router so a CLI client can render the visor's
// session history without going through the unix-socket RPC.
package logserver

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/skycoin/skywire/pkg/serviceuptime"
)

// SetUptimeRecorder wires the recorder into the log server. Safe
// to call before or after handler registration; the routes nil-check
// at request time, so a recorder attached after some early traffic
// still works for subsequent requests.
func (api *API) SetUptimeRecorder(r *serviceuptime.Recorder) {
	api.uptimeRecorder = r
}

// registerUptimeRoutes mounts /uptime/* on authRoute. Each handler
// degrades to 503 when api.uptimeRecorder is nil so the path stays
// available for monitoring scrapes that don't want to special-case
// pre-recorder visors.
func (api *API) registerUptimeRoutes(authRoute *gin.RouterGroup) {
	authRoute.GET("/uptime/now", func(c *gin.Context) {
		if api.uptimeRecorder == nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		serviceuptime.CurrentSessionHandler(api.uptimeRecorder).ServeHTTP(c.Writer, c.Request)
	})
	authRoute.GET("/uptime/sessions", func(c *gin.Context) {
		if api.uptimeRecorder == nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		serviceuptime.SessionsHandler(api.uptimeRecorder.Store()).ServeHTTP(c.Writer, c.Request)
	})
	authRoute.GET("/uptime/timeline", func(c *gin.Context) {
		if api.uptimeRecorder == nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		serviceuptime.TimelineHandler(api.uptimeRecorder.Store()).ServeHTTP(c.Writer, c.Request)
	})
	authRoute.GET("/uptime/dates", func(c *gin.Context) {
		if api.uptimeRecorder == nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		serviceuptime.DatesHandler(api.uptimeRecorder.Store()).ServeHTTP(c.Writer, c.Request)
	})
}
