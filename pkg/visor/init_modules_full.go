//go:build !mobile

// Package visor pkg/visor/init_modules_full.go c3-vis-core
//
// The full module graph: every module the visor has, for the desktop, server
// and wasm builds. The lite phone core registers the same fields in
// init_modules_mobile.go.
package visor

import (
	"github.com/skycoin/skywire/pkg/logging"
	vinit "github.com/skycoin/skywire/pkg/visor/visorinit"
)

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
