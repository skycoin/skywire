// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/nodeproxy.go
package clirewardsserver

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// registerNodeProxy sets up reverse proxy routes for a fiber/skycoin node API.
// This allows skycoin-web to connect to the reward system URL as its node endpoint,
// with /api/v1/* and /api/v2/* proxied to the local fiber node.
func registerNodeProxy(r *gin.Engine, targetURL string) error {
	target, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("invalid node URL %q: %w", targetURL, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Preserve the original director but override the host
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
	}

	// Handle CORS for skycoin-web thin client
	corsMiddleware := func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin != "" {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}

	proxyHandler := func(c *gin.Context) {
		proxy.ServeHTTP(c.Writer, c.Request)
	}

	// Register routes for node API versions
	for _, prefix := range []string{"/api/v1", "/api/v2"} {
		group := r.Group(prefix)
		group.Use(corsMiddleware)
		group.Any("/*path", proxyHandler)
	}

	// Also proxy /csrf endpoint which skycoin-web needs for POST requests
	r.GET("/csrf", corsMiddleware, proxyHandler)

	fmt.Printf("Proxying /api/v1/*, /api/v2/*, /csrf → %s\n", targetURL)
	return nil
}

// nodeHealthCheck verifies the fiber node is reachable
func nodeHealthCheck(targetURL string) error {
	resp, err := http.Get(strings.TrimSuffix(targetURL, "/") + "/api/v1/health") //nolint:gosec
	if err != nil {
		return fmt.Errorf("node health check failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("node health check returned status %d", resp.StatusCode)
	}
	return nil
}
