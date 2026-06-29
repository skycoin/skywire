//go:build js && wasm

// Package main — cmd/wasm-visor: a browser/wasm skywire visor assembled from
// the TinyGo-ported subsystems (dmsg client + transport.Manager + edge router +
// in-process app server), bypassing the 45k-LOC pkg/visor daemon (which serves
// an HTTP API surface a browser leaf does not need).
//
// This is the EDGE assembly: the tab dials outbound transports (dmsg + the
// browser-dialable WS/WT/WebRTC), runs a transport.Manager + an edge router (it
// RECEIVES route rules and forwards/consumes packets — "reachability !=
// listening"), and hosts a ProcManager running in-process skychat (dmsg:1). Its
// transport edges ARE registered in TPD over dmsg (net/http-free tpdclient.NewDmsg
// → transport_discovery_dmsg), so a peer's route-finder can compute a route to the
// tab — verified: `cli route calc <host> <tab-pk>` returns a forward/reverse pair
// over the tab's transport. (Route SETUP/forwarding over a transport to a browser
// leaf is a separate matter; skychat to a tab is reliably reached over dmsg:1.)
//
// JS API on globalThis.skywireVisor:
//
//	const pk = await skywireVisor.boot(skHexOrEmpty, seedPkHex, seedWsURL, discDmsgAddr)
//	skywireVisor.status()  // → { pk, booted, dmsg, tpManager, router, procManager, transports, routes }
//	// dial a direct visor↔visor transport from this tab to a peer that listens:
//	await skywireVisor.dialTransport(peerPkHex, "wt", "https://host:port/skywire", certHashHex)
//	await skywireVisor.dialTransport(peerPkHex, "ws", "ws://host:port/")
//	await skywireVisor.dialTransport(peerPkHex, "webrtc") // direct DataChannel, signaling over dmsg
//	// fetch arbitrary content over dmsg (browse a skynet/dmsg site by PK):
//	await skywireVisor.fetchDmsg("<pk>" /*or "pk:port"*/, "GET", "/", null) // → {status, body, headers}
//	// self-host content over dmsg (others reach it via fetchDmsg(<this-pk>, ...)):
//	skywireVisor.serveContent({ "/": {ct:"text/html", body:"<h1>hi from a tab</h1>"} })
//
// Build + run the dev harness (index.html drives boot/status/reload, and connects
// back to the cmd/dmsg-wasm serve.go control bridge so a shell can drive the tab):
//
//	make tinygo-wasm-visor                       # → build/wasm-visor/
//	go run cmd/dmsg-wasm/serve.go -dir build/wasm-visor
//	# open http://localhost:8085/ , then drive from a shell:
//	curl -s localhost:8085/ctl/tabs
//	curl -s -XPOST 'localhost:8085/ctl/cmd?tab=<id>' \
//	     -d '{"action":"boot","args":["","<seedPK>","<seedWS>","<discDmsgAddr>"]}'
//	curl -s 'localhost:8085/ctl/log?tab=<id>'
//
// bootEdge emits vlog() step markers through the __skylog bridge so each
// subsystem's progress is visible in /ctl/log — that is what pinned the
// router.New TinyGo-reflect hang documented below.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/google/uuid"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appdisc"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/netview"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
	"github.com/skycoin/skywire/pkg/wasmhv"
)

var (
	ctx context.Context

	selfPK cipher.PubKey
	dmsgC  *dmsg.Client
	tpM    *transport.Manager
	rtr    router.Router
	procM  appserver.ProcManager
	hvCore *wasmhv.Core
	tpd    transport.DiscoveryClient

	// wsTable / wtTable hold the dial targets (peer PK → endpoint) for the
	// browser-dialable direct transports. They are mutable so the dialTransport
	// JS hook can add a target just before SaveTransport dials it.
	wsTable stcp.PKTable
	wtTable network.WTTable

	// selfPublicIP caches this visor's own public IP as observed by the dmsg
	// servers (ClientSession.LookupIP — the server reports the source address of
	// the connection). A browser tab can't STUN (no UDP), so the dmsg-server view
	// is how it learns its public IP, exactly as the native visor's dmsg client
	// does for AR bind payloads. Refreshed by refreshSelfPublicIP; read by
	// SelfOverview. Holds a string ("" until first learned).
	selfPublicIP atomic.Value
)

// vlog emits a step message to the browser console AND, when present, the
// __skylog bridge the dev harness wires to /ctl/log — so boot progress is
// visible from the shell.
func vlog(msg string) {
	fmt.Println("[visor] " + msg)
	if h := js.Global().Get("__skylog"); h.Type() == js.TypeFunction {
		h.Invoke("[visor] " + msg)
	}
}

// pageHTTPS reports whether this wasm-visor was served over HTTPS. From an HTTPS
// origin the browser blocks plain ws:// (mixed content), so only wss:// WS seeds
// and WebTransport endpoints are dialable.
func pageHTTPS() bool {
	loc := js.Global().Get("location")
	if !loc.Truthy() {
		return false
	}
	return loc.Get("protocol").String() == "https:"
}

func main() {
	ctx = context.Background()
	js.Global().Set("skywireVisor", js.ValueOf(map[string]interface{}{
		"boot":            js.FuncOf(jsBoot),
		"status":          js.FuncOf(jsStatus),
		"hvApi":           js.FuncOf(jsHvAPI),
		"tpdEdge":         js.FuncOf(jsTPDEdge),
		"dialTransport":   js.FuncOf(jsDialTransport),
		"fetchDmsg":       js.FuncOf(jsFetchDmsg),
		"serveContent":    js.FuncOf(jsServeContent),
		"serveRPC":        js.FuncOf(jsServeRPC),
		"dialRoute":       js.FuncOf(jsDialRoute),
		"checkRegistered": js.FuncOf(jsCheckRegistered),
		"fetchClearnet":   js.FuncOf(jsFetchClearnet),
		"skychatSend":     js.FuncOf(jsSkychatSend),
		"skychatMessages": js.FuncOf(jsSkychatMessages),
	}))
	fmt.Println("wasm-visor: ready — call skywireVisor.boot(sk, seedPk, seedWs, discDmsgAddr)")
	select {} // block forever
}

// jsBoot(skHex, seedPkHex, seedWsURL, discDmsgAddr) → Promise<selfPkHex>.
func jsBoot(_ js.Value, args []js.Value) interface{} {
	skHex := args[0].String()
	seedPKHex := args[1].String()
	seedWSURL := args[2].String()
	discDmsgAddr := args[3].String()
	return promise(func() (interface{}, error) {
		pk, err := bootEdge(skHex, seedPKHex, seedWSURL, discDmsgAddr)
		if err != nil {
			return nil, err
		}
		return pk.Hex(), nil
	})
}

// jsStatus() → { pk, booted, dmsg, tpManager, router, procManager, transports, routes }.
// The boolean subsystem flags make a fully-initialized edge distinguishable from
// a half-boot (where a Serve() tripped and left a subsystem nil).
func jsStatus(js.Value, []js.Value) interface{} {
	st := map[string]interface{}{
		"pk": "", "booted": false,
		"dmsg": false, "tpManager": false, "router": false, "procManager": false,
		"hypervisor": false, "transports": 0, "routes": 0,
	}
	if !selfPK.Null() {
		st["pk"] = selfPK.Hex()
	}
	st["dmsg"] = dmsgC != nil
	st["tpManager"] = tpM != nil
	st["router"] = rtr != nil
	st["procManager"] = procM != nil
	st["hypervisor"] = hvCore != nil
	// booted = the edge (dmsg + transport + router: rule reception + packet
	// forwarding) plus the in-process app host (procManager), which runs the
	// in-process skychat app (dmsg:1).
	st["booted"] = dmsgC != nil && tpM != nil && rtr != nil && procM != nil
	if tpM != nil {
		st["transports"] = tpM.TransportCount()
	}
	if rtr != nil {
		st["routes"] = rtr.RoutesCount()
	}
	return js.ValueOf(st)
}

// bootEdge wires the edge: dmsg client → transport.Manager → edge router →
// in-process app ProcManager.
func bootEdge(skHex, seedPKHex, seedWSURL, discDmsgAddr string) (cipher.PubKey, error) {
	pk, sk, err := keysFromHex(skHex)
	if err != nil {
		return pk, err
	}
	selfPK = pk

	// Resolve the deployment service endpoints through the SHARED resolver
	// (pkg/visor/visorcore) — the same one the native visor will use — so the two
	// visors can't drift on config sourcing. nil V1 → pure deployment defaults
	// (a browser edge has no operator config file).
	svc := visorcore.ResolveServices(nil)

	// Default the discovery to the deployment's dmsg-discovery when the caller
	// didn't pass one. Without a discDmsgAddr, StartDmsgSeeded skips the discovery
	// upgrade, so the client never installs the registering fallback and never
	// publishes its entry — the tab stays unregistered (and can't be route-set-up,
	// which dials the source's own @136 over dmsg). A browser edge always wants the
	// deployment discovery; an explicit arg still overrides (e.g. a test discovery).
	if discDmsgAddr == "" {
		discDmsgAddr = svc.DmsgDiscoveryDmsg
	}

	mLog := logging.NewMasterLogger()

	// 1. dmsg client (browser WebSocket carrier). Seed from ALL embedded dmsg
	// servers via WebSocket on their advertised port — the unified dmsg-server
	// serves WS at ws://<Address>/dmsg (#3272). Multi-server connectivity mirrors
	// the non-wasm visor (its config carries the full servers[] list) and is what
	// lets a browser tab reach the discovery to register its own entry — the
	// prerequisite for inbound reachability and route setup. A boot-arg seed
	// (seedPk/seedWs), when given, is added or overrides (e.g. a server whose WS
	// is on a SEPARATE port). Servers not yet serving main-port WS just fail to
	// dial and are skipped (best-effort); the client settles on the reachable set.
	// Carrier choice is page-scheme-aware. An HTTPS page may ONLY open wss://
	// (the browser blocks plain ws:// as mixed content), so it seeds exclusively
	// from servers that advertise a wss:// AddressWS (a TLS-fronted domain) and
	// skips the rest. An http:// / file:// page derives plain ws://<Address>/dmsg
	// from the stable IP:port — no domain needed for plain ws. AddressWS is a
	// SEPARATE field from Address, so native visors are unaffected either way.
	https := pageHTTPS()
	seedsByPK := map[cipher.PubKey]*disc.Entry{}
	skipped := 0
	for _, ds := range svc.DmsgServers {
		var spk cipher.PubKey
		if spk.UnmarshalText([]byte(ds.Static)) != nil {
			continue
		}
		var wsURL string
		if https {
			if !strings.HasPrefix(strings.ToLower(ds.Server.AddressWS), "wss://") {
				skipped++ // no wss endpoint → unreachable from an HTTPS page
				continue
			}
			wsURL = ds.Server.AddressWS
		} else if ds.Server.Address != "" {
			wsURL = "ws://" + ds.Server.Address + "/dmsg"
		} else if ds.Server.AddressWS != "" {
			wsURL = ds.Server.AddressWS
		} else {
			continue
		}
		seedsByPK[spk] = &disc.Entry{Version: "0.0.1", Static: spk, Server: &disc.Server{AddressWS: wsURL}}
	}
	if https && skipped > 0 {
		vlog(fmt.Sprintf("dmsg: HTTPS page — skipped %d dmsg server(s) with no wss:// endpoint (browser blocks plain ws://)", skipped))
	}
	if seedPKHex != "" && seedWSURL != "" {
		var spk cipher.PubKey
		if err := spk.UnmarshalText([]byte(seedPKHex)); err != nil {
			return pk, fmt.Errorf("bad seed server pk: %w", err)
		}
		seedsByPK[spk] = &disc.Entry{Version: "0.0.1", Static: spk, Server: &disc.Server{AddressWS: seedWSURL}}
	}
	seeds := make([]*disc.Entry, 0, len(seedsByPK))
	for _, e := range seedsByPK {
		seeds = append(seeds, e)
	}
	if len(seeds) == 0 {
		if https {
			return pk, errors.New("no usable dmsg seed servers on an HTTPS page: no embedded server advertises a wss:// endpoint (the browser blocks plain ws://). Give the dmsg servers a TLS-fronted wss:// AddressWS (e.g. a Caddy reverse_proxy on a subdomain)")
		}
		return pk, errors.New("no dmsg seed servers (embedded set empty and no seedPk/seedWs provided)")
	}
	vlog(fmt.Sprintf("dmsg: connecting (%d WS seed servers)…", len(seeds)))
	// Preload ALL the deployment's non-registering SERVICE clients delegated to the
	// seed servers — they're dmsg DIRECT clients and never publish to the
	// dmsg-discovery, so without this a DialStream to them 404s ("entry is not found
	// in discovery"). This MUST cover the same set the native visor seeds via
	// dmsgServicePKs() (init_dmsg.go) — dmsgd, tpd, ar, rf, sd, conf, ut — or the two
	// visors diverge: omitting sd/ar/ut here left the wasm-visor's network-view /
	// services-health / uptime aggregation empty (DialStream to the SD 404'd) while
	// the native visor worked. dmsgURLPK errors are non-fatal (a null PK is skipped).
	var servicePKs []cipher.PubKey
	for _, u := range []string{
		svc.DmsgDiscoveryDmsg,
		svc.TransportDiscoveryDmsg,
		svc.AddressResolverDmsg,
		svc.RouteFinderDmsg,
		svc.ServiceDiscoveryDmsg,
		svc.ConfDmsg,
		svc.UptimeTrackerDmsg,
	} {
		if spk, e := dmsgURLPK(u); e == nil {
			servicePKs = append(servicePKs, spk)
		}
	}
	servicePKs = append(servicePKs, svc.RouteSetupNodes...)
	c, _, err := dmsgclient.StartDmsgSeeded(ctx, mLog.PackageLogger("dmsg"), pk, sk, seeds, discDmsgAddr, true, servicePKs...)
	if err != nil {
		return pk, fmt.Errorf("dmsg: %w", err)
	}
	dmsgC = c
	vlog("dmsg: ok")
	go refreshSelfPublicIP(ctx)

	// 2. transport manager (dmsg-only). The transport-discovery client registers
	// the tab's transport edges OVER DMSG (net/http-free), against the deployment's
	// transport_discovery_dmsg endpoint — so peers' route-finders can find paths to
	// the tab once it dials a visor↔visor transport.
	tpdPK, perr := dmsgURLPK(svc.TransportDiscoveryDmsg)
	if perr != nil {
		return pk, fmt.Errorf("tpd dmsg url: %w", perr)
	}
	tpd = tpdclient.NewDmsg(dmsgC, tpdPK, pk, sk, mLog)
	eb := appevent.NewBroadcaster(mLog.PackageLogger("eb"), time.Second)
	// Browser-dialable direct transports (WS / WT). The tables start empty and are
	// populated at dial time by the dialTransport JS hook; the tab can DIAL these
	// (browser WebSocket / WebTransport) but never accept them.
	wsTable = stcp.NewTable(nil)
	wtTable = network.NewWTTable(nil)
	// WebRTC ICE servers: the deployment's own STUN (reused from sudph). The
	// bare host:port entries need the stun: URL scheme the WebRTC stack expects.
	var iceURLs []string
	for _, s := range svc.StunServers {
		iceURLs = append(iceURLs, "stun:"+s)
	}
	factory := network.ClientFactory{
		PK: pk, SK: sk, DmsgC: dmsgC, MLogger: mLog, EB: eb,
		WSTable: wsTable, WTTable: wtTable, ICEURLs: iceURLs,
	}
	tmConf := &transport.ManagerConfig{
		PubKey:          pk,
		SecKey:          sk,
		DiscoveryClient: tpd,
		LogStore:        transport.InMemoryTransportLogStore(),
	}
	tm, err := transport.NewManager(mLog.PackageLogger("tp_manager"), nil, eb, tmConf, factory)
	if err != nil {
		return pk, fmt.Errorf("transport manager: %w", err)
	}
	tpM = tm
	vlog("tp_manager: ok; init dmsg client…")
	tm.InitDmsgClient(ctx, dmsgC)
	vlog("tp_manager: dmsg client inited; serving…")
	go tm.Serve(ctx)
	vlog("tp_manager: serving")
	// Register the browser-dialable direct transport clients. Their Start() fails
	// closed under TinyGo (a tab can't run a WS/WT listener) — logged, non-fatal —
	// but Dial works, so SaveTransport(WS|WT) can create an outbound transport to a
	// peer that runs the listener. This makes WS/WT "known" networks for the
	// dialTransport hook.
	tm.InitClient(ctx, types.WS, 0)
	tm.InitClient(ctx, types.WT, 0)
	// WebRTC is symmetric: InitClient also starts the dmsg signaling listener
	// (port 47), so the tab can ACCEPT WebRTC DataChannels, not just dial them.
	tm.InitClient(ctx, types.WEBRTC, 0)
	vlog("tp_manager: WS/WT/WebRTC dial clients registered")

	// 3. edge router (receives route rules + forwards/consumes packets). nil
	// RouteFinder/RouteGroupDialer → the route-SOURCE path is the build-tagged
	// TinyGo stub (a browser leaf does not originate routes). We open the dmsg
	// setup listener here and pass it as AwaitSetupListener.
	vlog("router: dmsg Listen(setup :136)…")
	setupLis, lerr := dmsgC.Listen(skyenv.DmsgAwaitSetupPort)
	if lerr != nil {
		return pk, fmt.Errorf("setup listen: %w", lerr)
	}
	vlog("router: setup listener ok")
	// router.New() used to HANG here under TinyGo: it registers the setup RPC
	// gateway, and gobimpl's reflection-based server called reflect.Type.Method(i),
	// which TinyGo's runtime reflect doesn't support (NumMethod works, Method(i)
	// never returns). Fixed by the reflection-free gobrpc server — the router now
	// registers explicit handlers under TinyGo (router_setup_rpc_tinygo.go) instead
	// of reflection. Validated in-browser via this harness.
	// Route ORIGINATION: query the route-finder over dmsg and dial route groups
	// via the deployment's setup nodes, so this tab can SET UP multihop (and mux)
	// routes — not just receive rules and forward. Legacy setup path
	// (forceLegacy=true); no embedded route-setup node in a browser leaf. The
	// std-Go build compiles the full route-source (the TinyGo stub does not apply).
	rfHTTP := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
	rfClient := rfclient.NewHTTP(svc.RouteFinderDmsg, 10*time.Second, rfHTTP, mLog)
	rgDialer := router.NewSetupNodeDialerFull(nil, router.NewRSNRelayCache(mLog.PackageLogger("router")), tm, true)
	vlog(fmt.Sprintf("router: route origination wired (rf=%s, %d setup nodes)", svc.RouteFinderDmsg, len(svc.RouteSetupNodes)))

	// Assemble + serve the router through the shared visorcore.BuildRouter so the
	// edge and the native visor can't drift on the router.Config field mapping
	// (e.g. the MinHops==0 ⇒ "routing disabled" gotcha) or the Serve pattern.
	// svc.MinHops is 1 (origination enabled; a direct transport still downgrades to
	// a 0-intermediate-hop path).
	vlog("router: New + serve…")
	r, err := visorcore.BuildRouter(ctx, visorcore.RouterDeps{
		DmsgC:              dmsgC,
		PubKey:             pk,
		SecKey:             sk,
		TransportManager:   tm,
		RouteFinder:        rfClient,
		RouteGroupDialer:   rgDialer,
		SetupNodes:         svc.RouteSetupNodes,
		MinHops:            svc.MinHops,
		AwaitSetupListener: setupLis,
		Logger:             mLog.PackageLogger("router"),
		MasterLogger:       mLog,
	})
	if err != nil {
		return pk, fmt.Errorf("router: %w", err)
	}
	rtr = r
	vlog("router: serving")

	vlog("router: serving — EDGE up (dmsg + transport + router)")

	// resolver aliases for the in-tab browser (home.dmsg, tpd.dmsg, dmsg0.dmsg, …),
	// so it resolves the same names as the socks5 resolving proxy.
	initResolver(svc, pk)

	// public autoconnect: dial WS transports to public visors (which expose WS on
	// their stcpr port, phase 2) so this browser leaf joins the mesh + routes form.
	startWSAutoconnect(ctx, svc.ServiceDiscoveryDmsg, svc.AddressResolverDmsg, pk, sk)

	// wss → WebTransport convergence: the browser bootstraps its dmsg session over
	// wss (the only carrier reachable before discovery), then prefers WT for
	// further sessions (Carriers=[wt,ws]). Once a WT session is live, drop the
	// lingering wss one so we shed its redundant TLS-over-Noise. Safe + idempotent
	// (never strands the client); a no-op until a dmsg server actually serves WT.
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n := dmsgC.UpgradeBrowserSessions(); n > 0 {
					vlog(fmt.Sprintf("dmsg: converged %d wss session(s) to WebTransport", n))
				}
			}
		}
	}()

	// 4. in-process app server (RunModeInternal). The browser-adapted
	// appserver.NewProcManager no longer net.Listen("tcp")s under TinyGo (a
	// browser can't); in-process apps connect over net.Pipe. addr "" → no TCP
	// ingress.
	vlog("proc_manager: New…")
	pm, err := appserver.NewProcManager(mLog, &appdisc.Factory{}, eb, "", "")
	if err != nil {
		return pk, fmt.Errorf("proc manager: %w", err)
	}
	procM = pm
	vlog("proc_manager: ok")

	// 4a. in-process skychat (the browser visor's first app). Register BOTH app
	// networkers so app.Client can listen/dial over dmsg (direct) AND skynet (over
	// a route): the dmsg networker rides dmsgC, the skynet networker rides the edge
	// router. skychat listens on both — wire-compatible with native skychat
	// (useDmsg+useSkynet), so a peer dialing skychat over a ROUTE (the host's
	// default `--net skynet`) reaches the tab, not just dmsg-direct.
	if aerr := appnet.AddNetworker(appnet.TypeDmsg, appnet.NewDMSGNetworker(dmsgC)); aerr != nil {
		vlog("skychat: add dmsg networker: " + aerr.Error())
	}
	if aerr := appnet.AddNetworker(appnet.TypeSkynet, appnet.NewSkywireNetworker(mLog.PackageLogger("skynet"), rtr)); aerr != nil {
		vlog("skychat: add skynet networker: " + aerr.Error())
	}
	if _, serr := pm.Start(appcommon.ProcConfig{
		AppName:     "skychat",
		ProcKey:     appcommon.RandProcKey(),
		VisorPK:     pk,
		RoutingPort: skychatPort,
		RunFunc:     runBrowserSkychat,
		RunMode:     appcommon.RunModeInternal,
	}); serr != nil {
		vlog("skychat: start: " + serr.Error())
	} else {
		vlog("skychat: in-process app started (dmsg:1 + skynet:1)")
	}

	// 5. hypervisor core: the tab also acts as a hypervisor — visors dial INTO it
	// on the dmsg hypervisor port and it RPC-controls them (gobrpc CLIENT, which
	// works under TinyGo). The HV UI's /api requests are served in-wasm via
	// skywireVisor.hvApi(). This is the HV-UI path: the tab is a visor edge AND a
	// hypervisor, so skychat etc. are reached through the HV UI rather than by
	// porting the app.
	vlog("hypervisor: serving…")
	hvCore = wasmhv.NewCore(pk, dmsgC)
	// The tab is a visor AND its own hypervisor: surface THIS visor in the HV UI
	// (identity / routes / transports), read from its own transport.Manager +
	// router, alongside any remote visors that dial in.
	hvCore.SetSelf(visorSelf{})
	go func() {
		if err := hvCore.Serve(ctx); err != nil {
			mLog.PackageLogger("hypervisor").WithError(err).Error("hvCore.Serve returned")
		}
	}()
	vlog("hypervisor: serving")

	// peer-interface parity: open the dmsg listeners a native visor serves so
	// other visors / hypervisors / the CLI can reach, health-check, latency-probe,
	// and (with authorization) command transports on this browser visor:
	//   :80 health/landing/ping · :7 dmsgctrl · :8 dmsg ping · :47 transport-setup.
	startPeerServices(mLog, svc.TransportSetupNodes)
	vlog("peer services: health(:80)/ctrl(:7)/ping(:8)/transport-setup(:47) up")

	vlog("EDGE + app-host + hypervisor booted")
	fmt.Printf("wasm-visor: booted pk=%s\n", pk.Hex())
	return pk, nil
}

// jsHvAPI(method, path, bodyOrNull) → Promise<{status, body}>. Serves a
// hypervisor /api request from the in-wasm wasmhv.Core.
func jsHvAPI(_ js.Value, args []js.Value) interface{} {
	method := args[0].String()
	path := args[1].String()
	var body []byte
	if len(args) > 2 && !args[2].IsNull() && !args[2].IsUndefined() {
		body = []byte(args[2].String())
	}
	return promise(func() (interface{}, error) {
		if hvCore == nil {
			return nil, errors.New("hypervisor not serving; boot() first")
		}
		status, b := hvCore.ServeHTTP(method, path, body)
		res := js.Global().Get("Object").New()
		res.Set("status", status)
		buf := js.Global().Get("Uint8Array").New(len(b))
		js.CopyBytesToJS(buf, b)
		res.Set("body", buf)
		return res, nil
	})
}

// jsFetchDmsg(pkHost, method, path, bodyOrNull) → Promise<{status, body, headers}>.
// Fetches arbitrary content over dmsg (HTTP/1.1 over a dmsg stream, net/http-free)
// from another visor/site addressed by PK — the primitive for browsing skynet/dmsg
// websites from the tab and for reaching any dmsg-served HTTP endpoint. pkHost is a
// PK or "pk:port" (port defaults to 80). IP-anonymous + uncensorable: no DNS, no
// IP, all over dmsg.
func jsFetchDmsg(_ js.Value, args []js.Value) interface{} {
	pkHost := args[0].String()
	method := "GET"
	if len(args) > 1 && !args[1].IsNull() && !args[1].IsUndefined() && args[1].String() != "" {
		method = args[1].String()
	}
	path := "/"
	if len(args) > 2 && args[2].String() != "" {
		path = args[2].String()
	}
	var body []byte
	if len(args) > 3 && !args[3].IsNull() && !args[3].IsUndefined() {
		body = []byte(args[3].String())
	}
	return promise(func() (interface{}, error) {
		if dmsgC == nil {
			return nil, errors.New("not booted; call boot() first")
		}
		// Resolve resolver aliases (home.dmsg, tpd.dmsg, <pk>.dmsg) like the socks5
		// resolving proxy, so the in-tab browser uses the same names.
		resolved, homeBody := resolveFetchHost(pkHost)
		var status int
		var respHeaders map[string]string
		var b []byte
		if homeBody != nil {
			status, respHeaders, b = 200, map[string]string{"Content-Type": "text/html"}, homeBody
		} else {
			var err error
			status, respHeaders, b, err = dmsgclient.FetchOverDmsg(ctx, dmsgC, method, resolved, path, nil, body)
			if err != nil {
				return nil, err
			}
		}
		res := js.Global().Get("Object").New()
		res.Set("status", status)
		buf := js.Global().Get("Uint8Array").New(len(b))
		js.CopyBytesToJS(buf, b)
		res.Set("body", buf)
		hdrs := js.Global().Get("Object").New()
		for k, v := range respHeaders {
			hdrs.Set(k, v)
		}
		res.Set("headers", hdrs)
		return res, nil
	})
}

func keysFromHex(skHex string) (cipher.PubKey, cipher.SecKey, error) {
	if skHex == "" {
		pk, sk := cipher.GenerateKeyPair()
		return pk, sk, nil
	}
	var sk cipher.SecKey
	if err := sk.UnmarshalText([]byte(skHex)); err != nil {
		return cipher.PubKey{}, cipher.SecKey{}, fmt.Errorf("bad secret key: %w", err)
	}
	pk, err := sk.PubKey()
	if err != nil {
		return cipher.PubKey{}, cipher.SecKey{}, fmt.Errorf("derive public key: %w", err)
	}
	return pk, sk, nil
}

// promise wraps a blocking Go function as a JS Promise.
func promise(fn func() (interface{}, error)) interface{} {
	handler := js.FuncOf(func(_ js.Value, pArgs []js.Value) interface{} {
		resolve, reject := pArgs[0], pArgs[1]
		go func() {
			v, err := fn()
			if err != nil {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
				return
			}
			resolve.Invoke(js.ValueOf(v))
		}()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}

// dmsgURLPK extracts the public key from a "dmsg://<pk>:<port>" URL.
func dmsgURLPK(raw string) (cipher.PubKey, error) {
	s := strings.TrimPrefix(raw, "dmsg://")
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	var pk cipher.PubKey
	if err := pk.UnmarshalText([]byte(s)); err != nil {
		return cipher.PubKey{}, fmt.Errorf("parse pk from %q: %w", raw, err)
	}
	return pk, nil
}

// jsTPDEdge(pkHex) → Promise<entriesJSON>. Queries the transport discovery (over
// dmsg) for the transports registered by edge pkHex — used to validate the
// dmsg-TPD client against the live TPD (e.g. this host's full visor and its many
// registered transports).
func jsTPDEdge(_ js.Value, args []js.Value) interface{} {
	pkHex := args[0].String()
	return promise(func() (interface{}, error) {
		if tpd == nil {
			return nil, errors.New("not booted; call boot() first")
		}
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			return nil, fmt.Errorf("bad pk: %w", err)
		}
		entries, err := tpd.GetTransportsByEdge(ctx, pk)
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(entries) //nolint:errcheck
		return string(b), nil
	})
}

// visorSelf is the wasmhv.SelfProvider for the tab's own visor: it reads the
// local identity, route count, and transports straight from this process's
// router + transport.Manager, so the HV UI shows THIS visor (not just remote
// visors that dial in).
type visorSelf struct{}

func (visorSelf) SelfPK() cipher.PubKey { return selfPK }

func (s visorSelf) SelfOverview() wasmhv.Overview {
	ov := wasmhv.Overview{PubKey: selfPK, BuildInfo: buildinfo.Get()}
	if rtr != nil {
		ov.RoutesCount = rtr.RoutesCount()
	}
	// Surface our live transports in the overview so the hypervisor node table's
	// "Transports" count reflects reality (the autoconnect WS transports). The
	// transports tab reads SelfTransports() directly; the summary reads this.
	ov.SetSelfTransports(s.SelfTransports())
	if ip, ok := selfPublicIP.Load().(string); ok {
		ov.PublicIP = ip
	}
	return ov
}

// refreshSelfPublicIP learns this tab's public IP from the dmsg servers
// (ClientSession.LookupIP: the server replies with the source address of our
// connection) and caches it for SelfOverview. A browser can't STUN, so this is
// how a wasm visor learns its public IP — the same dmsg-observed source the
// native client feeds into AR bind payloads. Retries until learned, then
// refreshes periodically (a browser's IP can change across network moves).
func refreshSelfPublicIP(ctx context.Context) {
	for {
		delay := 10 * time.Second
		if _, ok := selfPublicIP.Load().(string); !ok {
			delay = 3 * time.Second // not learned yet — poll faster
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if dmsgC == nil {
			continue
		}
		v4, v6 := dmsgC.LookupIPsByFamily(ctx)
		ip := ""
		switch {
		case v4 != nil:
			ip = v4.String()
		case v6 != nil:
			ip = v6.String()
		}
		if ip == "" {
			continue
		}
		if prev, _ := selfPublicIP.Load().(string); prev != ip {
			selfPublicIP.Store(ip)
			vlog("self public IP (via dmsg server): " + ip)
		}
	}
}

func (s visorSelf) SelfSummary() wasmhv.Summary {
	ov := s.SelfOverview()
	return wasmhv.Summary{
		Overview: &ov,
		Health:   &wasmhv.HealthInfo{ServicesHealth: "healthy"},
		// Non-nil: the CLI's `visor info` dereferences DmsgStats.RoundTrip
		// unconditionally. The tab has no dmsg-tracker, so RoundTrip stays 0.
		DmsgStats: &wasmhv.DmsgClientSummary{PK: selfPK},
		// A browser visor has no on-disk config file, so there is no separate
		// "config version" — its config IS its build. Report the build version so
		// the HV UI shows it (and treats version == config_version as up-to-date)
		// instead of rendering "Unknown".
		ConfigVersion: buildinfo.Version(),
		BuildTag:      "wasm",
		Online:        true,
		IsHypervisor:  true,
	}
}

// tpController is the wasmhv.TransportController backing the RPC gateway's
// transport-control methods (the CLI's `tp add`/`tp rm`), over this tab's
// transport.Manager. webrtc/dmsg dial by PK alone; ws/wt need an endpoint and
// must go through skywireVisor.dialTransport (which carries the url/cert).
type tpController struct{}

func (tpController) AddTransport(remote cipher.PubKey, tpType string, _ time.Duration) (*wasmhv.TransportSummary, error) {
	if tpM == nil {
		return nil, errors.New("not booted; call boot() first")
	}
	t := types.Type(tpType)
	switch t {
	case types.WEBRTC, types.DMSG:
		// no endpoint needed (webrtc signals over dmsg; dmsg dials by PK)
	case types.WS, types.WT:
		return nil, fmt.Errorf("%s needs a peer endpoint over the CLI path; use skywireVisor.dialTransport(pk, %q, url[, certHash])", tpType, tpType)
	default:
		return nil, fmt.Errorf("unsupported transport type %q (cli: webrtc or dmsg)", tpType)
	}
	tp, err := tpM.SaveTransport(ctx, remote, t, transport.LabelUser)
	if err != nil {
		return nil, fmt.Errorf("save transport: %w", err)
	}
	return &wasmhv.TransportSummary{
		ID:     tp.Entry.ID,
		Local:  selfPK,
		Remote: remote,
		Type:   tpType,
		Label:  string(transport.LabelUser),
	}, nil
}

func (tpController) RemoveTransport(id uuid.UUID) error {
	if tpM == nil {
		return errors.New("not booted; call boot() first")
	}
	tpM.DeleteTransport(id)
	return nil
}

func (visorSelf) SelfTransports() []*wasmhv.TransportSummary {
	out := []*wasmhv.TransportSummary{}
	if tpM == nil {
		return out
	}
	tpM.WalkTransports(func(mt *transport.ManagedTransport) bool {
		out = append(out, &wasmhv.TransportSummary{
			ID:     mt.Entry.ID,
			Local:  selfPK,
			Remote: mt.Remote(),
			Type:   string(mt.Type()),
			Label:  string(mt.Entry.Label),
		})
		return true
	})
	return out
}

// routeResp mirrors pkg/visor's routingRuleResp so the hypervisor Routing tab
// gets the same JSON shape from a wasm-visor as from a native one.
type routeResp struct {
	Key     routing.RouteID      `json:"key"`
	Rule    string               `json:"rule"`
	Summary *routing.RuleSummary `json:"rule_summary,omitempty"`
}

// SelfRoutes returns this tab's own routing rules as JSON matching the native
// /visors/{pk}/routes shape. rule.Summary() is the panic-safe converter (it
// switches on the rule type — unlike the Next*ID accessors, which panic on
// Consume/Reverse rules), and rule bytes hex-encode directly.
func (visorSelf) SelfRoutes() []byte {
	if rtr == nil {
		return nil
	}
	rules := rtr.Rules()
	resp := make([]routeResp, 0, len(rules))
	for _, rule := range rules {
		resp = append(resp, routeResp{
			Key:     rule.KeyRouteID(),
			Rule:    hex.EncodeToString(rule),
			Summary: rule.Summary(),
		})
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	return b
}

// SelfNetworkView builds the SD/TPD/UT-aggregated network table (the native
// /api/network-view shape) from this tab's OWN dmsg fetch of the deployment
// services, using the SHARED netview.Compute so it can't drift from the native
// visor. The deployment services are dmsg DIRECT clients reachable since the
// service-PK seed fix; without this the wasm core 404s /api/network-view and the
// network-view table + visualizer render empty in the browser.
func (visorSelf) SelfNetworkView() []byte {
	if dmsgC == nil {
		return nil
	}
	svc := visorcore.ResolveServices(nil)
	hostFor := func(dmsgURL string) string {
		if pk, e := dmsgURLPK(dmsgURL); e == nil {
			return pk.Hex()
		}
		return ""
	}
	hosts := map[string]string{
		"sd":  hostFor(svc.ServiceDiscoveryDmsg),
		"tpd": hostFor(svc.TransportDiscoveryDmsg),
		"ut":  hostFor(svc.UptimeTrackerDmsg),
	}
	fetch := func(service, path string) ([]byte, error) {
		host := hosts[service]
		if host == "" {
			return nil, fmt.Errorf("no dmsg url for %s", service)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		status, _, body, err := dmsgclient.FetchOverDmsg(ctx, dmsgC, "GET", host, path, nil, nil)
		if err != nil {
			return nil, err
		}
		if status != 200 {
			return nil, fmt.Errorf("%s: status %d", service, status)
		}
		return body, nil
	}
	b, err := json.Marshal(netview.Compute(fetch))
	if err != nil {
		return nil
	}
	return b
}

// jsDialTransport(pkHex, netType, url, certHash) creates a direct visor↔visor
// transport from this tab to a peer that runs the listener:
//   - netType "ws": url is the peer's ws:// (or wss://) WebSocket endpoint.
//   - netType "wt": url is the peer's https:// WebTransport endpoint and
//     certHash is the lowercase SHA-256 hex of its self-signed cert (CA-free,
//     the browser serverCertificateHashes model).
//   - netType "webrtc": no url — signaling rides dmsg by PK; a direct DataChannel
//     is negotiated to the peer (which must also run a WebRTC transport).
//
// It registers any dial target in the appropriate table, then SaveTransport dials
// it and registers the new edge in the TPD over dmsg — so peers' route-finders can
// path to this tab over the new link. Returns the transport ID on success.
func jsDialTransport(_ js.Value, args []js.Value) interface{} {
	pkHex := args[0].String()
	netType := args[1].String()
	url := ""
	if len(args) > 2 {
		url = args[2].String()
	}
	certHash := ""
	if len(args) > 3 {
		certHash = args[3].String()
	}
	return promise(func() (interface{}, error) {
		if tpM == nil {
			return nil, errors.New("not booted; call boot() first")
		}
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			return nil, fmt.Errorf("bad pk: %w", err)
		}
		switch types.Type(netType) {
		case types.WS:
			if wsTable == nil {
				return nil, errors.New("ws table not initialized")
			}
			wsTable.SetAddr(pk, url)
		case types.WT:
			if wtTable == nil {
				return nil, errors.New("wt table not initialized")
			}
			if certHash == "" {
				return nil, errors.New("wt requires a cert hash (4th arg)")
			}
			wtTable.SetEntry(pk, network.WTEntry{URL: url, CertHash: certHash})
		case types.WEBRTC:
			// No dial target: WebRTC signals to the peer by PK over dmsg.
		default:
			return nil, fmt.Errorf("unsupported transport type %q (use ws, wt, or webrtc)", netType)
		}
		tp, err := tpM.SaveTransport(ctx, pk, types.Type(netType), transport.LabelUser)
		if err != nil {
			return nil, fmt.Errorf("save transport: %w", err)
		}
		return tp.Entry.ID.String(), nil
	})
}

// jsDialRoute(pkHex, port) → Promise<{ok, remote, local}>. Originates a route
// group to a remote visor:port through the ROUTER (the routing layer, using the
// wired route-finder + setup nodes) — proving end-to-end route setup works from a
// browser tab. The remote must be reachable via a transport the route-finder
// knows (so make a transport toward it first). This is the same DialRoutes the
// in-tab skysocks-client rides; the probe closes the route group immediately.
func jsDialRoute(_ js.Value, args []js.Value) interface{} {
	pkHex := args[0].String()
	port := 80
	if len(args) > 1 && args[1].Truthy() {
		port = args[1].Int()
	}
	return promise(func() (interface{}, error) {
		if rtr == nil {
			return nil, errors.New("not booted; call boot() first")
		}
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			return nil, fmt.Errorf("bad pk: %w", err)
		}
		dctx, cancel := context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
		conn, err := rtr.DialRoutes(dctx, pk, 0, routing.Port(port), router.DefaultDialOptions())
		if err != nil {
			return nil, fmt.Errorf("dial route: %w", err)
		}
		defer conn.Close() //nolint:errcheck
		res := js.Global().Get("Object").New()
		res.Set("ok", true)
		res.Set("remote", conn.RemoteAddr().String())
		res.Set("local", conn.LocalAddr().String())
		return res, nil
	})
}

// jsCheckRegistered() → Promise<{registered, status, detail}>. Diagnoses dmsg
// discovery registration: GETs this tab's OWN entry from the dmsg-discovery over
// the tab's dmsg-HTTP. 200 = registered (RSN can reach us for route setup); 404 =
// reachable but NOT published (the registration write isn't landing); a transport
// error = the tab can't even reach the discovery over its one seed session
// (bootstrap reachability). See docs/design/wasm-visor-discovery-registration.md.
func jsCheckRegistered(_ js.Value, _ []js.Value) interface{} {
	return promise(func() (interface{}, error) {
		if dmsgC == nil {
			return nil, errors.New("not booted; call boot() first")
		}
		httpC := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC), Timeout: 25 * time.Second}
		reqURL := deployment.Prod.DmsgDiscoveryDmsg + "/dmsg-discovery/entry/" + selfPK.Hex()
		dctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(dctx, http.MethodGet, reqURL, nil)
		res := js.Global().Get("Object").New()
		resp, err := httpC.Do(req)
		if err != nil {
			res.Set("registered", false)
			res.Set("status", 0)
			res.Set("detail", "reach discovery: "+err.Error())
			return res, nil
		}
		defer resp.Body.Close() //nolint:errcheck
		body, _ := io.ReadAll(resp.Body)
		if len(body) > 200 {
			body = body[:200]
		}
		res.Set("registered", resp.StatusCode == http.StatusOK)
		res.Set("status", resp.StatusCode)
		res.Set("detail", string(body))
		return res, nil
	})
}
