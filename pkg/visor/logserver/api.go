// Package logserver contains api's for the logserver
package logserver

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler

	logger    *logging.Logger
	startedAt time.Time
}

// New creates a new API.
func New(log *logging.Logger, tpLogPath, localPath, customPath string, whitelistedPKs []cipher.PubKey, printLog bool) *API {
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
	// note that the survey only exists / is generated if the reward address is set
	authRoute.StaticFile("/node-info.json", filepath.Join(localPath, "node-info.json"))

	r.GET("/health", func(c *gin.Context) {
		api.health(c)
	})

	// serve transport log files ; then any files in the custom path
	r.GET("/:file", func(c *gin.Context) {
		// files with .csv extension are likely transport log files
		if filepath.Ext(c.Param("file")) == ".csv" {
			// check transport logs dir for the file, and serve it if it exists
			_, err := os.Stat(filepath.Join(tpLogPath, c.Param("file")))
			if err == nil {
				c.File(filepath.Join(tpLogPath, c.Param("file")))
				return
			}
		}
		// Check for any file in custom dmsghttp dir path
		_, err := os.Stat(filepath.Join(customPath, c.Param("file")))
		if err == nil {
			c.File(filepath.Join(customPath, c.Param("file")))
			return
		}
		// File not found, return 404
		c.Writer.WriteHeader(http.StatusNotFound)
	})

	api.Handler = r
	return api
}

func (api *API) health(c *gin.Context) {
	jsonObject, err := json.Marshal(
		httputil.HealthCheckResponse{
			BuildInfo: buildinfo.Get(),
			StartedAt: api.startedAt,
		})
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

func whitelistAuth(whitelistedPKs []cipher.PubKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get the remote PK.
		remotePK, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("500 Internal Server Error"))
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
			c.Writer.Write([]byte("401 Unauthorized"))
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

type consoleColorModeValue int

var consoleColorMode = autoColor

const (
	autoColor consoleColorModeValue = iota
	disableColor
	forceColor
)

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
