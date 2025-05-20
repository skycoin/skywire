// Package commands cmd/dmsgweb/commands/dmsgwebsrv.go
package commands

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
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

const dwsenv = "DMSGWEBSRV"

var dwscfg = os.Getenv(dwsenv)

func init() {
	dmsgPort = scriptExecUintSlice("${DMSGPORT[@]:-80}", dwscfg)
	wl = scriptExecStringSlice("${WHITELISTPKS[@]}", dwscfg)
	localPort = scriptExecUintSlice("${LOCALPORT[@]:-8086}", dwscfg)
	rawTCP = scriptExecBoolSlice("${RAWTCP[@]:-false}", dwscfg)
	if os.Getenv("DMSGWEBSRVSK") != "" {
		sk.Set(os.Getenv("DMSGWEBSRVSK")) //nolint
	}
	if scriptExecString("${DMSGWEBSRVSK}", dwscfg) != "" {
		sk.Set(scriptExecString("${DMSGWEBSRVSK}", dwscfg)) //nolint
	}
	pk, _ = sk.PubKey() //nolint

	RootCmd.AddCommand(srvCmd)
	flags.InitFlags(srvCmd)
	srvCmd.Flags().UintSliceVarP(&localPort, "lport", "p", localPort, "local application interface port(s)\033[0m\n\r")
	srvCmd.Flags().UintSliceVarP(&dmsgPort, "dport", "d", dmsgPort, "DMSG port(s) to serve\033[0m\n\r")
	srvCmd.Flags().StringSliceVarP(&wl, "wl", "w", wl, "whitelisted keys for DMSG authenticated routes"+func() string {
		if len(wl) > 0 {
			return "\033[0m\n\r"
		}
		return ""
	}())
	srvCmd.Flags().StringVarP(&proxyAddr, "proxy", "x", proxyAddr, "connect to DMSG via proxy (e.g., '127.0.0.1:1080')")
	srvCmd.Flags().BoolSliceVarP(&rawTCP, "rt", "c", rawTCP, "proxy local port as raw TCP, comma separated"+func() string {
		if len(rawTCP) > 0 {
			return "\033[0m\n\r"
		}
		return ""
	}())
	srvCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "debug", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m\n\r")
	srvCmd.Flags().BoolVarP(&isEnvs, "envs", "E", false, "show example .conf file")
	srvCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\033[0m\n\r")
	srvCmd.CompletionOptions.DisableDefaultCmd = true
}

var srvCmd = &cobra.Command{
	Use:   "srv",
	Short: "Serve HTTP or raw TCP from local port over DMSG",
	Long: `DMSG web server - serve HTTP or raw TCP interface from local port over DMSG` + func() string {
		if _, err := os.Stat(dwscfg); err == nil {
			return "\n\t.dmsenv file detected: " + dwscfg
		}
		return "\n\t.conf file may also be specified with " + dwsenv + `=/path/to/dmsgwebsrv.conf skywire dmsg web srv`
	}(),
	PreRun: func(_ *cobra.Command, _ []string) {
		if isEnvs {
			printEnvs(srvenvfileLinux)
		}
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}
		dlog = logging.MustGetLogger("dmsgwebsrv")

		err = flags.InitConfig()
		if err != nil {
			dlog.WithError(err).Fatal("Failed to read specified dmsghttp-config")
		}

		if len(localPort) != len(dmsgPort) || len(localPort) != len(rawTCP) {
			dlog.Fatal("The number of local ports, DMSG ports, and raw TCP bools must be the same")
		}
		pk, err = sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}
		dlog.Debugf("DMSG client public key: %v", pk.String())

		if len(wl) > 0 {
			for _, key := range wl {
				var pk cipher.PubKey
				if err := pk.Set(key); err == nil {
					wlkeys = append(wlkeys, pk)
				}
			}
			dlog.Infof("%d keys whitelisted", len(wlkeys))
		}

	},
	Run: func(_ *cobra.Command, _ []string) {
		server()
	},
}

func server() {

	ctx, cancel := cmdutil.SignalContext(context.Background(), dlog)
	defer cancel()

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

	wg := sync.WaitGroup{}
	for i := range localPort {
		lis, err := dmsgC.Listen(uint16(dmsgPort[i])) //nolint
		if err != nil {
			dlog.Fatalf("Error listening on DMSG port %d: %v", dmsgPort[i], err)
		}
		wg.Add(1)
		go func(ctx context.Context, localPort uint, rawTCP bool, listener net.Listener) {
			defer wg.Done()
			defer listener.Close() //nolint

			if rawTCP {
				proxyTCPConnections(ctx, localPort, listener)
			} else {
				proxyHTTPConnections(ctx, localPort, listener)
			}
		}(ctx, localPort[i], rawTCP[i], lis)
	}
	wg.Wait()
}

func proxyHTTPConnections(ctx context.Context, localPort uint, listener net.Listener) {
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(loggingMiddleware())

	authRoute := router.Group("/")
	if len(wlkeys) > 0 {
		authRoute.Use(whitelistAuth(wlkeys))
	}
	authRoute.Any("/*path", func(c *gin.Context) {
		targetURL := fmt.Sprintf("http://127.0.0.1:%d%s?%s", localPort, c.Request.URL.Path, c.Request.URL.RawQuery)
		proxy := httputil.ReverseProxy{Director: func(req *http.Request) {
			req.URL, _ = url.Parse(targetURL) //nolint
			req.Host = req.URL.Host
		}}
		proxy.ServeHTTP(c.Writer, c.Request)
	})

	server := &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// Graceful shutdown on context cancellation
	go func() {
		<-ctx.Done()
		if err := server.Shutdown(context.Background()); err != nil {
			dlog.Errorf("HTTP server shutdown error: %v", err)
		}
	}()

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		dlog.Fatalf("HTTP server error: %v", err)
	}
}

func proxyTCPConnections(ctx context.Context, localPort uint, listener net.Listener) {
	// To track active connections for cleanup
	var connWg sync.WaitGroup
	connChan := make(chan net.Conn)
	activeConns := make(map[net.Conn]struct{})
	connMutex := &sync.Mutex{} // Protect access to activeConns

	// Goroutine to accept new connections
	go func() {
		defer close(connChan)
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					// Listener closed due to context cancellation
					return
				default:
					dlog.Errorf("Error accepting connection: %v", err)
					return
				}
			}
			connChan <- conn
		}
	}()

	for {
		select {
		case <-ctx.Done():
			dlog.Info("Shutting down TCP proxy connections...")
			listener.Close() //nolint

			connMutex.Lock()
			for conn := range activeConns {
				conn.Close() //nolint
			}
			connMutex.Unlock()

			connWg.Wait()
			return

		case conn, ok := <-connChan:
			if !ok {
				return
			}

			connMutex.Lock()
			activeConns[conn] = struct{}{}
			connMutex.Unlock()

			connWg.Add(1)
			go func(dmsgConn net.Conn) {
				defer connWg.Done()
				defer dmsgConn.Close() //nolint

				localConn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
				if err != nil {
					dlog.Errorf("Error connecting to local port %d: %v", localPort, err)

					connMutex.Lock()
					delete(activeConns, dmsgConn)
					connMutex.Unlock()

					return
				}
				defer localConn.Close() //nolint

				go func() {
					_, err1 := io.Copy(dmsgConn, localConn)
					if err1 != nil {
						dlog.WithError(err1).Warn("Error on io.Copy(dmsgConn, localConn)")
					}
				}()
				_, err2 := io.Copy(localConn, dmsgConn)
				if err2 != nil {
					dlog.WithError(err2).Warn("Error on io.Copy(localConn, dmsgConn)")
				}

				connMutex.Lock()
				delete(activeConns, dmsgConn)
				connMutex.Unlock()
			}(conn)
		}
	}
}

const srvenvfileLinux = `
#########################################################################
#--	DMSG WEB SRV CONFIG TEMPLATE
#--		Defaults shown
#--		Uncomment to change default value
#--		LOCALPORT, DMSGPORT, and RAWTCP must contain the same number of elements
#########################################################################

#--	DMSG port to serve
#DMSGPORT=('80')

#--	Local Port to serve over dmsg
#LOCALPORT=('8086')

#--	Number of dmsg servers to connect to (0 unlimits)
#DMSGSESSIONS=1

#--	Set secret key
#DMSGWEBSRVSK=''

#--	Whitelisted keys to access the web interface
#WHITELISTPKS=('')

#-- Proxy as raw TCP
#RAWTCP=('false')
`
