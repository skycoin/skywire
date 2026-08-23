// Package commands cmd/apps/skynet-client/commands/skynet-client.go c4-app-skynet
package commands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/skycoin/skywire/pkg/cmdutil"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynet"
	"github.com/skycoin/skywire/pkg/skyroute"
)

const (
	netType = appnet.TypeSkynet
)

var (
	// srv is the remote server public key
	srv string
	// remotePort is the port to connect to on the remote server
	remotePort int
	// localPort is the local port to listen on
	localPort int
	// appPort is the routing port for the app
	appPort uint16
	// routes is the number of parallel skynet mux routes to use for the
	// underlying app-level conn. 0 or 1 = single route (legacy Dial
	// path); N > 1 = router establishes N parallel mux routes and the
	// app sees a single conn whose payload is striped across them.
	routes int
	// minHops, when >= 2, forces the router to find non-direct paths
	// (rejecting any direct transport between this visor and the
	// remote). Needed in combination with routes > 1 to actually
	// exercise the mux>direct hypothesis end-to-end: without
	// min-hops, the router happily picks the direct transport for
	// every mux route, so N mux streams ride one TCP socket and
	// nothing is multiplexed across intermediates.
	minHops int
	// fwdMinHops / revMinHops are per-direction MinHops overrides for
	// bandwidth-asymmetric workloads (HTTP GET = tiny upstream + bulk
	// downstream). When > 0 they win over min-hops for THAT direction.
	// Canonical asymmetric test: --forward-min-hops 0 --reverse-min-hops 2
	// lets forward stay direct while reverse is forced multi-hop.
	fwdMinHops int
	revMinHops int
	// fwdMux / revMux are per-direction MuxRoutes overrides. When > 0
	// they win over --routes for THAT direction's route count.
	// Canonical download-heavy shape: --forward-mux 1 --reverse-mux 4
	// yields 1 forward leg (cheap upstream) + 4 reverse legs that
	// aggregate the bulk payload.
	fwdMux int
	revMux int
	// direct, when set, forces a direct-transport-only route to the server:
	// the router creates a direct transport if none exists and dials 1-hop
	// over it, bypassing the route-finder. For control-plane forwards (e.g.
	// the rsn-pprof resolving-proxy bridge) that need a reliable single hop
	// and must self-heal when the peer restarts and its transport drops —
	// avoiding the silent flaky-multihop fallback. Mutually exclusive with
	// --min-hops >= 2 / --routes > 1 (which exist to force multi-hop).
	direct bool
	// reconnect, when set, makes per-accept dial failures retry
	// indefinitely (with --reconnect-delay between attempts) instead
	// of dropping the local conn on first failure. Closes the gap
	// during remote-visor restarts: a browser refresh that lands
	// during the remote's downtime keeps the local TCP open until
	// the remote comes back, instead of returning ECONNREFUSED.
	// Per-accept rather than outer-loop because skynet-client's
	// local listener already survives indefinitely — the failure
	// site is inside handleLocalConn's dialRemote, not at startup.
	reconnect      bool
	reconnectDelay int64
)

func init() {
	launcher.RegisterApp("skynet-client", RunSkynetClient)
	RootCmd.Flags().StringVar(&srv, "srv", "", "remote server public key")
	RootCmd.Flags().IntVar(&remotePort, "remote", 0, "remote port to forward")
	RootCmd.Flags().IntVar(&localPort, "local", 0, "local port to listen on")
	RootCmd.Flags().Uint16Var(&appPort, "port", 0, "routing port for communication between app and visor")
	RootCmd.Flags().IntVar(&routes, "routes", 0, "number of parallel skynet mux routes (0 or 1 = single route)")
	RootCmd.Flags().IntVar(&minHops, "min-hops", 0, "force routes through at least this many intermediates (>=2 rejects direct paths)")
	RootCmd.Flags().IntVar(&fwdMinHops, "forward-min-hops", 0, "per-direction forward MinHops override (>=2 forces multi-hop on forward direction only)")
	RootCmd.Flags().IntVar(&revMinHops, "reverse-min-hops", 0, "per-direction reverse MinHops override (>=2 forces multi-hop on reverse direction only)")
	RootCmd.Flags().IntVar(&fwdMux, "forward-mux", 0, "per-direction forward MuxRoutes override (>0 sets forward leg count independent of --routes)")
	RootCmd.Flags().IntVar(&revMux, "reverse-mux", 0, "per-direction reverse MuxRoutes override (>0 sets reverse leg count; canonical download-heavy shape: --forward-mux 1 --reverse-mux N)")
	RootCmd.Flags().BoolVar(&direct, "direct", false, "force a direct-transport-only route (create the transport on demand, dial 1-hop, bypass the route-finder); self-heals when the peer restarts")
	RootCmd.Flags().BoolVar(&reconnect, "reconnect", false, "retry per-accept dial indefinitely instead of dropping local conn on remote-visor failure")
	RootCmd.Flags().Int64Var(&reconnectDelay, "reconnect-delay", 2, "seconds between per-accept dial retries when --reconnect is set")
}

// RootCmd is the root command for skynet-client
var RootCmd = &cobra.Command{
	Use:                   "skynet-client",
	Short:                 "skywire port forwarding client application",
	Long:                  "Skynet client connects to a remote skynet server and forwards traffic to localhost",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		ctx := context.Background()
		if err := RunSkynetClient(ctx, args); err != nil {
			log.Fatal(err)
		}
	},
}

// RunSkynetClient runs the skynet client app logic.
func RunSkynetClient(ctx context.Context, args []string) error {
	// Parse flags when called via internal launcher
	if len(args) > 0 {
		fs := pflag.NewFlagSet("skynet-client", pflag.ContinueOnError)
		fs.StringVar(&srv, "srv", "", "remote server public key")
		fs.IntVar(&remotePort, "remote", 0, "remote port")
		fs.IntVar(&localPort, "local", 0, "local port")
		fs.Uint16Var(&appPort, "port", 0, "routing port")
		fs.IntVar(&routes, "routes", 0, "number of parallel skynet mux routes")
		fs.IntVar(&minHops, "min-hops", 0, "minimum hop count (>=2 rejects direct paths)")
		fs.IntVar(&fwdMinHops, "forward-min-hops", 0, "per-direction forward MinHops override")
		fs.IntVar(&revMinHops, "reverse-min-hops", 0, "per-direction reverse MinHops override")
		fs.IntVar(&fwdMux, "forward-mux", 0, "per-direction forward MuxRoutes override")
		fs.IntVar(&revMux, "reverse-mux", 0, "per-direction reverse MuxRoutes override")
		fs.BoolVar(&direct, "direct", false, "force a direct-transport-only route (create on demand, dial 1-hop, bypass route-finder)")
		fs.BoolVar(&reconnect, "reconnect", false, "retry per-accept dial indefinitely on failure")
		fs.Int64Var(&reconnectDelay, "reconnect-delay", 2, "seconds between per-accept dial retries")
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}

	appCl := app.NewClient(nil)
	defer appCl.Close()

	appCl.Log().Infof("Build info: %s", buildinfo.Version())

	// Validate required flags
	if srv == "" {
		err := fmt.Errorf("--srv flag (remote server public key) is required")
		appCl.SetErrorOrLog(err)
		return err
	}

	var remotePK cipher.PubKey
	if err := remotePK.Set(srv); err != nil {
		appCl.SetErrorOrLog(fmt.Errorf("invalid server public key: %w", err))
		return fmt.Errorf("invalid server public key: %w", err)
	}

	if remotePort <= 0 || remotePort > 65535 {
		err := fmt.Errorf("--remote flag (remote port) must be 1-65535")
		appCl.SetErrorOrLog(err)
		return err
	}

	if localPort <= 0 || localPort > 65535 {
		err := fmt.Errorf("--local flag (local port) must be 1-65535")
		appCl.SetErrorOrLog(err)
		return err
	}

	appCl.Log().Infof("Setting up accept loop on localhost:%d → %s:%d",
		localPort, remotePK.Hex(), remotePort)

	// Set routing port if specified
	if appPort != 0 {
		appCl.SetAppPortOrLog(routing.Port(appPort))
	}

	appCl.SetStatusOrLog(appserver.AppDetailedStatusStarting)

	// A skynet-client is a port-forward — a control/tunnel channel (pprof,
	// debug, dmsgweb, :4443…), not bulk data. So when the operator has set NO
	// routing flags at all, default to a direct 1-hop route instead of
	// inheriting the visor-global routing policy: the adaptive default would
	// otherwise spread this forward across a churning multi-hop warm-standby mux
	// (observed: a 3ms same-LAN forward muxed across a 11.5s leg, which broke
	// :4443). Any explicit flag (--direct, --routes, --min-hops, per-direction
	// mux/min-hops) opts back into the global/explicit shape and wins.
	if !direct && routes == 0 && minHops == 0 && fwdMinHops == 0 && revMinHops == 0 && fwdMux == 0 && revMux == 0 {
		direct = true
		appCl.Log().Info("skynet-client forward: no routing flags set — defaulting to --direct (1-hop, no mux). Pass --routes/--min-hops to opt into the mux.")
	}

	// dialWithShape performs the actual app-level dial to a given
	// forwarding port, applying the mux/min-hops shape. Any EXPLICIT
	// per-call value (>=1), including 1, is a per-app override of the
	// visor-global min_hops/mux_routes — so `--routes 1` / `--min-hops 1`
	// force a single / direct route out from under a global
	// mux_routes/min_hops>1 (which otherwise forces this forward into
	// multi-hop mux and breaks it). Unset (0) inherits the global default
	// via the plain Dial path.
	dialWithShape := func(port uint16) (net.Conn, error) {
		addr := appnet.Addr{Net: netType, PubKey: remotePK, Port: routing.Port(port)}
		if direct || routes >= 1 || minHops >= 1 || fwdMinHops >= 1 || revMinHops >= 1 || fwdMux >= 1 || revMux >= 1 {
			return appCl.DialWithOptions(addr, routes, minHops, fwdMinHops, revMinHops, fwdMux, revMux, direct)
		}
		return appCl.Dial(addr)
	}

	// Build a single log line describing the dial shape (mux + min-hops
	// + per-direction). Suppress unset fields so the common case stays
	// short.
	dialShape := fmt.Sprintf("%s via mux port %d (fallback %d)", remotePK.Hex(),
		skyenv.SkyForwardingMuxPort, skyenv.SkyForwardingServerPort)
	if routes > 1 {
		dialShape += fmt.Sprintf(" with %d parallel mux routes", routes)
	}
	if minHops > 1 {
		dialShape += fmt.Sprintf(" min-hops=%d", minHops)
	}
	if fwdMinHops > 1 {
		dialShape += fmt.Sprintf(" forward-min-hops=%d", fwdMinHops)
	}
	if revMinHops > 1 {
		dialShape += fmt.Sprintf(" reverse-min-hops=%d", revMinHops)
	}
	if fwdMux > 0 {
		dialShape += fmt.Sprintf(" forward-mux=%d", fwdMux)
	}
	if revMux > 0 {
		dialShape += fmt.Sprintf(" reverse-mux=%d", revMux)
	}
	if direct {
		dialShape += " direct(force-direct-tp, ensure-on-demand)"
	}
	appCl.Log().Infof("Per-accept dial shape: %s", dialShape)

	// rawDial57 is the 1:1 legacy path: a fresh route group to
	// SkyForwardingServerPort per accept. Kept as the fallback for
	// older far-ends that don't serve the yamux mux port. The mux
	// shape (routes/min-hops) never SUSTAINS on this path — each accept
	// tore down its route group immediately — so it's only a
	// compatibility fallback, not the mux carrier.
	rawDial57 := func() (net.Conn, error) {
		return dialWithShape(skyenv.SkyForwardingServerPort)
	}

	// skyroute.Pool holds ONE route group open to SkyForwardingMuxPort
	// (59) and yamux-muxes every local accept over it. This is the fix
	// that makes the forward ACTUALLY carry the mux: the N parallel mux
	// routes are established ONCE at the route-group level (the port-59
	// dial), the group's keepalive keeps those hops warm, and each local
	// TCP accept becomes a yamux STREAM over that single warm group.
	// Per-stream yamux framing is what lets many local conns share one
	// route group without interleaving their bytes — the exact reason
	// the old code dialed a fresh 1:1 conn per accept (and so never
	// sustained a mux). The far-end serves this via acceptSkyForwardingMux
	// → serveSkyForwardingMuxSession, running the same ready-byte +
	// port-request handshake on each accepted stream, so Client.Connect
	// works unchanged per stream.
	pool := skyroute.New(skyroute.DefaultIdleTTL, appCl.Log())
	defer func() { _ = pool.Close() }() //nolint:errcheck

	// The DialRoute signature carries a ctx for route-setup cancellation,
	// but appCl's dial helpers take no ctx, so it's intentionally ignored.
	dialToMux := func(_ context.Context, muxPort uint16) (net.Conn, error) {
		return dialWithShape(muxPort)
	}

	// poolKey pins one warm route group per (peer, remote port, shape).
	// Distinct shapes hold independent groups instead of clobbering.
	poolKey := fmt.Sprintf("%s:%d:r%d:h%d:fh%d:rh%d:fm%d:rm%d:d%t",
		remotePK.Hex(), remotePort, routes, minHops, fwdMinHops, revMinHops, fwdMux, revMux, direct)

	rawDial := func() (net.Conn, error) {
		stream, err := pool.OpenStream(ctx, poolKey, dialToMux)
		if err == nil {
			return stream, nil
		}
		if errors.Is(err, skyroute.ErrNoMux) {
			// Older far-end without the yamux mux port — fall back to a
			// 1:1 dial on SkyForwardingServerPort.
			appCl.Log().Debug("Remote lacks mux forwarding port; falling back to 1:1 port-57 dial")
			return rawDial57()
		}
		return nil, err
	}

	// --reconnect wraps rawDial in a retry loop bounded only by ctx.
	// First attempt is immediate (no warmup pause); subsequent
	// attempts wait --reconnect-delay seconds. pkg/skynet.Client
	// invokes the factory per local TCP accept, so each accept that
	// happens during a remote-visor outage keeps trying until the
	// remote returns or the parent ctx is canceled (e.g. the local
	// TCP closes because the browser tab gave up).
	dialRemote := rawDial
	if reconnect {
		delay := time.Duration(reconnectDelay) * time.Second
		dialRemote = func() (net.Conn, error) {
			attempt := 0
			for {
				conn, err := rawDial()
				if err == nil {
					if attempt > 0 {
						appCl.Log().Infof("Dial succeeded after %d retries", attempt)
					}
					return conn, nil
				}
				attempt++
				appCl.Log().WithError(err).Warnf("Dial attempt %d failed; retrying in %v", attempt, delay)
				select {
				case <-ctx.Done():
					return nil, fmt.Errorf("dial canceled: %w", ctx.Err())
				case <-time.After(delay):
				}
			}
		}
	}

	// Create client with the dial factory; Serve binds the local
	// listener and runs the accept loop.
	client := skynet.NewClient(appCl.Log(), remotePK, remotePort, localPort, dialRemote)

	appCl.Log().Info("Skynet client ready — waiting for local connections")
	appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)

	// Handle shutdown
	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, os.Interrupt)

	go func() {
		<-termCh
		appCl.Log().Info("Received interrupt, shutting down...")
		if err := client.Close(); err != nil {
			appCl.Log().Errorf("Error closing client: %v", err)
		}
	}()

	defer appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)

	// Serve (accept local connections)
	serveCh := make(chan error, 1)
	go func() {
		serveCh <- client.Serve()
	}()

	select {
	case err := <-serveCh:
		if err != nil {
			appCl.Log().Errorf("Serve error: %v", err)
			return err
		}
	case <-ctx.Done():
		if err := client.Close(); err != nil {
			return err
		}
	}

	return nil
}

// Execute executes root CLI command.
func Execute() {
	cmdutil.RunRoot(RootCmd)
}
