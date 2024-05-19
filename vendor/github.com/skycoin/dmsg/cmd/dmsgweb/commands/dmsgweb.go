// Package commands cmd/dmsgweb/commands/dmsgweb.go
package commands

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bitfield/script"
	"github.com/confiant-inc/go-socks5"
	"github.com/gin-gonic/gin"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/spf13/cobra"
	"golang.org/x/net/proxy"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
)

type customResolver struct{}

func (r *customResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	// Handle custom name resolution for .dmsg domains
	regexPattern := `\.` + filterDomainSuffix + `(:[0-9]+)?$`
	match, _ := regexp.MatchString(regexPattern, name) //nolint:errcheck
	if match {
		ip := net.ParseIP("127.0.0.1")
		if ip == nil {
			return ctx, nil, fmt.Errorf("failed to parse IP address")
		}
		// Modify the context to include the desired port
		ctx = context.WithValue(ctx, "port", strconv.Itoa(webPort)) //nolint
		return ctx, ip, nil
	}
	// Use default name resolution for other domains
	return ctx, nil, nil
}

var (
	httpC              http.Client
	dmsgDisc           string
	dmsgSessions       int
	filterDomainSuffix string
	sk                 cipher.SecKey
	dmsgWebLog         *logging.Logger
	logLvl             string
	webPort            int
	proxyPort          uint
	addProxy           string
	resolveDmsgAddr    string
	wg                 sync.WaitGroup
	isEnvs             bool
)

const envname = "DMSGWEB"

var envfile = os.Getenv(envname)

func init() {

	RootCmd.Flags().StringVarP(&filterDomainSuffix, "filter", "f", ".dmsg", "domain suffix to filter")
	RootCmd.Flags().UintVarP(&proxyPort, "socks", "q", scriptExecUint("${PROXYPORT:-4445}"), "port to serve the socks5 proxy")
	RootCmd.Flags().StringVarP(&addProxy, "proxy", "r", scriptExecString("${ADDPROXY}"), "configure additional socks5 proxy for dmsgweb (i.e. 127.0.0.1:1080)")
	RootCmd.Flags().IntVarP(&webPort, "port", "p", scriptExecInt("${WEBPORT:-8080}"), "port to serve the web application")
	RootCmd.Flags().StringVarP(&resolveDmsgAddr, "resolve", "t", scriptExecString("${RESOLVEPK}"), "resolve the specified dmsg address:port on the local port & disable proxy")
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "d", skyenv.DmsgDiscAddr, "dmsg discovery url")
	RootCmd.Flags().IntVarP(&dmsgSessions, "sess", "e", scriptExecInt("${DMSGSESSIONS:-1}"), "number of dmsg servers to connect to")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m")
	if os.Getenv("DMSGWEB_SK") != "" {
		sk.Set(os.Getenv("DMSGWEB_SK")) //nolint
	}
	if scriptExecString("${DMSGWEB_SK}") != "" {
		sk.Set(scriptExecString("${DMSGWEB_SK}")) //nolint
	}
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	RootCmd.Flags().BoolVarP(&isEnvs, "envs", "z", false, "show example .conf file")

}

// RootCmd contains the root command for dmsgweb
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
	Short: "DMSG resolving proxy & browser client",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐┬ ┬┌─┐┌┐
	 │││││└─┐│ ┬│││├┤ ├┴┐
	─┴┘┴ ┴└─┘└─┘└┴┘└─┘└─┘
DMSG resolving proxy & browser client - access websites over dmsg` + func() string {
		if _, err := os.Stat(envfile); err == nil {
			return `
dmsgweb env file detected: ` + envfile
		}
		return `
.conf file may also be specified with
` + envname + `=/path/to/dmsgweb.conf skywire dmsg web`
	}(),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(cmd *cobra.Command, _ []string) {
		if isEnvs {
			if runtime.GOOS == "windows" {
				envfile = envfileWindows
			} else {
				envfile = envfileLinux
			}
			fmt.Println(envfile)
			os.Exit(0)
		}
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM) //nolint
		go func() {
			<-c
			os.Exit(1)
		}()
		if dmsgWebLog == nil {
			dmsgWebLog = logging.MustGetLogger("dmsgweb")
		}
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		if filterDomainSuffix == "" {
			dmsgWebLog.Fatal("domain suffix to filter cannot be an empty string")
		}
		if dmsgDisc == "" {
			dmsgDisc = skyenv.DmsgDiscAddr
		}
		ctx, cancel := cmdutil.SignalContext(context.Background(), dmsgWebLog)
		defer cancel()

		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}

		dmsgC, closeDmsg, err := startDmsg(ctx, pk, sk)
		if err != nil {
			dmsgWebLog.WithError(err).Fatal("failed to start dmsg")
		}
		defer closeDmsg()

		go func() {
			<-ctx.Done()
			cancel()
			closeDmsg()
			os.Exit(0) //this should not be necessary
		}()

		httpC = http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}

		if resolveDmsgAddr == "" {
			// Create a SOCKS5 server with custom name resolution
			conf := &socks5.Config{
				Resolver: &customResolver{},
				Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
					host, _, err := net.SplitHostPort(addr)
					if err != nil {
						return nil, err
					}
					regexPattern := `\` + filterDomainSuffix + `(:[0-9]+)?$`
					match, _ := regexp.MatchString(regexPattern, host) //nolint:errcheck
					if match {
						port, ok := ctx.Value("port").(string)
						if !ok {
							port = strconv.Itoa(webPort)
						}
						addr = "localhost:" + port
					} else {
						if addProxy != "" {
							// Fallback to another SOCKS5 proxy
							dialer, err := proxy.SOCKS5("tcp", addProxy, nil, proxy.Direct)
							if err != nil {
								return nil, err
							}
							return dialer.Dial(network, addr)
						}
					}
					dmsgWebLog.Debug("Dialing address:", addr)
					return net.Dial(network, addr)
				},
			}

			// Start the SOCKS5 server
			socksAddr := fmt.Sprintf("127.0.0.1:%v", proxyPort)
			log.Printf("SOCKS5 proxy server started on %s", socksAddr)

			server, err := socks5.New(conf)
			if err != nil {
				log.Fatalf("Failed to create SOCKS5 server: %v", err)
			}

			wg.Add(1)
			go func() {
				dmsgWebLog.Debug("Serving SOCKS5 proxy on " + socksAddr)
				err := server.ListenAndServe("tcp", socksAddr)
				if err != nil {
					log.Fatalf("Failed to start SOCKS5 server: %v", err)
				}
				defer server.Close()
				dmsgWebLog.Debug("Stopped serving SOCKS5 proxy on " + socksAddr)
			}()
		}
		r := gin.New()

		r.Use(gin.Recovery())

		r.Use(loggingMiddleware())

		r.Any("/*path", func(c *gin.Context) {
			var urlStr string
			if resolveDmsgAddr != "" {
				urlStr = fmt.Sprintf("dmsg://%s%s", resolveDmsgAddr, c.Param("path"))
			} else {

				hostParts := strings.Split(c.Request.Host, ":")
				var dmsgp string
				if len(hostParts) > 1 {
					dmsgp = hostParts[1]
				} else {
					dmsgp = "80"
				}
				urlStr = fmt.Sprintf("dmsg://%s:%s%s", strings.TrimRight(hostParts[0], filterDomainSuffix), dmsgp, c.Param("path"))
			}
			req, err := http.NewRequest(http.MethodGet, urlStr, nil)
			if err != nil {
				c.String(http.StatusInternalServerError, "Failed to create HTTP request")
				return
			}
			resp, err := httpC.Do(req)
			if err != nil {
				c.String(http.StatusInternalServerError, "Failed to connect to HTTP server")
				return
			}
			defer resp.Body.Close() //nolint
			c.Status(http.StatusOK)
			io.Copy(c.Writer, resp.Body) //nolint
		})
		wg.Add(1)
		go func() {
			dmsgWebLog.Debug(fmt.Sprintf("Serving http on: http://127.0.0.1:%v", webPort))
			r.Run(":" + strconv.Itoa(webPort)) //nolint
			dmsgWebLog.Debug(fmt.Sprintf("Stopped serving http on: http://127.0.0.1:%v", webPort))
			wg.Done()
		}()
		wg.Wait()
	},
}

func startDmsg(ctx context.Context, pk cipher.PubKey, sk cipher.SecKey) (dmsgC *dmsg.Client, stop func(), err error) {
	dmsgC = dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, &http.Client{}, dmsgWebLog), &dmsg.Config{MinSessions: dmsgSessions})
	go dmsgC.Serve(context.Background())

	stop = func() {
		err := dmsgC.Close()
		dmsgWebLog.WithError(err).Debug("Disconnected from dmsg network.")
		fmt.Printf("\n")
	}
	dmsgWebLog.WithField("public_key", pk.String()).WithField("dmsg_disc", dmsgDisc).
		Debug("Connecting to dmsg network...")

	select {
	case <-ctx.Done():
		stop()
		os.Exit(0)
		return nil, nil, ctx.Err()

	case <-dmsgC.Ready():
		dmsgWebLog.Debug("Dmsg network ready.")
		return dmsgC, stop, nil
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

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

func scriptExecString(s string) string {
	if runtime.GOOS == "windows" {
		var variable, defaultvalue string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
			defaultvalue = strings.TrimRight(parts[1], "}")
		} else {
			variable = s
			defaultvalue = ""
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, envfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return defaultvalue
			}
			return strings.TrimRight(out, "\n")
		}
		return defaultvalue
	}
	z, err := script.Exec(fmt.Sprintf(`sh -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, envfile, s)).String()
	if err == nil {
		return strings.TrimSpace(z)
	}
	return ""
}

func scriptExecInt(s string) int {
	if runtime.GOOS == "windows" {
		var variable string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
		} else {
			variable = s
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, envfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return 0
			}
			i, err := strconv.Atoi(strings.TrimSpace(strings.TrimRight(out, "\n")))
			if err == nil {
				return i
			}
			return 0
		}
		return 0
	}
	z, err := script.Exec(fmt.Sprintf(`sh -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, envfile, s)).String()
	if err == nil {
		if z == "" {
			return 0
		}
		i, err := strconv.Atoi(z)
		if err == nil {
			return i
		}
	}
	return 0
}
func scriptExecUint(s string) uint {
	if runtime.GOOS == "windows" {
		var variable string
		if strings.Contains(s, ":-") {
			parts := strings.SplitN(s, ":-", 2)
			variable = parts[0] + "}"
		} else {
			variable = s
		}
		out, err := script.Exec(fmt.Sprintf(`powershell -c '$SKYENV = "%s"; if ($SKYENV -ne "" -and (Test-Path $SKYENV)) { . $SKYENV }; echo %s"`, envfile, variable)).String()
		if err == nil {
			if (out == "") || (out == variable) {
				return 0
			}
			i, err := strconv.Atoi(strings.TrimSpace(strings.TrimRight(out, "\n")))
			if err == nil {
				return uint(i)
			}
			return 0
		}
		return 0
	}
	z, err := script.Exec(fmt.Sprintf(`sh -c 'SKYENV=%s ; if [[ $SKYENV != "" ]] && [[ -f $SKYENV ]] ; then source $SKYENV ; fi ; printf "%s"'`, envfile, s)).String()
	if err == nil {
		if z == "" {
			return 0
		}
		i, err := strconv.Atoi(z)
		if err == nil {
			return uint(i)
		}
	}
	return uint(0)
}

const envfileLinux = `
#########################################################################
#	DMSGWEB CONFIG TEMPLATE
#		Defaults shown
#		Uncomment to change default value
#########################################################################

#--	Set port for proxy interface
#PROXYPORT=4445

#--	Configure additional proxy for dmsgvlc to use
#ADDPROXY='127.0.0.1:1080'

#--	Web Interface Port
#WEBPORT=8080

#--	Resove a specific PK to the web port (also disables proxy)
#RESOLVEPK=''

#--	Number of dmsg servers to connect to (0 unlimits)
#DMSGSESSIONS=1

#--	Set secret key
#DMSGWEB_SK=''
`
const envfileWindows = `
#########################################################################
#	DMSGWEB CONFIG TEMPLATE
#		Defaults shown
#		Uncomment to change default value
#########################################################################

#--	Set port for proxy interface
#$PROXYPORT=4445

#--	Configure additional proxy for dmsgvlc to use
#$ADDPROXY='127.0.0.1:1080'

#--	Web Interface Port
#$WEBPORT=8080

#--	Resove a specific PK to the web port (also disables proxy)
#$RESOLVEPK=''

#--	Number of dmsg servers to connect to (0 unlimits)
#$DMSGSESSIONS=1

#--	Set secret key
#$DMSGWEB_SK=''
`
