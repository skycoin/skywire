// Package visor pkg/visor/init.go c3-vis-core
//
// This file is the "table of contents" for visor initialization. It declares all
// init modules, registers them with their dependencies, and provides the shared
// context helpers used by the init functions spread across the init_*.go files.
//
// Actual initialization logic lives in:
//   - init_dmsg.go      — DMSG client, ctrl, pty, ping, server latency
//   - init_transport.go — Transport manager, STCPR/SUDPH/STCP, address resolver, TPD
//   - init_router.go    — Router, route setup hooks, embedded route setup, node health
//   - init_apps.go      — App launcher, CLI/RPC, hypervisors
//   - init_services.go  — Event broadcaster, uptime, survey, forwarding, ping, UI server
package visor

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
	"github.com/skycoin/skywire/pkg/logging"
	vinit "github.com/skycoin/skywire/pkg/visor/visorinit"
)

type visorCtxKey int

const visorKey visorCtxKey = iota

type runtimeErrsCtxKey int

const runtimeErrsKey runtimeErrsCtxKey = iota

const ownerRWX = 0700

// Visor initialization is split into modules, that can be initialized independently.
// modules is one visor's module graph: each module needs an init function, its
// dependencies and its name. To add a piece of functionality to the visor, add a
// field here and register it in registerModules.
type modules struct {
	// Event broadcasting system
	ebc vinit.Module
	// Address resolver
	ar vinit.Module
	// App discovery
	disc vinit.Module
	// Stun module
	sc vinit.Module
	// SUDPH module
	sudphC vinit.Module
	// STCPR module
	stcprC vinit.Module
	// STCP module
	stcpC vinit.Module
	// QUIC module (#2607 QUIC follow-on)
	quicC vinit.Module
	// WS module: serves the WebSocket transport over the stcpr+WS cmux so
	// browser (wasm) visors can reach this visor over WebSocket
	wsC vinit.Module
	// WT module: serves the WebTransport (QUIC/HTTP3) transport + registers its
	// cert hash with the AR so browser (wasm) visors can dial it
	wtC vinit.Module
	// dmsg pty: a remote terminal to the visor working over dmsg protocol
	ptyModule vinit.Module
	// Dmsg module
	dmsgC vinit.Module
	// Transportability checker ensures the visor can accept transports by creating a self-transport or exiting after 3 failed attempts to create one
	tc vinit.Module
	// TPD concurrency checker removes transports from tpd that the visor does not have registered locally
	tpdco vinit.Module
	// Transport manager
	tr vinit.Module
	// Transport setup
	trs vinit.Module
	// Routing system
	rt vinit.Module
	// Application launcher
	launch vinit.Module
	// CLI
	cli vinit.Module
	// hypervisors to control this visor
	hvs vinit.Module
	// Uptime tracker
	ut vinit.Module
	// Public visors: automatically establish connections to public visors
	pvs vinit.Module
	// Public visor: advertise current visor as public
	pv vinit.Module
	// Transport module (this is not a functional module but a grouping of all heavy transport types initializations)
	tm vinit.Module
	// hypervisor module
	hv vinit.Module
	// Dmsg ctrl module
	dmsgCtrl vinit.Module
	// Dmsg http log server module
	dmsgHTTPLogServer vinit.Module
	// System survey module
	systemSurvey vinit.Module
	// Dmsg http module
	dmsgHTTP vinit.Module
	// Dmsg trackers module
	dmsgTrackers vinit.Module
	// Skywire Forwarding conn module
	skyFwd vinit.Module
	// Ping module (skywire routes)
	pi vinit.Module
	// Dmsg ping module (dmsg direct connection)
	dmsgPi vinit.Module
	// In-process dmsg server sharing the visor's PK/SK (config-gated, default off)
	dmsgSrv vinit.Module
	// Dmsg server latency tracking (self-ping via each server)
	dmsgServerLatency vinit.Module
	// Embedded Transport Setup Node (separate dmsg client with TPS identity)
	embTPS vinit.Module
	// Embedded Route Setup Node (separate dmsg client with route setup identity)
	embRouteSetup vinit.Module
	// Embedded dmsgweb resolver (localhost SOCKS5 for .dmsg browsing)
	embDmsgWeb vinit.Module
	embWisp    vinit.Module
	// Clearnet HTTP forward-proxy over dmsg (opt-in, whitelist-gated)
	embFwdProxy vinit.Module
	// Embedded skynetweb resolver (localhost SOCKS5 for .skynet browsing)
	embSkynetWeb vinit.Module
	// Additional resolving proxies from the `resolvers` config list
	embResolvers vinit.Module
	// Native real-origin browse proxy (loopback reverse-proxy origin over dmsg)
	meshProxy vinit.Module
	// Embedded SMTP→skywire bridge (localhost SMTP listener for *.skynet recipients)
	embSkymailBridge vinit.Module
	skymail          vinit.Module
	// UI server module (serves tp-viz)
	uiServer vinit.Module
	// Node health tracking for TPS and RSN
	nodeHealth vinit.Module
	// Auto-register localhost ports for skynet forwarding
	skynetPorts vinit.Module
	// Self-probe: periodic dmsg listener reachability check
	selfProbe vinit.Module
	// Pre-open DmsgAwaitSetupPort listener so it's ready before initRouter
	routerListener vinit.Module
	// Visor-local telemetry store (bbolt + sampler)
	statsMod        vinit.Module
	cxoUserFeedsMod vinit.Module
	// Chat-pair feed manager (opt-in per-partner CXO feeds).
	pairingMod vinit.Module
	// Chat-group feed manager (D1 owner-centric CXO feeds with
	// multi-PK allowlist).
	groupingMod vinit.Module
	// Skychat 1:1 voice-call manager (dmsg+skynet signaling, RTP media).
	voiceMod vinit.Module
	// coinNodesMod forwards + advertises configured fibercoin nodes
	coinNodesMod vinit.Module
	// regCXOMod publishes this visor's discovery entry as a CXO feed
	// (registration-over-CXO) when opted in
	regCXOMod vinit.Module
	// arBindCXOMod mirrors this visor's AR bindings onto a CXO feed the
	// address-resolver aggregates (AR-bind-over-CXO), always-on/additive
	arBindCXOMod vinit.Module
	// sdRegCXOMod mirrors this visor's live service-discovery entry set onto
	// a CXO feed the service-discovery aggregates (SD-registration-over-CXO),
	// always-on/additive
	sdRegCXOMod vinit.Module
	// visor that groups all modules together
	vis vinit.Module
}

// registerModules builds a module graph: modules with their names and
// dependencies, and init functions wrapped to reach the visor and its runtime
// errors channel. Dependencies point at fields of the same graph, so a module
// can name one that is assigned further down.
func registerModules(logger *logging.MasterLogger) *modules {
	m := &modules{}
	// utility module maker, to avoid passing logger and wrapping each init function
	// in withVisorCtx
	maker := func(name string, f initFn, deps ...*vinit.Module) vinit.Module {
		return vinit.MakeModule(name, withInitCtx(f), logger, deps...)
	}
	m.dmsgHTTP = maker("dmsg_http", initDmsgHTTP)
	m.ebc = maker("event_broadcaster", initEventBroadcaster)
	m.ar = maker("address_resolver", initAddressResolver, &m.dmsgC, &m.sc, &m.dmsgHTTP)
	m.disc = maker("discovery", initDiscovery, &m.dmsgC, &m.sc, &m.dmsgHTTP)
	m.tr = maker("transport", initTransport, &m.ar, &m.ebc, &m.dmsgHTTP)

	m.sc = maker("stun_client", initStunClient)
	m.sudphC = maker("sudph", initSudphClient, &m.sc, &m.tr)
	m.stcprC = maker("stcpr", initStcprClient, &m.tr)
	m.stcpC = maker("stcp", initStcpClient, &m.tr)
	m.quicC = maker("quic", initQuicClient, &m.tr)
	m.wsC = maker("swsr", initWSClient, &m.tr)
	m.wtC = maker("swtr", initWTClient, &m.tr)
	m.dmsgC = maker("dmsg", initDmsg, &m.ebc, &m.dmsgHTTP)
	m.dmsgCtrl = maker("dmsg_ctrl", initDmsgCtrl, &m.dmsgC, &m.tr)
	// dmsghttp_logserver mounts /pty on top of dmsgpty's CLI socket
	// (or self-dialed dmsg when no CLI socket is configured), so it
	// must run after pty has finished its setup.
	m.dmsgHTTPLogServer = maker("dmsghttp_logserver", initDmsgHTTPLogServer, &m.dmsgC, &m.tr, &m.ptyModule)
	m.systemSurvey = maker("system_survey", initSystemSurvey, &m.dmsgHTTPLogServer)
	m.dmsgTrackers = maker("dmsg_trackers", initDmsgTrackers, &m.dmsgC)

	m.ptyModule = maker("dmsg_pty", initDmsgpty, &m.dmsgC)
	m.embRouteSetup = maker("embedded_route_setup", initEmbeddedRouteSetup, &m.dmsgC)
	m.embDmsgWeb = maker("embedded_dmsgweb", initEmbeddedDmsgWeb, &m.dmsgC)
	m.embFwdProxy = maker("dmsg_forward_proxy", initDmsgForwardProxy, &m.dmsgC)
	m.embSkymailBridge = maker("embedded_skymail_bridge", initEmbeddedSkymailBridge, &m.dmsgC)
	// The mailbox binds dmsg port 25 at once; its skynet mirror waits for
	// the router on its own, and forwarded ports decide whether 25 is free.
	m.skymail = maker("skymail", initSkymail, &m.dmsgC)
	// The embedded Wisp server needs nothing from the mesh at init: its
	// egress is dialed per stream, so it binds its vnet port immediately and
	// a page can connect before any route exists. launch is the dependency
	// only so the skysocks-client it dials has had its chance to bind first.
	m.embWisp = maker("embedded_wisp", initEmbeddedWisp, &m.launch)
	// routerListener pre-opens DmsgAwaitSetupPort the moment dmsgC is
	// ready so peers dialing it during the rt-init window (held up by
	// &m.tr) don't hit "request has no associated listener". rt picks up
	// the listener via router.Config.AwaitSetupListener.
	m.routerListener = maker("router_listener", initRouterListener, &m.dmsgC)
	m.rt = maker("router", initRouter, &m.tr, &m.dmsgC, &m.dmsgHTTP, &m.embRouteSetup, &m.routerListener)
	// skynetweb depends on the router being up, unlike dmsgweb.
	m.embSkynetWeb = maker("embedded_skynetweb", initEmbeddedSkynetWeb, &m.rt)
	// The additional `resolvers` list can hold entries of BOTH kinds, so it
	// takes the stricter of the two dependencies: rt (which transitively
	// brings up dmsgC) rather than dmsgC alone. A dmsg-only list would be
	// happy earlier, but gating on the kinds actually present would make the
	// module graph depend on config contents — and get it wrong the first
	// time someone adds a skynet entry.
	m.embResolvers = maker("embedded_resolvers", initEmbeddedResolvers, &m.rt)
	// Native real-origin browse proxy: reverse-proxies mesh sites over dmsg AND
	// skynet from an isolated loopback origin. Depends on the router module (rt) —
	// which transitively brings up dmsgC (assigns v.dmsgHTTP) AND sets v.router
	// (needed for the skynet transport). Without the rt dep the skynet round-
	// tripper would be built before v.router exists.
	m.meshProxy = maker("mesh_proxy", initMeshProxy, &m.rt)
	// launch also waits on the embedded resolving proxies: initLauncher builds
	// the app list from v.embeddedDmsgWeb / v.embeddedSkynetWeb, and without
	// these deps whether the "dmsgweb"/"skynetweb" app rows exist (and thus
	// whether Enable=true autostarts them) is a RACE against their inits.
	// Observed live: dmsgweb (dep dmsgC, early) reliably won its race while
	// skynetweb (dep rt — the same module launch waits on, so they start
	// concurrently) reliably lost: skynet_web.enable=true bound nothing and
	// `app ls` had no skynetweb row.
	m.launch = maker("launcher", initLauncher, &m.ebc, &m.disc, &m.dmsgC, &m.tr, &m.rt, &m.embDmsgWeb, &m.embSkynetWeb, &m.embResolvers)
	// cli depends on tr so v.tpM is set when initCLI wires up the
	// shared VStreamMux for transport-RPC (registered as the manager's
	// VisorRPCPacket handler). Without this dep, initCLI could run
	// before initTransport, the mux would be skipped, and every
	// TransportRPCCall would error with "transport RPC not initialized".
	m.cli = maker("cli", initCLI, &m.tr)
	// hvs depends on tr for the same reason: ServeRPCClient takes
	// v.tpM to gate its skynet-preferred dial path. Without this dep
	// the dial would always fall through to dmsg even when a fast
	// transport exists.
	m.hvs = maker("hypervisors", initHypervisors, &m.dmsgC, &m.tr)
	m.ut = maker("uptime_tracker", initUptimeTracker, &m.dmsgHTTP)
	m.pv = maker("public_autoconnect", initPublicAutoconnect, &m.tr, &m.disc)
	m.trs = maker("transport_setup", initTransportSetup, &m.dmsgC, &m.tr)
	m.tm = vinit.MakeModule("transports", vinit.DoNothing, logger, &m.sc, &m.sudphC, &m.dmsgCtrl, &m.dmsgHTTPLogServer, &m.dmsgTrackers, &m.launch)
	m.pvs = maker("public_visor", initPublicVisor, &m.tr, &m.ar, &m.disc, &m.stcprC)
	m.skyFwd = maker("sky_forward_conn", initSkywireForwardConn, &m.dmsgC, &m.dmsgCtrl, &m.tr, &m.launch)
	m.pi = maker("ping", initPing, &m.dmsgC, &m.tm)
	m.dmsgPi = maker("dmsg_ping", initDmsgPing, &m.dmsgC)
	// &m.tr: the in-process server serves on the transport stage's shared TCP
	// cmux branch by default, so that stage must have bound it first.
	m.dmsgSrv = maker("dmsg_server", initDmsgServer, &m.dmsgC, &m.tr)
	m.dmsgServerLatency = maker("dmsg_server_latency", initDmsgServerLatency, &m.dmsgPi)
	m.tc = maker("transportable", initEnsureVisorIsTransportable, &m.dmsgC, &m.tm, &m.stcprC)
	m.tpdco = maker("tpd_concurrency", initEnsureTPDConcurrency, &m.dmsgC, &m.tm)
	m.embTPS = maker("embedded_tps", initEmbeddedTPS, &m.dmsgC)
	m.uiServer = maker("ui_server", initUIServer, &m.dmsgC, &m.tr, &m.embTPS)
	m.nodeHealth = maker("node_health", initNodeHealth, &m.dmsgC)
	m.selfProbe = maker("self_probe", initSelfProbe, &m.dmsgC, &m.dmsgHTTPLogServer, &m.rt)
	// Register localhost ports for skynet forwarding AFTER all
	// services are up (cli, ui, logserver) so the ports are
	// actually listening when we probe them.
	m.skynetPorts = maker("skynet_ports", initSkynetForwardPorts, &m.cli, &m.dmsgHTTPLogServer, &m.uiServer, &m.skyFwd)
	// Stats depends on tr (transport probe), dmsgC (dmsg-online probe),
	// and launch (proc manager — service probe). The probes are
	// pull-style and tolerate nil at probe time, so missing-but-still-
	// initializing deps just yield "offline" for that subsystem.
	m.statsMod = maker("stats", initStats, &m.tr, &m.dmsgC, &m.launch)
	// User-feed registry depends on stats (it shares the logserver
	// hookup) and dmsgC (each user feed gets its own dmsg listener).
	m.cxoUserFeedsMod = maker("cxo_user_feeds", initCXOUserFeeds, &m.statsMod, &m.dmsgC)
	// Chat-pair manager: opt-in per-partner CXO feeds with allowlist.
	// Depends only on dmsgC.
	m.pairingMod = maker("pairing", initPairing, &m.dmsgC)
	// Chat-group manager: D1 owner-centric group CXO feeds. Same
	// shape as pairingMod, same dmsgC dependency, separate bbolt
	// store. See init_group.go.
	m.groupingMod = maker("grouping", initGrouping, &m.dmsgC)
	// Skychat 1:1 voice: signaling + RTP media over dmsg/skynet. Depends on
	// dmsgC. See init_voice.go.
	m.voiceMod = maker("voice", initVoice, &m.dmsgC)
	// Fibercoin node discovery: forward configured coin-node HTTP APIs over
	// dmsg + health-gated type=coin SD registration. Depends on dmsgC (forward
	// + SD dmsg client) and skyFwd (dmsg forwarder). See init_coinnode.go.
	m.coinNodesMod = maker("coin_nodes", initCoinNodes, &m.dmsgC, &m.skyFwd)
	// Registration-over-CXO publisher: runs whenever a dmsg-discovery PK
	// resolves (no opt-in flag exists, despite older comments). Mirrors this visor's signed discovery entry onto a CXO feed that
	// dmsg-discovery aggregates, off the timer-driven HTTP re-PUT. Depends on
	// dmsgC (the entry it publishes + the feed's transport). See
	// init_registration_cxo.go.
	m.regCXOMod = maker("registration_cxo", initRegistrationCXO, &m.dmsgC)
	// AR-bind-over-CXO publisher: mirror this visor's address-resolver
	// bindings onto a CXO feed the AR aggregates, off the timer-driven
	// re-registration (each a fresh dmsg Noise handshake). Depends on the AR
	// client (the bind hook it publishes from) and dmsgC (the feed transport).
	// See init_ar_bind_cxo.go.
	m.arBindCXOMod = maker("ar_bind_cxo", initARBindCXO, &m.ar, &m.dmsgC)
	// SD-registration-over-CXO publisher: mirror this visor's live
	// service-discovery entry set onto a CXO feed the SD aggregates, off the
	// 90s HTTP re-POST (each a fresh dmsg Noise handshake). Depends on disc
	// (the SD clients whose entries it mirrors) and dmsgC (the feed
	// transport). See init_sd_reg_cxo.go.
	m.sdRegCXOMod = maker("sd_reg_cxo", initSDRegCXO, &m.disc, &m.dmsgC)
	m.vis = vinit.MakeModule("visor", vinit.DoNothing, logger, &m.ebc, &m.ar, &m.disc, &m.ptyModule,
		&m.tr, &m.rt, &m.launch, &m.cli, &m.hvs, &m.ut, &m.pv, &m.pvs, &m.trs, &m.stcpC, &m.stcprC, &m.quicC, &m.wsC, &m.wtC, &m.skyFwd, &m.pi, &m.dmsgPi, &m.dmsgSrv, &m.dmsgServerLatency, &m.systemSurvey, &m.tc, &m.tpdco, &m.embTPS, &m.embRouteSetup, &m.embDmsgWeb, &m.embFwdProxy, &m.embSkynetWeb, &m.embResolvers, &m.meshProxy, &m.embSkymailBridge, &m.skymail, &m.embWisp, &m.uiServer, &m.nodeHealth, &m.selfProbe, &m.skynetPorts, &m.statsMod, &m.cxoUserFeedsMod, &m.pairingMod, &m.groupingMod, &m.voiceMod, &m.coinNodesMod, &m.regCXOMod, &m.arBindCXOMod, &m.sdRegCXOMod)

	// Hypervisor includes the full visor module tree so all services
	// (CLI, transports, pings, public visor, etc.) run in hypervisor mode.
	m.hv = maker("hypervisor", initHypervisor, &m.vis)
	return m
}

type initFn func(context.Context, *Visor, *logging.Logger) error

// ErrNoVisorInCtx is returned when visor is not set in module initialization context
var ErrNoVisorInCtx = errors.New("visor not set in module initialization context")

// ErrNoErrorsCtx is returned when errors channel is not set in module initialization context
var ErrNoErrorsCtx = errors.New("errors not set in module initialization context")

// withInitCtx wraps init function and returns a hook that can be used in
// the module system
// Passed context should have visor value under visorKey key, this visor will be used
// in the passed function
// Passed context should have errors channel for module runtime errors. It can be accessed
// through a function call
func withInitCtx(f initFn) vinit.Hook {
	return func(ctx context.Context, log *logging.Logger) (err error) {
		val := ctx.Value(visorKey)
		v, ok := val.(*Visor)
		if !ok && v == nil {
			return ErrNoVisorInCtx
		}
		val = ctx.Value(runtimeErrsKey)
		errs, ok := val.(chan error)
		if !ok && errs == nil {
			return ErrNoErrorsCtx
		}
		// Recover a panicking module init so one bad module can't crash the
		// whole visor process. This matters most during Resume (Suspend/Resume,
		// api_suspend.go), which RE-RUNS the module graph and can hit
		// non-idempotent init (a module init runs in its own goroutine under
		// InitConcurrent, so an un-recovered panic there kills the process, not
		// just the module). Convert it to an error the init system reports, and
		// log the stack so the offending module is identifiable.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("module init panicked: %v", r)
				logging.LogRecovered(log, "module init", r)
			}
		}()
		return f(ctx, v, log)
	}
}

func getErrors(ctx context.Context) chan error {
	val := ctx.Value(runtimeErrsKey)
	errs, ok := val.(chan error)
	if !ok && errs == nil {
		// ok to panic because with check for this value in withInitCtx
		// probably will never be reached, but better than generic NPE just in case
		panic("runtime errors channel is not set in context")
	}
	return errs
}

func getHTTPClient(ctx context.Context, v *Visor, service string) (*http.Client, error) {

	var serviceURL dmsgcurl.URL
	var delegatedServers []cipher.PubKey
	err := serviceURL.Fill(service)

	if serviceURL.Scheme == "dmsg" {
		if err != nil {
			return nil, fmt.Errorf("provided URL is invalid: %w", err)
		}
		// Defense-in-depth: v.dClient is seeded from the embedded deployment
		// set in initDmsgHTTP so it is normally never nil, but an
		// empty-keyring build could still leave it nil — fail the call with a
		// clear error instead of nil-dereferencing and crashing visor startup.
		if v.dClient == nil {
			return nil, fmt.Errorf("dmsg direct client unavailable (no configured/cached/embedded dmsg servers); cannot resolve %s", serviceURL.Host)
		}
		// get delegated servers and add them to the client entry
		servers, err := v.dClient.AvailableServers(ctx)
		if err != nil {
			return nil, fmt.Errorf("error getting AvailableServers: %w", err)
		}
		// randomize dmsg servers list for load distribution (not security-
		// sensitive — a weak RNG is fine and crypto/rand would be needless).
		rand.Shuffle(len(servers), func(i, j int) { //nolint:gosec // non-crypto shuffle for dmsg-server load distribution
			servers[i], servers[j] = servers[j], servers[i]
		})
		for _, server := range servers {
			delegatedServers = append(delegatedServers, server.Static)
		}

		clientEntry := &dmsgdisc.Entry{
			Client: &dmsgdisc.Client{
				DelegatedServers: delegatedServers,
			},
			Static: serviceURL.Addr.PK,
		}

		err = v.dClient.PostEntry(ctx, clientEntry)
		if err != nil {
			return nil, fmt.Errorf("error saving clientEntry: %w", err)
		}
		// Wait for the background DMSG HTTP transport to become ready.
		// initDmsgHTTP starts the connection asynchronously; the channel
		// is closed once v.dmsgHTTP is set.
		select {
		case <-v.dmsgHTTPReady:
		case <-ctx.Done():
			return nil, fmt.Errorf("DMSG HTTP transport not ready: %w", ctx.Err())
		}
		if v.dmsgHTTP == nil {
			return nil, fmt.Errorf("DMSG HTTP transport failed to initialize")
		}
		return v.dmsgHTTP, nil
	}
	// Deployment services are dmsg-only: plain HTTP to a deployment service is no
	// longer supported. A non-dmsg URL here is a misconfiguration (a clearnet
	// service URL) — fail loud instead of silently building a clearnet client.
	return nil, fmt.Errorf("deployment service %q is not a dmsg:// URL — plain HTTP to deployment services is no longer supported", service)
}
