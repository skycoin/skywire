// Package commands cmd/apps/skysocks-client/commands/skysocks-client.go c4-app-proxy
package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/elazarl/goproxy"
	ipc "github.com/james-barrow/golang-ipc"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/calvin"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skysocks"
)

const (
	netType    = appnet.TypeSkynet
	serverPort = routing.Port(3) // skysocks server port
)

var (
	r              *netutil.Retrier
	addr           string
	serverPK       string
	httpAddr       string
	retryDelay     int64
	tries          int64
	appPort        uint16
	reconnect      bool
	reconnectDelay int64
	direct         bool
	dmsgFallback   bool
	tunnels        int64
)

func init() {
	launcher.RegisterApp("skysocks-client", RunSkysocksClient)
	RootCmd.Flags().StringVar(&addr, "addr", skyenv.SkysocksClientAddr, "Client address to listen on")
	RootCmd.Flags().StringVar(&serverPK, "srv", "", "PubKey of the server to connect to")
	RootCmd.Flags().StringVar(&httpAddr, "http", "", "http proxy mode")
	RootCmd.Flags().Int64Var(&tries, "tries", 3, "number of tries")
	RootCmd.Flags().Int64Var(&retryDelay, "retry-time", 5, "delay between each try")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
	// In-proc reconnect loop. When set, the proc does not exit
	// on dial / stream failure — it sleeps --reconnect-delay
	// seconds and re-dials. Closes the ~3-5s gap that the
	// restart_policy:always cycle leaves during remote-visor
	// restarts (e.g., upstream auto-update). The visor-side
	// restart_policy is still useful as a backstop for unrecov-
	// erable proc-level failures.
	RootCmd.Flags().BoolVar(&reconnect, "reconnect", false, "in-process reconnect on stream failure (vs exiting)")
	RootCmd.Flags().Int64Var(&reconnectDelay, "reconnect-delay", 2, "seconds between in-process reconnect attempts")
	RootCmd.Flags().BoolVar(&direct, "direct", false, "force a direct-transport-only route to the server (create the transport on demand, dial 1-hop, bypass the route-finder + setup node); self-heals when the server restarts")
	RootCmd.Flags().BoolVar(&dmsgFallback, "dmsg-fallback", false, "if the skynet (route) dial to the server fails, fall back to a direct dmsg stream (opt-in: dmsg relays via a dmsg server — higher latency + the server sees both endpoint PKs)")
	// N independent tunnels (route group + noise + yamux each); browser conns are
	// striped across them by the least-loaded policy so throughput sums. Default 1
	// == the single-tunnel pre-aggregation behavior. See docs/mux_aggregation_rfc.md.
	RootCmd.Flags().Int64Var(&tunnels, "tunnels", 1, "number of independent tunnels to stripe connections across (1 = today's behavior; >1 aggregates ONLY over disjoint routes — see --help)")
}

// RootCmd is the root command for skysocks
var RootCmd = &cobra.Command{
	Use:                   "skysocks-client",
	Short:                 "skywire socks5 proxy client application",
	Long:                  calvin.AsciiFont("skysocks-client"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunSkysocksClient(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// RunSkysocksClient runs the skysocks client app logic.
func RunSkysocksClient(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skysocks-client", pflag.ContinueOnError)
		fs.StringVar(&addr, "addr", skyenv.SkysocksClientAddr, "Client address")
		fs.StringVar(&serverPK, "srv", "", "PubKey of server")
		fs.StringVar(&httpAddr, "http", "", "http proxy mode")
		fs.Int64Var(&tries, "tries", 3, "number of tries")
		fs.Int64Var(&retryDelay, "retry-time", 5, "delay between tries")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		fs.BoolVar(&reconnect, "reconnect", false, "in-process reconnect on stream failure")
		fs.Int64Var(&reconnectDelay, "reconnect-delay", 2, "seconds between reconnect attempts")
		fs.BoolVar(&direct, "direct", false, "force a direct-transport-only route to the server (1-hop, bypass the route-finder + setup node); self-heals on server restart")
		fs.BoolVar(&dmsgFallback, "dmsg-fallback", false, "fall back to a direct dmsg stream if the skynet dial fails")
		fs.Int64Var(&tunnels, "tunnels", 1, "number of independent tunnels to stripe connections across")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()
	log := appCl.Log()

	r = netutil.NewRetrier(log, time.Duration(retryDelay)*time.Second, netutil.DefaultMaxBackoff, tries, 1)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	port := appCl.Config().RoutingPort
	if appPort != 0 {
		port = routing.Port(appPort)
	}
	setAppPort(appCl, log, port)

	bi := buildinfo.Get()
	log.Infof("Version %q built on %q against commit %q", bi.Version, bi.Date, bi.Commit)

	if serverPK == "" {
		err := errors.New("Empty server PubKey. Exiting")
		log.Error(err)
		setAppErr(appCl, log, err)
		return err
	}

	pk := cipher.PubKey{}
	if err := pk.UnmarshalText([]byte(serverPK)); err != nil {
		log.WithError(err).Error("Invalid server PubKey")
		setAppErr(appCl, log, err)
		return err
	}

	defer setAppStatus(appCl, log, appserver.AppDetailedStatusStopped)

	// httpProxy is bound once per proc lifetime — its connection
	// per request to the local SOCKS5 listener means it survives
	// reconnect cycles transparently (browser sees a brief
	// connection-refused on the gap, retries succeed).
	httpCtx, httpCancel := context.WithCancel(ctx)
	defer httpCancel()
	if httpAddr != "" {
		go httpProxy(httpCtx, httpAddr, addr, log)
	}

	// SIGINT goroutine cancels the outer ctx so the cycle loop
	// breaks out cleanly. Installed once for the proc lifetime;
	// each cycle's client.ListenAndServe returns when the
	// underlying conn closes (we close it in the cycle's deferred
	// cleanup when ctx fires).
	cycleCtx, cycleCancel := context.WithCancel(ctx)
	defer cycleCancel()
	if runtime.GOOS != "windows" {
		termCh := make(chan os.Signal, 1)
		signal.Notify(termCh, os.Interrupt)
		go func() {
			select {
			case <-termCh:
				cycleCancel()
			case <-cycleCtx.Done():
			}
		}()
	}

	// runCycle does one connect + serve attempt. Returns nil
	// only on a clean ctx-done stop; any other return is a
	// (re-)connect trigger when --reconnect is set.
	runCycle := func() error {
		// Bind :1080 up front and serve status.skysocks (and the branded
		// interstitial for real traffic) in-process WHILE we dial the exit, so the
		// reserved diagnostic page is reachable during the "still connecting" window
		// instead of connection-refused. The live Client can only be built from the
		// dialed session conn, so this sessionless listener owns :1080 until the dial
		// succeeds, then releases it to client.ListenAndServe below. Best-effort: if
		// the bind fails we just skip it and dial as before.
		dctx, dcancel := context.WithCancel(cycleCtx)
		ddone := make(chan struct{})
		if lis, lerr := net.Listen("tcp", addr); lerr == nil {
			go func() {
				defer close(ddone)
				skysocks.ServeDisconnected(dctx, lis, appCl)
			}()
		} else {
			log.WithError(lerr).Debug("disconnected status listener not bound")
			close(ddone)
		}

		conn, err := dialServer(cycleCtx, appCl, pk, serverPort, false)
		if err != nil {
			// Stop the disconnected listener and wait for it to release :1080.
			dcancel()
			<-ddone
			return fmt.Errorf("dial server: %w", err)
		}
		conns := []net.Conn{conn}
		// Dial the additional tunnels. Each is an independent route group +
		// noise + yamux; the client stripes browser conns across them. A tunnel
		// that fails to dial is skipped (degrade to fewer tunnels) rather than
		// failing the whole cycle. diversify=true asks the visor to leave over a
		// DIFFERENT first-hop transport than the tunnels already dialed to this
		// exit (disjoint-tunnel coordination), so their throughputs sum instead
		// of splitting one link — see docs/mux_aggregation_rfc.md step 3.
		//
		// The loop is SEQUENTIAL by design, and that is what makes the
		// diversification reliable: the visor's sibling-exclusion scan
		// (router.siblingRouteGroupExclusions, #4214) diverts tunnel i+1 off the
		// first-hop transports tunnel i already holds ONLY if tunnel i's route
		// group is already visible in the visor's live set (rgsNs). It is: the
		// visor registers the primary route group — with its first-hop transport
		// in rg.tps — into rgsNs SYNCHRONOUSLY inside saveRouteGroupRules, before
		// DialRoutes (and thus this RPC dial) returns. #4209 defers only the
		// AUXILIARY mux legs asynchronously, and skysocks tunnels request no mux
		// (muxRoutes=0), so each tunnel is single-leg with nothing left to
		// register late. Because each dialServer call below returns only after its
		// tunnel is registered, tunnel i is always seen by tunnel i+1's exclusion
		// scan — no inter-dial delay or route-group poll is needed. (The exclusion
		// is a soft preference: if fewer than N disjoint transports exist, tunnels
		// fall back to a shared path.)
		for i := int64(1); i < tunnels; i++ {
			extra, derr := dialServer(cycleCtx, appCl, pk, serverPort, true)
			if derr != nil {
				log.WithError(derr).Warnf("tunnel %d/%d dial failed; continuing with %d tunnel(s)", i+1, tunnels, len(conns))
				continue
			}
			conns = append(conns, extra)
		}
		// Stop the disconnected listener and wait for it to release :1080 before the
		// live Client rebinds the same addr.
		dcancel()
		<-ddone
		log.Infof("Connected to %v", pk)
		if len(conns) > 1 {
			log.Infof("skysocks-client: %d tunnels dialed with visor-side disjoint-path coordination — each extra tunnel is steered off the first-hop transports the earlier ones claimed (docs/mux_aggregation_rfc.md step 3). Tunnels that could not find a disjoint transport fall back to a shared path.", len(conns))
		}
		client, err := skysocks.NewMultiClient(conns, appCl)
		if err != nil {
			return fmt.Errorf("new client: %w", err)
		}
		// Keep N healthy tunnels: when one dies, the client re-dials a fresh
		// disjoint replacement (docs/mux_aggregation_rfc.md steps 3-4). The dial
		// lives here (it needs the server PK + retrier + appnet fallback), so we
		// hand the Client the SAME diversify=true dial the extra tunnels used;
		// the visor steers the replacement off the survivors' first-hop transports
		// (#4214). Target the requested --tunnels so a re-dial also refills the
		// width when some initial dials fell short. Only wire re-dial for N>1: at
		// N==1 the client never re-dials (its lone tunnel's death is total
		// collapse, handled by the --reconnect loop above), keeping the default
		// path byte-identical. The Client bounds re-dial to one in-flight attempt
		// and backs off on repeated failure, so this cannot fight the outer
		// runCycle: it only replaces individual dead tunnels while the client is
		// otherwise alive; if EVERY tunnel dies, ListenAndServe returns and the
		// runCycle re-dials the whole client.
		client.SetTunnelTarget(int(tunnels))
		if tunnels > 1 {
			client.SetTunnelRedial(func() (net.Conn, error) {
				return dialServer(cycleCtx, appCl, pk, serverPort, true)
			})
		}
		// Close the client when the outer ctx fires so
		// ListenAndServe returns. With --reconnect the loop
		// just iterates; without it the loop body returns and
		// the proc exits (existing behavior).
		closeOnCtx := make(chan struct{})
		go func() {
			select {
			case <-cycleCtx.Done():
				if cerr := client.Close(); cerr != nil {
					log.WithError(cerr).Warn("Error closing client on context done")
				}
			case <-closeOnCtx:
			}
		}()
		defer close(closeOnCtx)

		if runtime.GOOS == "windows" {
			ipcClient, err := ipc.StartClient(skyenv.SkysocksClientName, nil)
			if err != nil {
				return fmt.Errorf("start IPC client: %w", err)
			}
			go client.ListenIPC(ipcClient)
		}

		log.Infof("Serving proxy client %v", addr)
		setAppStatus(appCl, log, appserver.AppDetailedStatusRunning)
		//nolint:staticcheck
		if err := client.ListenAndServe(addr); err != nil {
			return fmt.Errorf("serve proxy client: %w", err)
		}
		return nil
	}

	if !reconnect {
		if err := runCycle(); err != nil {
			log.WithError(err).Error("skysocks-client failed")
			setAppErr(appCl, log, err)
			return err
		}
		setAppStatus(appCl, log, appserver.AppDetailedStatusStopped)
		return nil
	}

	// Reconnect mode: loop until cycleCtx is canceled (signal /
	// parent visor stop). Each iteration is one connect+serve
	// cycle; a failure logs + sleeps + retries indefinitely.
	// The visor-side restart_policy still wins as a backstop —
	// if this proc itself panics, the restart loop catches it.
	baseDelay := time.Duration(reconnectDelay) * time.Second
	const maxReconnectDelay = 30 * time.Second
	delay := baseDelay
	for {
		start := time.Now()
		err := runCycle()
		if cycleCtx.Err() != nil {
			// Ctx-induced unwind (signal / visor stop), not a failure.
			return nil
		}
		if err != nil {
			log.WithError(err).Warnf("Reconnecting in %v", delay)
			setAppStatus(appCl, log, appserver.AppDetailedStatusReconnecting)
		}
		select {
		case <-cycleCtx.Done():
			return nil
		case <-time.After(delay):
		}
		// Back off while the remote stays unreachable; reset once a
		// cycle has actually served for a while (remote came back).
		if time.Since(start) > maxReconnectDelay {
			delay = baseDelay
		} else if delay < maxReconnectDelay {
			delay *= 2
			if delay > maxReconnectDelay {
				delay = maxReconnectDelay
			}
		}
	}
}

// dialServer dials one tunnel to the exit. diversify, set for tunnels 2..N of
// a --tunnels aggregation, asks the visor to steer this tunnel off the
// first-hop transports/intermediates the earlier tunnels to this same exit
// already occupy — so the tunnels leave over DIFFERENT first-hop transports
// and their throughputs sum instead of splitting one link
// (docs/mux_aggregation_rfc.md step 3). The visor does all the transport-ID
// bookkeeping; the app just signals intent. It is a no-op for the first tunnel
// (no sibling route group to diverge from → dial is identical to today).
func dialServer(ctx context.Context, appCl *app.Client, pk cipher.PubKey, port routing.Port, diversify bool) (net.Conn, error) {
	//nolint:errcheck
	appCl.SetDetailedStatus(appserver.AppDetailedStatusStarting) //nolint:errcheck,gosec
	// dial one network to the server. On skynet, --direct forces a 1-hop
	// direct-transport-only dial (create-on-demand, bypass the route-finder +
	// setup node, self-heals on server restart); dmsg is a plain relay stream.
	dial := func(_ context.Context, a appnet.Addr) (net.Conn, error) {
		if a.Net == netType && (direct || diversify) {
			return appCl.DialWithOptions(a, 0, 0, 0, 0, 0, 0, direct, diversify)
		}
		return appCl.Dial(a)
	}
	nets := []appnet.Type{netType}
	if dmsgFallback {
		nets = append(nets, appnet.TypeDmsg)
	}
	var conn net.Conn
	err := r.Do(ctx, func() error {
		var err error
		conn, _, err = appnet.DialWithFallback(ctx, dial, pk, port, nets...)
		return err
	})
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func setAppErr(appCl *app.Client, log logrus.FieldLogger, err error) {
	if appErr := appCl.SetError(err.Error()); appErr != nil {
		log.WithError(appErr).WithField("original_error", err).Warn("Failed to set error")
	}
}

func setAppStatus(appCl *app.Client, log logrus.FieldLogger, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		log.WithError(err).WithField("status", status).Warn("Failed to set status")
	}
}

func httpProxy(ctx context.Context, httpAddr, sockscAddr string, log logrus.FieldLogger) {
	proxy := goproxy.NewProxyHttpServer()

	proxyURL, err := url.Parse(fmt.Sprintf("socks5://127.0.0.1%s", sockscAddr))
	if err != nil {
		log.WithError(err).Error("Failed to parse socks address")
		return
	}

	proxy.Tr.Proxy = http.ProxyURL(proxyURL)

	log.Infof("Serving http proxy %v", httpAddr)
	httpProxySrv := &http.Server{
		Addr:              httpAddr,
		Handler:           proxy,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		//nolint:errcheck
		httpProxySrv.Close() //nolint:errcheck,gosec
		log.Info("Stopping http proxy")
	}()

	if err := httpProxySrv.ListenAndServe(); err != nil {
		log.WithError(err).Error("Error serving http proxy")
	}
}

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

func setAppPort(appCl *app.Client, log logrus.FieldLogger, port routing.Port) {
	if err := appCl.SetAppPort(port); err != nil {
		log.WithError(err).WithField("port", port).Warn("Failed to set port")
	}
}
