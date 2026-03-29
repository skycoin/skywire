// Package commands cmd/dmsghttp/commands/dmsghttp.go
package commands

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"
	"golang.org/x/net/proxy"

	dmsgcmdutil "github.com/skycoin/dmsg/pkg/cmdutil"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgclient"
)

var (
	dlog      = logging.MustGetLogger("dmsghttp")
	dmsgPort  uint
	logLvl    string
	proxyAddr string
	sk        cipher.SecKey
	pk        cipher.PubKey
	serveDir  string
	wl        []string
	wlkeys    []cipher.PubKey
	err       error
	pprofMode string
	pprofAddr string
)

func init() {
	RootCmd.Flags().SortFlags = false
	dmsgclient.InitFlags(RootCmd)
	RootCmd.Flags().StringVarP(&proxyAddr, "proxy", "p", proxyAddr, "connect to DMSG via proxy (i.e. '127.0.0.1:1080')")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "debug", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m\n\r")
	RootCmd.Flags().StringVarP(&serveDir, "dir", "r", ".", "local dir to serve via dmsghttp\033[0m\n\r")
	RootCmd.Flags().UintVarP(&dmsgPort, "port", "d", 80, "DMSG port to serve from\033[0m\n\r")
	RootCmd.Flags().StringSliceVarP(&wl, "wl", "w", []string{}, "whitelist keys to access server, comma separated")
	if os.Getenv("DMSGHTTP_SK") != "" {
		sk.Set(os.Getenv("DMSGHTTP_SK")) //nolint
	}
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\033[0m\n\r")
	RootCmd.Flags().StringVar(&pprofMode, "pprofmode", "", "[ cpu | mem | mutex | block | trace | http ]")
	RootCmd.Flags().StringVar(&pprofAddr, "pprofaddr", "localhost:6060", "pprof http port")

}

// RootCmd contains the root dmsghttp command
var RootCmd = &cobra.Command{
	Use:   dmsgclient.ExecName(),
	Short: "DMSG http file server",
	Long: calvin.AsciiFont("dmsghttp") + `
	DMSG http file server`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),

	Run: func(_ *cobra.Command, _ []string) {
		server()
	},
}

func server() {
	stopPProf := dmsgcmdutil.InitPProf(dlog, pprofMode, pprofAddr)
	defer stopPProf()

	wg := new(sync.WaitGroup)
	wg.Add(1)

	err = dmsgclient.InitConfig()
	if err != nil {
		dlog.WithError(err).Fatal("Failed to read specified dmsghttp-config")
	}

	pk, err = sk.PubKey()
	if err != nil {
		pk, sk = cipher.GenerateKeyPair()
	}

	for _, key := range wl {
		var pk1 cipher.PubKey
		err := pk1.Set(key)
		if err == nil {
			wlkeys = append(wlkeys, pk1)
		}
	}

	if len(wlkeys) > 0 {
		if len(wlkeys) == 1 {
			dlog.Info(fmt.Sprintf("%d key whitelisted", len(wlkeys)))
		} else {
			dlog.Info(fmt.Sprintf("%d keys whitelisted", len(wlkeys)))
		}
	}

	ctx, cancel := cmdutil.SignalContext(context.Background(), dlog)
	defer cancel()

	httpClient := &http.Client{}

	if proxyAddr != "" {
		// Use SOCKS5 proxy dialer if specified
		dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
		if err != nil {
			dlog.WithError(err).Fatal("Error creating SOCKS5 dialer")
		}
		transport := &http.Transport{
			Dial: dialer.Dial,
		}
		httpClient = &http.Client{
			Transport: transport,
		}
		ctx = context.WithValue(ctx, "socks5_proxy", proxyAddr) //nolint
	}

	var dmsgC *dmsg.Client
	var closeDmsg func()

	dmsgC, closeDmsg, err = dmsgclient.InitDmsgWithFlags(ctx, dlog, pk, sk, httpClient, "")
	if err != nil {
		dlog.WithError(err).Error("Error connecting to dmsg network")
		return
	}
	defer closeDmsg()

	lis, err := dmsgC.Listen(uint16(dmsgPort)) //nolint gosec
	if err != nil {
		dlog.WithError(err).Debug()
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			dlog.WithError(err).Debug()
		}
	}()

	r1 := gin.New()
	// Disable Gin's default logger middleware
	r1.Use(gin.Recovery())
	r1.Use(loggingMiddleware())
	// only whitelisted public keys can access authRoute(s)
	authRoute := r1.Group("/")
	if len(wlkeys) > 0 {
		authRoute.Use(whitelistAuth(wlkeys))
	}

	r1.Static("/", serveDir)

	// Start the server using the custom Gin handler
	serve := &http.Server{
		Handler:           &GinHandler{Router: r1},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	// Gracefully shutdown HTTP server on context cancellation
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := serve.Shutdown(shutdownCtx); err != nil {
			dlog.WithError(err).Warn("Server shutdown error")
		}
	}()

	// Start serving
	go func() {
		dlog.WithField("dmsg_addr", lis.Addr().String()).Debug("Serving...\n")
		if err := serve.Serve(lis); err != nil && err != http.ErrServerClosed {
			dlog.WithError(err).Debug("Server error\n")
		}
		wg.Done()
	}()

	wg.Wait()
}

func whitelistAuth(whitelistedPKs []cipher.PubKey) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get the remote PK.
		remotePK, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			c.Writer.WriteHeader(http.StatusInternalServerError)
			c.Writer.Write([]byte("500 Internal Server Error")) //nolint errcheck
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
			c.Writer.Write([]byte("401 Unauthorized")) //nolint errcheck
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
	}
}

// GinHandler is handler for gin on dmsg http sever
type GinHandler struct {
	Router *gin.Engine
}

func (h *GinHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

		fmt.Printf("[EXAMPLE] %s |%s %3d %s| %13v | %15s | %72s |%s %-7s %s %s\n",
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

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}
