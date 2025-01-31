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

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

const dwsenv = "DMSGWEBSRV"

var dwscfg = os.Getenv(dwsenv)

func init() {
	dmsgPort = scriptExecUintSlice("${DMSGPORT[@]:-80}", dwscfg)
	dmsgSess = scriptExecInt("${DMSGSESSIONS:-1}", dwscfg)
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
	srvCmd.Flags().UintSliceVarP(&localPort, "lport", "p", localPort, "local application interface port(s)\033[0m\n\r")
	srvCmd.Flags().UintSliceVarP(&dmsgPort, "dport", "d", dmsgPort, "DMSG port(s) to serve\033[0m\n\r")
	srvCmd.Flags().StringSliceVarP(&wl, "wl", "w", wl, "whitelisted keys for DMSG authenticated routes\033[0m\n\r")
	srvCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", dmsgDisc, "DMSG discovery URL\033[0m\n\r")
	srvCmd.Flags().StringVarP(&proxyAddr, "proxy", "x", proxyAddr, "connect to DMSG via proxy (e.g., '127.0.0.1:1080')\033[0m\n\r")
	srvCmd.Flags().IntVarP(&dmsgSess, "dsess", "e", dmsgSess, "DMSG sessions\033[0m\n\r")
	srvCmd.Flags().BoolSliceVarP(&rawTCP, "rt", "c", rawTCP, "proxy local port as raw TCP, comma separated\033[0m\n\r")
	srvCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "debug", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m\n\r")
	srvCmd.Flags().BoolVarP(&isEnvs, "envs", "z", false, "show example .conf file\033[0m\n\r")
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
		dLog = logging.MustGetLogger("dmsgwebsrv")
		if len(localPort) != len(dmsgPort) || len(localPort) != len(rawTCP) {
			dLog.Fatal("The number of local ports, DMSG ports, and raw TCP flags must be the same")
		}
		pk, err = sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}
		dLog.Debugf("DMSG client public key: %v", pk.String())

		if len(wl) > 0 {
			for _, key := range wl {
				var pk cipher.PubKey
				if err := pk.Set(key); err == nil {
					wlkeys = append(wlkeys, pk)
				}
			}
			dLog.Infof("%d keys whitelisted", len(wlkeys))
		}

		if proxyAddr != "" {
			var err error
			dialer, err = proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
			if err != nil {
				dLog.Fatalf("Error creating SOCKS5 dialer: %v", err)
			}
			httpClient = &http.Client{Transport: &http.Transport{Dial: dialer.Dial}}
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		server()
	},
}

func server() {

	ctx, cancel := cmdutil.SignalContext(context.Background(), dLog)
	defer cancel()

	dmsgClient := dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, &http.Client{}, dLog), dmsg.DefaultConfig())
	defer func() {
		if err := dmsgClient.Close(); err != nil {
			dLog.WithError(err).Error()
		}
	}()
	go dmsgClient.Serve(ctx)

	select {
	case <-ctx.Done():
		dLog.WithError(ctx.Err()).Warn()
		return
	case <-dmsgClient.Ready():
	}

	wg := sync.WaitGroup{}
	for i := range localPort {
		lis, err := dmsgClient.Listen(uint16(dmsgPort[i])) //nolint
		if err != nil {
			dLog.Fatalf("Error listening on DMSG port %d: %v", dmsgPort[i], err)
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
			dLog.Errorf("HTTP server shutdown error: %v", err)
		}
	}()

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		dLog.Fatalf("HTTP server error: %v", err)
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
					dLog.Errorf("Error accepting connection: %v", err)
					return
				}
			}
			connChan <- conn
		}
	}()

	for {
		select {
		case <-ctx.Done():
			dLog.Info("Shutting down TCP proxy connections...")
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
					dLog.Errorf("Error connecting to local port %d: %v", localPort, err)

					connMutex.Lock()
					delete(activeConns, dmsgConn)
					connMutex.Unlock()

					return
				}
				defer localConn.Close() //nolint

				go func() {
					_, err1 := io.Copy(dmsgConn, localConn)
					if err1 != nil {
						dLog.WithError(err1).Warn("Error on io.Copy(dmsgConn, localConn)")
					}
				}()
				_, err2 := io.Copy(localConn, dmsgConn)
				if err2 != nil {
					dLog.WithError(err2).Warn("Error on io.Copy(localConn, dmsgConn)")
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
#--	DMSGWEB SRV CONFIG TEMPLATE
#--		Defaults shown
#--		Uncomment to change default value
#--		LOCALPORT and DMSGPORT must contain the same number of elements
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
