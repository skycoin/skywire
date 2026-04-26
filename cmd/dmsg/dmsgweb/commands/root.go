// Package commands cmd/dmsgweb/commands/root.go
package commands

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/gin-gonic/gin"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/logging"
)

// Package-level globals shared between the `web` (client) and `srv`
// (server) subcommands. The dmsgweb client runtime now lives in
// pkg/dmsgweb and owns its own state — these remain only because the
// server-side subcommand (dmsgwebsrv.go) still references them.
// Anything only referenced by the client runtime was moved out
// during the extraction step.
var (
	dlog               *logging.Logger
	httpClient         *http.Client
	dmsgC              *dmsg.Client
	closeDmsg          func()
	proxyAddr          string
	filterDomainSuffix string
	sk                 cipher.SecKey
	pk                 cipher.PubKey
	logLvl             string
	webPort            []uint
	proxyPort          uint
	addProxy           string
	resolveDmsgAddr    []string
	isEnvs             bool
	dmsgPort           []uint
	wl                 []string
	wlkeys             []cipher.PubKey
	localPort          []uint
	err                error
	rawTCP             []bool
	pprofMode          string
	pprofAddr          string
)

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}

func printEnvs(envfile string) {
	if runtime.GOOS == "windows" {
		envfileslice, _ := script.Echo(envfile).Slice() //nolint
		for i := range envfileslice {
			efs, _ := script.Echo(envfileslice[i]).Reject("##").Reject("#-").Reject("# ").Replace("#", "#$").String() //nolint
			if efs != "" && efs != "\n" {
				envfileslice[i] = strings.ReplaceAll(efs, "\n", "")
			}
		}
		envfile = strings.Join(envfileslice, "\n")
	}
	fmt.Println(envfile)
	os.Exit(0)
}

func whitelistAuth(whitelistedPKs []cipher.PubKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		remotePK, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("500 Internal Server Error")) //nolint
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
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
			c.Writer.WriteHeader(http.StatusUnauthorized)
			c.Writer.Write([]byte("401 Unauthorized")) //nolint
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
	}
}

type ginHandler struct { //nolint unused
	Router *gin.Engine
}

func (h *ginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { //nolint unused
	h.Router.ServeHTTP(w, r)
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
		fmt.Printf("[DMSGWEB] %s |%s %3d %s| %13v | %15s | %72s |%s %-7s %s %s\n",
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
