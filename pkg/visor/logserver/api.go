// Package logserver contains api's for the logserver
package logserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// ServiceEntry describes a port-forwarded service for the /services catalog.
type ServiceEntry struct {
	Port  uint16 `json:"port"`
	Label string `json:"label"`
}

// ServiceLister provides the service catalog for the /services endpoint.
// Implemented by visor's ServiceRegistry.
type ServiceLister interface {
	ListPublic() []ServiceEntry
}

// ForwardedPortEntry describes a user-forwarded port for the landing page.
type ForwardedPortEntry struct {
	Port        int    `json:"port"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ForwardedPortLister provides forwarded ports for the landing page
// and per-port whitelist for access control.
type ForwardedPortLister interface {
	LandingPageEntries() []ForwardedPortEntry
	// PortWhitelist returns the PK whitelist for a given port.
	// An empty slice means the port is accessible to everyone.
	PortWhitelist(port int) []cipher.PubKey
}

// HealthStatsProvider provides transport statistics for the /health endpoint.
type HealthStatsProvider interface {
	// IsPublicAutoconnectRunning returns true if the public autoconnect module is running.
	IsPublicAutoconnectRunning() bool
	// GetTransportCounts returns the count of STCPR and SUDPH transports (excluding "user" labeled).
	GetTransportCounts() (stcpr, sudph int)
	// GetNetworkTypes returns the network types used by the visor.
	GetNetworkTypes() []string
}

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler

	logger              *logging.Logger
	startedAt           time.Time
	healthStatsProvider HealthStatsProvider
	serviceLister       ServiceLister
	forwardedPortLister ForwardedPortLister
	statsReader         StatsReader  // visor-local telemetry store, set via SetStatsReader
	websiteHandler      http.Handler // optional: serves unmatched routes (custom website)
}

// New creates a new API.
func New(log *logging.Logger, tpLogPath, localPath, _ string, whitelistedPKs []cipher.PubKey, survey *visorconfig.Survey, printLog bool) *API {
	api := &API{
		logger:    log,
		startedAt: time.Now(),
	}
	// disable gin's debug logging on startup
	gin.SetMode(gin.ReleaseMode)
	// Gin router without default logging
	r := gin.New()
	// use Gin's recovery logging middleware to recover from panic
	r.Use(gin.Recovery())
	if printLog {
		// use custom logging middleware
		r.Use(loggingMiddleware())
	}

	// whitelist-based authentication for survey collection if there are keys whitelisted for that
	// no survey-whitelisted keys means the file is publicly accessible
	authRoute := r.Group("/")
	if len(whitelistedPKs) > 0 {
		authRoute.Use(whitelistAuth(whitelistedPKs))
	}

	// serve the file with the reward address - only exists if the reward address is set
	authRoute.StaticFile("/"+skyenv.RewardFile, filepath.Join(localPath, skyenv.RewardFile)) // "/reward.txt"

	// This survey endpoint generates the survey as a response
	authRoute.GET("/node-info", func(c *gin.Context) {
		c.JSON(http.StatusOK, *survey)
	})

	// Checksum endpoint for survey — allows collectors to skip re-downloading unchanged surveys
	authRoute.GET("/node-info/checksum", func(c *gin.Context) {
		data, err := json.Marshal(*survey)
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		sum := sha256.Sum256(data)
		c.JSON(http.StatusOK, gin.H{"sha256": hex.EncodeToString(sum[:])})
	})

	r.GET("/health", func(c *gin.Context) {
		api.health(c)
	})

	// Service catalog — lists ports available for .skynet / skynet
	// forwarding. Public services are visible; hidden services are
	// omitted. Browsers visiting http://pk.dmsg/services see what
	// the visor exposes.
	r.GET("/services", func(c *gin.Context) {
		if api.serviceLister == nil {
			c.JSON(http.StatusOK, []ServiceEntry{})
			return
		}
		c.JSON(http.StatusOK, api.serviceLister.ListPublic())
	})

	// Transport log files (auth'd)
	authRoute.GET("/transport_logs/:file", func(c *gin.Context) {
		if filepath.Ext(c.Param("file")) == ".csv" {
			fpath := filepath.Join(tpLogPath, c.Param("file"))
			if _, err := os.Stat(fpath); err == nil {
				c.File(fpath)
				return
			}
		}
		c.Writer.WriteHeader(http.StatusNotFound)
	})

	// Serve visor log file (auth'd) — written when visor runs with -s/--save-log
	authRoute.GET("/visor.log", func(c *gin.Context) {
		logFile := filepath.Join(localPath, "visor.log")
		if _, err := os.Stat(logFile); err != nil {
			c.String(http.StatusNotFound, "visor.log not found (start visor with -s flag)")
			return
		}
		c.File(logFile)
	})

	// pprof endpoints (auth'd) — runtime profiling
	authRoute.GET("/debug/pprof/", gin.WrapF(pprof.Index))
	authRoute.GET("/debug/pprof/cmdline", gin.WrapF(pprof.Cmdline))
	authRoute.GET("/debug/pprof/profile", gin.WrapF(pprof.Profile))
	authRoute.GET("/debug/pprof/symbol", gin.WrapF(pprof.Symbol))
	authRoute.GET("/debug/pprof/trace", gin.WrapF(pprof.Trace))
	authRoute.GET("/debug/pprof/:name", gin.WrapH(http.HandlerFunc(pprof.Index)))

	// /stats/* (auth'd) — visor-local telemetry store. Handlers
	// degrade to 503 when SetStatsReader hasn't been called.
	api.registerStatsRoutes(authRoute)

	// isWhitelisted checks if the current request is from a whitelisted PK
	// without blocking. Used by the landing page to show/hide auth'd links.
	isWhitelisted := func(c *gin.Context) bool {
		if len(whitelistedPKs) == 0 {
			return true
		}
		remotePK, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			return false
		}
		for _, pk := range whitelistedPKs {
			if remotePK == pk.String() {
				return true
			}
		}
		return false
	}

	// Landing page with links to available endpoints
	r.GET("/", func(c *gin.Context) {
		wl := isWhitelisted(c)
		c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		var links []string
		links = append(links, `<a href="/health">/health</a> - visor health status`)
		if wl {
			links = append(links, `<a href="/node-info">/node-info</a> - node survey`)
			links = append(links, `<a href="/node-info/checksum">/node-info/checksum</a> - survey checksum`)
			links = append(links, `<a href="/visor.log">/visor.log</a> - visor debug log`)
			links = append(links, `<a href="/debug/pprof/">/debug/pprof/</a> - runtime profiling`)
			if api.statsReader != nil {
				links = append(links, `<a href="/stats/transports">/stats/transports</a> - live transport snapshot`)
				links = append(links, `<a href="/stats/transports/history">/stats/transports/history</a> - daily transport rollups (?since=&until=&id=)`)
				links = append(links, `<a href="/stats/uptime">/stats/uptime</a> - three-tier uptime bitmaps`)
				links = append(links, `<a href="/stats/services">/stats/services</a> - per-service uptime bitmaps`)
			}
			// List transport log files
			if entries, err := os.ReadDir(tpLogPath); err == nil {
				for _, e := range entries {
					if !e.IsDir() && filepath.Ext(e.Name()) == ".csv" {
						links = append(links, fmt.Sprintf(`<a href="/transport_logs/%s">/transport_logs/%s</a>`, e.Name(), e.Name()))
					}
				}
			}
		}
		// Add forwarded ports visible on the landing page.
		// Use the request Host to construct proper URLs that work
		// in the browser (e.g. http://pk.skynet:8000/).
		if api.forwardedPortLister != nil {
			host := c.Request.Host
			// Strip existing port from host if present
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			// If host is a bare PK (66 hex chars), append .skynet
			// so generated URLs route through the SOCKS5 proxy.
			if len(host) == 66 && !strings.Contains(host, ".") {
				host += ".skynet"
			}
			for _, fp := range api.forwardedPortLister.LandingPageEntries() {
				label := fp.Label
				if label == "" {
					label = fmt.Sprintf("port %d", fp.Port)
				}
				desc := ""
				if fp.Description != "" {
					desc = " - " + fp.Description
				}
				url := fmt.Sprintf("http://%s:%d/", host, fp.Port)
				links = append(links, fmt.Sprintf(`<a href="%s">%s</a>%s`, url, label, desc))
			}
		}

		c.Writer.WriteHeader(http.StatusOK)
		fmt.Fprintf(c.Writer, `<!doctype html><html><head><title>Skywire Visor</title>`+ //nolint:errcheck,gosec
			`<style>body{background:#000;color:#fff;font-family:monospace;padding:20px}a{color:#3399FF}a:visited{color:#FF00FF}</style>`+
			`</head><body><h2>Skywire Visor</h2><pre>%s</pre></body></html>`, strings.Join(links, "\n"))
	})

	// Catch-all: if a custom website handler is set, serve unmatched
	// routes through it. Visor endpoints always take priority since
	// they're registered as explicit routes above.
	//
	// Access control tiers on port 80:
	//   /health, /ping, /services — open to everyone
	//   /node-info, /visor.log, /debug/pprof — survey whitelist
	//   everything else (website) — forwarded port whitelist (if set)
	r.NoRoute(func(c *gin.Context) {
		if api.websiteHandler != nil {
			// Enforce the forwarded port's PK whitelist on the website.
			if api.forwardedPortLister != nil {
				if wl := api.forwardedPortLister.PortWhitelist(80); len(wl) > 0 {
					remotePK, _, err := net.SplitHostPort(c.Request.RemoteAddr)
					if err != nil {
						c.AbortWithStatus(http.StatusForbidden)
						return
					}
					allowed := false
					for _, pk := range wl {
						if remotePK == pk.String() {
							allowed = true
							break
						}
					}
					if !allowed {
						c.AbortWithStatus(http.StatusForbidden)
						return
					}
				}
			}
			api.websiteHandler.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.String(http.StatusNotFound, "404 not found")
	})

	api.Handler = r
	return api
}

// SetWebsiteHandler sets a custom HTTP handler for serving unmatched
// routes on the visor's DMSG/skynet port 80. Visor system endpoints
// (/health, /node-info, /services, etc.) always take priority.
//
// Use cases:
// - Static file server: http.FileServer(http.Dir("/path/to/site"))
// - Reverse proxy to a local web app: httputil.ReverseProxy
// - The reward system UI gin handler
func (api *API) SetWebsiteHandler(h http.Handler) {
	api.websiteHandler = h
}

func (api *API) health(c *gin.Context) {
	resp := httputil.HealthCheckResponse{
		ServiceName: "visor",
		BuildInfo:   buildinfo.Get(),
		StartedAt:   api.startedAt,
	}

	// Add transport stats if provider is available
	if api.healthStatsProvider != nil {
		resp.PublicAutoconnect = api.healthStatsProvider.IsPublicAutoconnectRunning()
		resp.StcprCount, resp.SudphCount = api.healthStatsProvider.GetTransportCounts()
		resp.NetworkTypes = api.healthStatsProvider.GetNetworkTypes()
	}

	jsonObject, err := json.Marshal(resp)
	if err != nil {
		httputil.GetLogger(c.Request).WithError(err).Errorf("failed to encode json response")
		c.Writer.WriteHeader(http.StatusInternalServerError)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Header("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)

	_, err = c.Writer.Write(jsonObject)
	if err != nil {
		httputil.GetLogger(c.Request).WithError(err).Errorf("failed to write json response")
	}
}

// SetHealthStatsProvider sets the health stats provider after initialization.
func (api *API) SetHealthStatsProvider(provider HealthStatsProvider) {
	api.healthStatsProvider = provider
}

// SetServiceLister sets the service catalog provider. Called from
// visor init after the ServiceRegistry is populated.
func (api *API) SetServiceLister(lister ServiceLister) {
	api.serviceLister = lister
}

// SetForwardedPortLister sets the forwarded port provider for the landing page.
func (api *API) SetForwardedPortLister(lister ForwardedPortLister) {
	api.forwardedPortLister = lister
}

func whitelistAuth(whitelistedPKs []cipher.PubKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get the remote PK.
		remotePK, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		// Check if the remote PK is whitelisted.
		whitelisted := false
		if len(whitelistedPKs) == 0 {
			whitelisted = true
		} else {
			for _, whitelistedPK := range whitelistedPKs {
				if remotePK == whitelistedPK.String() {
					whitelisted = true
					break
				}
			}
		}
		if whitelisted {
			c.Next()
		} else {
			// Otherwise, return a 401 Unauthorized error.
			c.Writer.WriteHeader(http.StatusUnauthorized)
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
	}
}

func loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)
		if latency > time.Minute {
			latency = latency.Truncate(time.Second)
		}
		statusCode := c.Writer.Status()
		method := c.Request.Method
		path := c.Request.URL.Path
		// Get the background color based on the status code
		statusCodeBackgroundColor := getBackgroundColor(statusCode)
		// Get the method color
		methodColor := getMethodColor(method)
		// Print the logging in a custom format which includes the publickeyfrom c.Request.RemoteAddr ex.:
		// [DMSGHTTP] 2023/05/18 - 19:43:15 | 200 |    10.80885ms |                 | 02b5ee5333aa6b7f5fc623b7d5f35f505cb7f974e98a70751cf41962f84c8c4637:49153 | GET      /node-info.json
		fmt.Printf("[DMSGHTTP] %s |%s %3d %s| %13v | %15s | %72s |%s %-7s %s %s\n",
			time.Now().Format("2006/01/02 - 15:04:05"),
			statusCodeBackgroundColor,
			statusCode,
			resetColor(),
			latency,
			c.ClientIP(),
			c.Request.RemoteAddr,
			methodColor,
			method,
			resetColor(),
			path,
		)
	}
}
func getBackgroundColor(statusCode int) string {
	switch {
	case statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices:
		return green
	case statusCode >= http.StatusMultipleChoices && statusCode < http.StatusBadRequest:
		return white
	case statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError:
		return yellow
	default:
		return red
	}
}

func getMethodColor(method string) string {
	switch method {
	case http.MethodGet:
		return blue
	case http.MethodPost:
		return cyan
	case http.MethodPut:
		return yellow
	case http.MethodDelete:
		return red
	case http.MethodPatch:
		return green
	case http.MethodHead:
		return magenta
	case http.MethodOptions:
		return white
	default:
		return reset
	}
}

func resetColor() string {
	return reset
}

const (
	green   = "\033[97;42m"
	white   = "\033[90;47m"
	yellow  = "\033[90;43m"
	red     = "\033[97;41m"
	blue    = "\033[97;44m"
	magenta = "\033[97;45m"
	cyan    = "\033[97;46m"
	reset   = "\033[0m"
)
