// Package commands cmd/dmsgweb/commands/dmsgweb.go
package commands

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/chen3feng/safecast"
	"github.com/confiant-inc/go-socks5"
	"github.com/gin-gonic/gin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"
	"golang.org/x/net/proxy"

	"github.com/skycoin/dmsg/internal/cli"
	"github.com/skycoin/dmsg/internal/flags"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
)

type customResolver struct{}

func (r *customResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	regexPattern := `\.` + filterDomainSuffix + `(:[0-9]+)?$`
	match, _ := regexp.MatchString(regexPattern, name) //nolint:errcheck
	if match {
		ip := net.ParseIP("127.0.0.1")
		if ip == nil {
			return ctx, nil, fmt.Errorf("failed to parse IP address")
		}
		ctx = context.WithValue(ctx, "port", fmt.Sprintf("%v", webPort)) //nolint
		return ctx, ip, nil
	}
	return ctx, nil, nil
}

const dwenv = "DMSGWEB"

var dwcfg = os.Getenv(dwenv)

func init() {
	webPort = scriptExecUintSlice("${WEBPORT[@]:-8080}", dwcfg)
	proxyPort = scriptExecUint("${PROXYPORT:-4445}", dwcfg)
	addProxy = scriptExecString("${ADDPROXY}", dwcfg)
	resolveDmsgAddr = scriptExecStringSlice("${RESOLVEPK[@]}", dwcfg)
	rawTCP = scriptExecBoolSlice("${RAWTCP[@]:-false}", dwcfg)
	if os.Getenv("DMSGWEBSK") != "" {
		sk.Set(os.Getenv("DMSGWEBSK")) //nolint
	}
	if scriptExecString("${DMSGWEBSK}", dwcfg) != "" {
		sk.Set(scriptExecString("${DMSGWEBSK}", dwcfg)) //nolint
	}
	pk, _ = sk.PubKey() //nolint

	flags.InitFlags(RootCmd)
	RootCmd.Flags().StringVarP(&filterDomainSuffix, "filter", "f", ".dmsg", "domain suffix to filter\033[0m\n\r")
	RootCmd.Flags().UintVarP(&proxyPort, "socks", "q", proxyPort, "port to serve the socks5 proxy\033[0m\n\r")
	RootCmd.Flags().StringVarP(&addProxy, "addproxy", "r", addProxy, "configure additional socks5 proxy for dmsgweb (i.e. 127.0.0.1:1080)\033[0m\n\r")
	RootCmd.Flags().UintSliceVarP(&webPort, "port", "p", webPort, "port(s) to serve the web application\033[0m\n\r")
	RootCmd.Flags().StringSliceVarP(&resolveDmsgAddr, "resolve", "t", resolveDmsgAddr, "resolve the specified dmsg address:port on the local port & disable proxy\033[0m\n\r")
	RootCmd.Flags().StringVarP(&proxyAddr, "proxy", "x", "", "connect to DMSG via proxy (i.e. '127.0.0.1:1080')\033[0m\n\r")
	RootCmd.Flags().BoolSliceVarP(&rawTCP, "rt", "c", rawTCP, "proxy to local port as raw TCP, comma separated\033[0m\n\r")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "debug", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m\n\r")
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	RootCmd.Flags().BoolVarP(&isEnvs, "envs", "E", false, "show example .conf file\033[0m\n\r")

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
DMSG resolving proxy & browser client - access websites, HTTP & TCP interfaces over DMSG` + func() string {
		if _, err := os.Stat(dwcfg); err == nil {
			return `
dmsgweb conf file detected: ` + dwcfg
		}
		return `
.conf file may also be specified with
` + dwenv + `=/path/to/dmsgweb.conf skywire dmsg web`
	}(),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	PreRun: func(_ *cobra.Command, _ []string) {
		if isEnvs {
			printEnvs(srvenvfileLinux)
		}
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}
		dlog = logging.MustGetLogger("dmsgweb")
		if flags.DmsgDiscURL == "" {
			dlog.Fatal("Dmsg Discovery Server URL not specified")
		}
		if flags.DmsgDiscURL == "" {
			dlog.Fatal("Dmsg Discovery Server dmsg address not specified")
		}

		if len(resolveDmsgAddr) > 0 && len(webPort) != len(resolveDmsgAddr) {
			dlog.Fatal("--resolve -t flag cannot contain a different number of elements than -port -p flag")
		}
		if len(resolveDmsgAddr) == 0 && len(webPort) > 1 {
			dlog.Fatal("--port -p flag cannot specify multiple ports without specifying multiple dmsg address:port(s) to -resolve --t flag")
		}

		seenResolveDmsgAddr := make(map[string]bool)
		for _, item := range resolveDmsgAddr {
			if seenResolveDmsgAddr[item] {
				dlog.Fatal("--resolve -t flag cannot contain duplicates")
			}
			seenResolveDmsgAddr[item] = true
		}

		seenWebPort := make(map[uint]bool)
		for _, item := range webPort {
			if seenWebPort[item] {
				dlog.Fatal("--port -p flag cannot contain duplicates")
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
		if len(webPort) == 0 {
			dlog.Fatal("webPort is empty. Ensure at least one port is specified.")
		}
		if filterDomainSuffix == "" {
			dlog.Fatal("domain suffix to filter cannot be an empty string")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		//		c := make(chan os.Signal, 1)
		//		signal.Notify(c, os.Interrupt, syscall.SIGTERM) //nolint
		//		go func() {
		//			<-c
		//			os.Exit(0)
		//		}()

		ctx, cancel := cmdutil.SignalContext(context.Background(), dlog)
		defer cancel()

		pk, err = sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}
		dlog.Info("dmsg client pk: ", pk.String())
		if len(resolveDmsgAddr) > 0 {
			dialPK = make([]cipher.PubKey, len(resolveDmsgAddr))
			dmsgPorts = make([]uint, len(resolveDmsgAddr))

			for i, dmsgaddr := range resolveDmsgAddr {
				dlog.Info("dmsg address to dial: ", dmsgaddr)

				// Split the address into public key and port
				parts := strings.Split(dmsgaddr, ":")
				if len(parts) < 1 || parts[0] == "" {
					dlog.Fatal("Invalid dmsg address format. Expected <public_key>[:<port>]")
				}

				// Parse the public key
				var pk cipher.PubKey
				if err := pk.Set(parts[0]); err != nil {
					dlog.WithError(err).Fatal("Failed to parse public key from dmsg address")
				}
				dialPK[i] = pk
				dlog.Info("Parsed public key: ", pk)

				// Parse the port or use the default (80)
				port := uint(80) // Default port
				if len(parts) > 1 && parts[1] != "" {
					parsedPort, err := strconv.ParseUint(parts[1], 10, 16) // Ports are 16-bit unsigned integers
					if err != nil {
						dlog.WithError(err).Fatal("Failed to parse dmsg port")
					}
					port = uint(parsedPort)
				}
				dmsgPorts[i] = port
				dlog.Info("Parsed port: ", port)
			}
		}

		httpClient = &http.Client{}

		if proxyAddr != "" {
			dialer, err = proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
			if err != nil {
				dlog.WithError(err).Fatal("Error creating SOCKS5 dialer")
			}
			transport := &http.Transport{
				Dial: dialer.Dial,
			}
			httpClient = &http.Client{
				Transport: transport,
			}
			ctx = context.WithValue(context.Background(), "socks5_proxy", proxyAddr) //nolint
		}

		if flags.UseDC {
			dmsgC, closeDmsg, err = cli.StartDmsgDirect(ctx, dlog, pk, sk, httpClient, "", flags.DmsgSessions, pk.String())
		} else {
			if flags.UseHTTP {
				dmsgC, closeDmsg, err = cli.StartDmsg(ctx, dlog, pk, sk, httpClient, flags.DmsgDiscURL, flags.DmsgSessions)
			} else {
				var dmsgDC *dmsg.Client
				var closeDmsgDC func()
				dmsgDC, closeDmsgDC, err = cli.StartDmsgDirect(ctx, dlog, pk, sk, httpClient, "", flags.DmsgSessions, dmsg.ExtractPKFromDmsgAddr(flags.DmsgDiscAddr))
				if err != nil {
					dlog.WithError(err).Error("Error connecting to dmsg network")
					return
				}
				defer closeDmsgDC()
				dmsgHTTP := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgDC)}
				dmsgC, closeDmsg, err = cli.StartDmsg(ctx, dlog, pk, sk, dmsgHTTP, flags.DmsgDiscAddr, flags.DmsgSessions)
			}
		}
		if err != nil {
			dlog.WithError(err).Error("Error connecting to dmsg network")
			return
		}
		defer closeDmsg()

		/*
			go func() {
				<-ctx.Done()
				cancel()
				closeDmsg()
				os.Exit(0)
			}()
		*/
		httpC = http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}

		if len(resolveDmsgAddr) == 0 {
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
							dialer, err := proxy.SOCKS5("tcp", addProxy, nil, proxy.Direct)
							if err != nil {
								return nil, err
							}
							return dialer.Dial(network, addr)
						}
					}
					dlog.Debug("Dialing address:", addr)
					return net.Dial(network, addr)
				},
			}

			socksAddr := fmt.Sprintf("127.0.0.1:%v", proxyPort)
			dlog.Debug("SOCKS5 proxy server started on", socksAddr)

			server, err := socks5.New(conf)
			if err != nil {
				dlog.WithError(err).Fatal("Failed to create SOCKS5 server")
			}

			wg.Add(1)
			go func() {
				dlog.Debug("Serving SOCKS5 proxy on " + socksAddr)
				err := server.ListenAndServe("tcp", socksAddr)
				if err != nil {
					dlog.WithError(err).Fatal("Failed to start SOCKS5 server")
				}
				defer server.Close() //nolint
				dlog.Debug("Stopped serving SOCKS5 proxy on " + socksAddr)
			}()
		}

		if len(resolveDmsgAddr) == 0 && len(webPort) == 1 {
			if rawTCP[0] {
				dlog.Debug("proxyTCPConn(-1)")
				proxyTCPConn(-1)
			} else {
				dlog.Debug("proxyHTTPConn(-1)")
				proxyHTTPConn(-1)
			}
		} else {
			for i := range resolveDmsgAddr {
				wg.Add(1)
				if rawTCP[i] {
					dlog.Debug("proxyTCPConn(" + fmt.Sprintf("%v", i) + ")")
					go proxyTCPConn(i)
				} else {
					dlog.Debug("proxyHTTPConn(" + fmt.Sprintf("%v", i) + ")")
					go proxyHTTPConn(i)
				}
			}
		}
		wg.Wait()
	},
}

func proxyTCPConn(n int) {
	var thiswebport uint
	if n == -1 {
		thiswebport = webPort[0]
	} else {
		thiswebport = webPort[n]
	}
	listener, err := net.Listen("tcp", fmt.Sprintf(":%v", thiswebport))
	if err != nil {
		dlog.WithError(err).Fatal(fmt.Sprintf("Failed to start TCP listener on port: %v", thiswebport))
	}
	defer listener.Close() //nolint
	dlog.Debug("Serving TCP on 127.0.0.1:", thiswebport)
	if dmsgC == nil {
		dlog.Fatal("dmsgC is nil")
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			dlog.WithError(err).Warn("Failed to accept connection")
			continue
		}

		go func(conn net.Conn, n int, dmsgC *dmsg.Client) {
			defer conn.Close() //nolint
			dp, ok := safecast.To[uint16](dmsgPorts[n])
			if !ok {
				dlog.Fatal("uint16 overflow when converting dmsg port")
			}
			dlog.Debug(fmt.Sprintf("Dialing %v:%v", dialPK[n].String(), dp))
			dmsgConn, err := dmsgC.DialStream(context.Background(), dmsg.Addr{PK: dialPK[n], Port: dp}) //nolint
			if err != nil {
				dlog.WithError(err).Warn(fmt.Sprintf("Failed to dial dmsg address %v port %v", dialPK[n].String(), dmsgPorts[n]))
				return
			}

			defer dmsgConn.Close() //nolint

			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				defer wg.Done()
				_, err := io.Copy(dmsgConn, conn)
				if err != nil {
					dlog.WithError(err).Warn("Error on io.Copy(dmsgConn, conn)")
				}
			}()

			go func() {
				defer wg.Done()
				_, err := io.Copy(conn, dmsgConn)
				if err != nil {
					dlog.WithError(err).Warn("Error on io.Copy(conn, dmsgConn)")
				}
			}()

			wg.Wait()
		}(conn, n, dmsgC)
	}
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

		dlog.Debug(fmt.Sprintf("Proxying request: %s %s", c.Request.Method, urlStr))
		req, err := http.NewRequest(c.Request.Method, urlStr, c.Request.Body)
		if err != nil {
			c.String(http.StatusInternalServerError, "Failed to create HTTP request")
			dlog.WithError(err).Warn("Failed to create HTTP request")
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
			dlog.WithError(err).Warn("Failed to connect to HTTP server")
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
			dlog.WithError(err).Warn("Failed to copy response body")
		}
	})
	wg.Add(1)
	go func() {
		var thiswebport uint
		if n == -1 {
			thiswebport = webPort[0]
		} else {
			thiswebport = webPort[n]
		}
		dlog.Debug(fmt.Sprintf("Serving http on: http://127.0.0.1:%v", thiswebport))
		r.Run(":" + fmt.Sprintf("%v", thiswebport)) //nolint
		dlog.Debug(fmt.Sprintf("Stopped serving http on: http://127.0.0.1:%v", thiswebport))
		wg.Done()
	}()
}

const envfileLinux = //nolint unused
`
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

#--	Dmsg port to use
#DMSGPORT=('80')

#--	Set secret key
#DMSGWEBSK=''
`
