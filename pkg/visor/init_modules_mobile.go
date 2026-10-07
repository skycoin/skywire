//go:build mobile

// Package visor pkg/visor/init_modules_mobile.go c3-vis-core
//
// The lite phone core's module graph (`mobile` tag; Android's
// libskywire-mobile.so and the iOS SkywireCore). A phone runs the four client
// apps — skychat, skysocks-client, vpn-client, skydex-client — over every
// transport type, answers the local API the apps call, keeps Fleet and remote
// management, and nothing else: no metrics, host stats, server roles or
// embedded infrastructure nodes.
//
// Every field of modules is registered here, the dropped ones as
// vinit.DoNothing with no dependencies, so the kept modules keep exactly the
// dependency lists they have in init_modules_full.go (launch still waits on
// the embedded resolvers, rt on the embedded route setup, tm on the dmsghttp
// log server); a no-op dependency completes at once. A dropped module's init
// function is then referenced by nothing in this build, so the linker leaves
// the function out — though not always its package, which other API code may
// still use. The Visor fields those functions would have set stay nil; each
// reader is nil-guarded (the PR that introduced this file lists them).
package visor

import (
	"github.com/skycoin/skywire/pkg/logging"
	vinit "github.com/skycoin/skywire/pkg/visor/visorinit"
)

// registerModules builds the phone's module graph. See init_modules_full.go
// for what each module does; the comments here say only why a module is or
// is not on the phone.
func registerModules(logger *logging.MasterLogger) *modules {
	m := &modules{}
	maker := func(name string, f initFn, deps ...*vinit.Module) vinit.Module {
		return vinit.MakeModule(name, withInitCtx(f), logger, deps...)
	}
	dropped := func(name string) vinit.Module {
		return vinit.MakeModule(name, vinit.DoNothing, logger)
	}

	// Kept: dmsg, every transport type, the router, the launcher and the
	// discovery clients the client apps need.
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
	m.dmsgTrackers = maker("dmsg_trackers", initDmsgTrackers, &m.dmsgC)
	m.routerListener = maker("router_listener", initRouterListener, &m.dmsgC)
	m.rt = maker("router", initRouter, &m.tr, &m.dmsgC, &m.dmsgHTTP, &m.embRouteSetup, &m.routerListener)
	m.launch = maker("launcher", initLauncher, &m.ebc, &m.disc, &m.dmsgC, &m.tr, &m.rt, &m.embDmsgWeb, &m.embSkynetWeb, &m.embResolvers, &m.skymail)
	// cli carries remote management (dmsg visor-RPC) as well as the local
	// RPC listener, which the phone profile switches off (cli_addr "").
	m.cli = maker("cli", initCLI, &m.tr)
	m.hvs = maker("hypervisors", initHypervisors, &m.dmsgC, &m.tr)
	m.pv = maker("public_autoconnect", initPublicAutoconnect, &m.tr, &m.disc)
	m.trs = maker("transport_setup", initTransportSetup, &m.dmsgC, &m.tr)
	m.tm = vinit.MakeModule("transports", vinit.DoNothing, logger, &m.sc, &m.sudphC, &m.dmsgCtrl, &m.dmsgHTTPLogServer, &m.dmsgTrackers, &m.launch)
	m.pi = maker("ping", initPing, &m.dmsgC, &m.tm)
	// dmsg ping answers skychat's presence probes.
	m.dmsgPi = maker("dmsg_ping", initDmsgPing, &m.dmsgC)
	// The client apps' data path: the direct (0-hop) dial over an existing
	// non-dmsg transport and the inbound skynet-forward/app-direct handlers.
	// Without it every app dial goes through route setup (seconds slower)
	// and a peer dialing the phone directly waits for a fallback.
	m.skyFwd = maker("sky_forward_conn", initSkywireForwardConn, &m.dmsgC, &m.dmsgCtrl, &m.tr, &m.launch)
	// Without the stats module there is no CXO transport-list publisher, so
	// this HTTP reconcile is the only TPD cleanup the phone has.
	m.tpdco = maker("tpd_concurrency", initEnsureTPDConcurrency, &m.dmsgC, &m.tm)
	// Skychat's feeds, groups and calls. cxo_user_feeds keeps its stats
	// dependency (a no-op here); it only needs the logserver hookup, which it
	// nil-guards.
	m.cxoUserFeedsMod = maker("cxo_user_feeds", initCXOUserFeeds, &m.statsMod, &m.dmsgC)
	m.pairingMod = maker("pairing", initPairing, &m.dmsgC)
	m.groupingMod = maker("grouping", initGrouping, &m.dmsgC)
	m.voiceMod = maker("voice", initVoice, &m.dmsgC)

	// Dropped. Remote shell and file copy (the phone profile removes pty);
	// the dmsghttp log server and system survey; host telemetry (stats), the
	// uptime tracker and the transportability self-check; public-visor
	// advertising; every server role and embedded infrastructure node; the
	// browsing proxies and resolvers; skymail; the tpviz UI server; node
	// health; skynet port auto-registration and the self-probe; coin-node
	// forwarding (the wallet calls nodes directly); the registration/AR/SD
	// mirrors over CXO (the HTTP paths stay); and the deployment services
	// embedded on dmsg :80.
	m.ptyModule = dropped("dmsg_pty")
	m.dmsgHTTPLogServer = dropped("dmsghttp_logserver")
	m.systemSurvey = dropped("system_survey")
	m.statsMod = dropped("stats")
	m.ut = dropped("uptime_tracker")
	m.tc = dropped("transportable")
	m.pvs = dropped("public_visor")
	m.dmsgSrv = dropped("dmsg_server")
	m.dmsgServerLatency = dropped("dmsg_server_latency")
	m.embTPS = dropped("embedded_tps")
	m.embRouteSetup = dropped("embedded_route_setup")
	m.embDmsgWeb = dropped("embedded_dmsgweb")
	m.embWisp = dropped("embedded_wisp")
	m.embFwdProxy = dropped("dmsg_forward_proxy")
	m.embSkynetWeb = dropped("embedded_skynetweb")
	m.embResolvers = dropped("embedded_resolvers")
	m.meshProxy = dropped("mesh_proxy")
	m.embSkymailBridge = dropped("embedded_skymail_bridge")
	m.skymail = dropped("skymail")
	m.uiServer = dropped("ui_server")
	m.nodeHealth = dropped("node_health")
	m.selfProbe = dropped("self_probe")
	m.skynetPorts = dropped("skynet_ports")
	m.coinNodesMod = dropped("coin_nodes")
	m.regCXOMod = dropped("registration_cxo")
	m.arBindCXOMod = dropped("ar_bind_cxo")
	m.sdRegCXOMod = dropped("sd_reg_cxo")
	m.embeddedServices = dropped("embedded_services")

	m.vis = vinit.MakeModule("visor", vinit.DoNothing, logger, &m.ebc, &m.ar, &m.disc,
		&m.tr, &m.rt, &m.launch, &m.cli, &m.hvs, &m.pv, &m.trs, &m.stcpC, &m.stcprC, &m.quicC, &m.wsC, &m.wtC,
		&m.skyFwd, &m.pi, &m.dmsgPi, &m.tpdco, &m.cxoUserFeedsMod, &m.pairingMod, &m.groupingMod, &m.voiceMod)
	// Hypervisor: the local API the app talks to (plus Fleet, opt-in).
	m.hv = maker("hypervisor", initHypervisor, &m.vis)
	return m
}
