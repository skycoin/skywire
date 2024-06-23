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
	"syscall"

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
		ctx = context.WithValue(ctx, "port", fmt.Sprintf("%v", webPort)) //nolint
		return ctx, ip, nil
	}
	// Use default name resolution for other domains
	return ctx, nil, nil
}

const dmsgwebenvname = "DMSGWEB"

var dmsgwebconffile = os.Getenv(dmsgwebenvname)

func init() {

	RootCmd.Flags().StringVarP(&filterDomainSuffix, "filter", "f", ".dmsg", "domain suffix to filter")
	RootCmd.Flags().UintVarP(&proxyPort, "socks", "q", scriptExecUint("${PROXYPORT:-4445}", dmsgwebconffile), "port to serve the socks5 proxy")
	RootCmd.Flags().StringVarP(&addProxy, "proxy", "r", scriptExecString("${ADDPROXY}", dmsgwebconffile), "configure additional socks5 proxy for dmsgweb (i.e. 127.0.0.1:1080)")
	RootCmd.Flags().UintSliceVarP(&webPort, "port", "p", scriptExecUintSlice("${WEBPORT[@]:-8080}", dmsgwebconffile), "port(s) to serve the web application")
	RootCmd.Flags().StringSliceVarP(&resolveDmsgAddr, "resolve", "t", scriptExecStringSlice("${RESOLVEPK[@]}", dmsgwebconffile), "resolve the specified dmsg address:port on the local port & disable proxy")
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "d", skyenv.DmsgDiscAddr, "dmsg discovery url")
	RootCmd.Flags().IntVarP(&dmsgSessions, "sess", "e", scriptExecInt("${DMSGSESSIONS:-1}", dmsgwebconffile), "number of dmsg servers to connect to")
	RootCmd.Flags().BoolSliceVarP(&rawTCP, "rt", "c", scriptExecBoolSlice("${RAWTCP[@]:-false}", dmsgwebconffile), "proxy local port as raw TCP")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m")
	if os.Getenv("DMSGWEBSK") != "" {
		sk.Set(os.Getenv("DMSGWEBSK")) //nolint
	}
	if scriptExecString("${DMSGWEBSK}", dmsgwebconffile) != "" {
		sk.Set(scriptExecString("${DMSGWEBSK}", dmsgwebconffile)) //nolint
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
DMSG resolving proxy & browser client - access websites and http interfaces over dmsg` + func() string {
		if _, err := os.Stat(dmsgwebconffile); err == nil {
			return `
dmsgweb conf file detected: ` + dmsgwebconffile
		}
		return `
.conf file may also be specified with
` + dmsgwebenvname + `=/path/to/dmsgweb.conf skywire dmsg web`
	}(),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(cmd *cobra.Command, _ []string) {
		if isEnvs {
			envfile := envfileLinux
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

		if len(resolveDmsgAddr) > 0 && len(webPort) != len(resolveDmsgAddr) {
			dmsgWebLog.Fatal("-resolve --t flag cannot contain a different number of elements than -port -p flag")
		}
		if len(resolveDmsgAddr) == 0 && len(webPort) > 1 {
			dmsgWebLog.Fatal("-port --p flag cannot specify multiple ports without specifying multiple dmsg address:port(s) to -resolve --t flag")
		}

		seenResolveDmsgAddr := make(map[string]bool)
		for _, item := range resolveDmsgAddr {
			if seenResolveDmsgAddr[item] {
				dmsgWebLog.Fatal("-resolve --t flag cannot contain duplicates")
			}
			seenResolveDmsgAddr[item] = true
		}

		seenWebPort := make(map[uint]bool)
		for _, item := range webPort {
			if seenWebPort[item] {
				dmsgWebLog.Fatal("-port --p flag cannot contain duplicates")
			}
			seenWebPort[item] = true
		}

		if len(rawTCP) < len(resolveDmsgAddr) {
			for len(rawTCP) < len(resolveDmsgAddr) {
				rawTCP = append(rawTCP, false)
			}
		} else if len(rawTCP) > len(resolveDmsgAddr) {
			rawTCP = rawTCP[:len(resolveDmsgAddr)]
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
		dmsgWebLog.Info("dmsg client pk: %v", pk.String())

		if len(resolveDmsgAddr) > 0 {
			for i, dmsgaddr := range resolveDmsgAddr {
				dmsgAddr = strings.Split(dmsgaddr, ":")
				err = dialPK[i].Set(dmsgAddr[0])
				if err != nil {
					log.Fatalf("failed to parse dmsg <address>:<port> : %v", err)
				}
				if len(dmsgAddr) > 1 {
					dport, err := strconv.ParseUint(dmsgAddr[1], 10, 64)
					if err != nil {
						log.Fatalf("Failed to parse dmsg port: %v", err)
					}
					dmsgPorts[i] = uint(dport)
				} else {
					dmsgPorts[i] = uint(80)
				}
			}
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
			os.Exit(0)
		}()

		httpC = http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}

		if len(resolveDmsgAddr) == 0 {
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
							port = fmt.Sprintf("%v", webPort)
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

		if len(resolveDmsgAddr) == 0 && len(webPort) == 1 {
			if rawTCP[0] {
				proxyTCPConn(-1)
			} else {
				proxyHTTPConn(-1)
			}
		} else {
			for i := range resolveDmsgAddr {
				if rawTCP[i] {
					proxyTCPConn(i)
				} else {
					proxyHTTPConn(i)
				}
			}
		}
		wg.Wait()
	},
}

func proxyHTTPConn(n int) {
	r := gin.New()

	r.Use(gin.Recovery())

	r.Use(loggingMiddleware())

	r.Any("/*path", func(c *gin.Context) {
		var urlStr string
		if n > -1 {
			urlStr = fmt.Sprintf("dmsg://%s%s", resolveDmsgAddr[n], c.Param("path"))
			if c.Request.URL.RawQuery != "" {
				urlStr = fmt.Sprintf("%s?%s", urlStr, c.Request.URL.RawQuery)
			}
		} else {
			hostParts := strings.Split(c.Request.Host, ":")
			var dmsgp string
			if len(hostParts) > 1 {
				dmsgp = hostParts[1]
			} else {
				dmsgp = "80"
			}
			urlStr = fmt.Sprintf("dmsg://%s:%s%s", strings.TrimRight(hostParts[0], filterDomainSuffix), dmsgp, c.Param("path"))
			if c.Request.URL.RawQuery != "" {
				urlStr = fmt.Sprintf("%s?%s", urlStr, c.Request.URL.RawQuery)
			}
		}

		fmt.Printf("Proxying request: %s %s\n", c.Request.Method, urlStr)
		req, err := http.NewRequest(c.Request.Method, urlStr, c.Request.Body)
		if err != nil {
			c.String(http.StatusInternalServerError, "Failed to create HTTP request")
			return
		}

		for header, values := range c.Request.Header {
			for _, value := range values {
				req.Header.Add(header, value)
			}
		}

		resp, err := httpC.Do(req)
		if err != nil {
			c.String(http.StatusInternalServerError, "Failed to connect to HTTP server")
			fmt.Printf("Error: %v\n", err)
			return
		}
		defer resp.Body.Close() //nolint

		for header, values := range resp.Header {
			for _, value := range values {
				c.Writer.Header().Add(header, value)
			}
		}

		c.Status(resp.StatusCode)
		if _, err := io.Copy(c.Writer, resp.Body); err != nil {
			c.String(http.StatusInternalServerError, "Failed to copy response body")
			fmt.Printf("Error copying response body: %v\n", err)
		}
	})
	wg.Add(1)
	go func() {
		dmsgWebLog.Debug(fmt.Sprintf("Serving http on: http://127.0.0.1:%v", webPort))
		r.Run(":" + fmt.Sprintf("%v", webPort)) //nolint
		dmsgWebLog.Debug(fmt.Sprintf("Stopped serving http on: http://127.0.0.1:%v", webPort))
		wg.Done()
	}()
}
func proxyTCPConn(n int) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", webPort[n]))
	if err != nil {
		log.Fatalf("Failed to start TCP listener on port %d: %v", webPort[n], err)
	}
	defer listener.Close() //nolint
	log.Printf("Serving TCP on 127.0.0.1:%d", webPort[n])

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		wg.Add(1)
		go func(conn net.Conn, n int) {
			defer wg.Done()
			defer conn.Close() //nolint

			dmsgConn, err := dmsgC.DialStream(context.Background(), dmsg.Addr{PK: dialPK[n], Port: uint16(dmsgPorts[n])})
			if err != nil {
				log.Printf("Failed to dial dmsg address %v:%v %v", dialPK[n].String(), dmsgPorts[n], err)
				return
			}
			defer dmsgConn.Close() //nolint

			go func() {
				_, err := io.Copy(dmsgConn, conn)
				if err != nil {
					log.Printf("Error copying data to dmsg server: %v", err)
				}
				dmsgConn.Close() //nolint
			}()

			go func() {
				_, err := io.Copy(conn, dmsgConn)
				if err != nil {
					log.Printf("Error copying data from dmsg server: %v", err)
				}
				conn.Close() //nolint
			}()
		}(conn, n)
	}
}

const envfileLinux = `
#########################################################################
#--	DMSGWEB CONFIG TEMPLATE
#--		Defaults shown
#--		Uncomment to change default value
#--		WEBPORT and DMSGPORT must contain the same number of elements
#########################################################################

#--	Set port for proxy interface
#PROXYPORT=4445

#--	Configure additional socks5 proxy for dmsgweb to use to connect to dmsg
#ADDPROXY='127.0.0.1:1080'

#--	Web Interface Port
#WEBPORT=('8080')

#--	Resove a specific PK to the web port (also disables proxy)
#RESOLVEPK=('')

#--	Use raw tcp mode instead of http (also disables proxy)
#RAWTCP=('false')

#--	Number of dmsg servers to connect to (0 unlimits)
#DMSGSESSIONS=2

#--	Dmsg port to use
#DMSGPORT=('80')

#--	Set secret key
#DMSGWEBSK=''
`
