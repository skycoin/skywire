// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/nodeproxy.go c4-vis-cli
package clirewardsserver

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// registerNodeProxy sets up reverse proxy routes for a fiber/skycoin node API.
// This allows skycoin-web to connect to the reward system URL as its node endpoint,
// with /api/v1/* and /api/v2/* proxied to the local fiber node.
func registerNodeProxy(mux *http.ServeMux, targetURL string) error {
	target, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("invalid node URL %q: %w", targetURL, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Preserve the original director but override the host
	originalDirector := proxy.Director         //nolint:staticcheck
	proxy.Director = func(req *http.Request) { //nolint:staticcheck
		originalDirector(req)
		req.Host = target.Host
	}

	// Handle CORS for skycoin-web thin client
	corsProxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		proxy.ServeHTTP(w, r)
	})

	mux.Handle("/api/v1/", corsProxy)
	mux.Handle("/api/v2/", corsProxy)
	// skycoin-web needs /csrf for POST requests
	mux.Handle("GET /csrf", corsProxy)

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
