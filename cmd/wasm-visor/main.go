//go:build js && wasm

// Package main — cmd/wasm-visor: a browser/wasm skywire visor assembled from
// the TinyGo-ported subsystems (dmsg client + transport.Manager + edge router +
// in-process app server), bypassing the 45k-LOC pkg/visor daemon (which serves
// an HTTP API surface a browser leaf does not need).
//
// This is the EDGE assembly: the tab dials ONE outbound dmsg transport, runs a
// transport.Manager + an edge router (it RECEIVES route rules and forwards/
// consumes packets — "reachability != listening"), and hosts a ProcManager for
// future in-process apps (skychat). It boots but does not yet register its edge
// in TPD (that path is HTTP/native; a dmsg-based registration is the next step)
// nor run an app — see the TODOs.
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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
	types "github.com/skycoin/skywire/pkg/transport/types"
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

func main() {
	ctx = context.Background()
	js.Global().Set("skywireVisor", js.ValueOf(map[string]interface{}{
		"boot":          js.FuncOf(jsBoot),
		"status":        js.FuncOf(jsStatus),
		"hvApi":         js.FuncOf(jsHvAPI),
		"tpdEdge":       js.FuncOf(jsTPDEdge),
		"dialTransport": js.FuncOf(jsDialTransport),
		"fetchDmsg":     js.FuncOf(jsFetchDmsg),
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
	// forwarding) plus the in-process app host (procManager). No app is running
	// yet — that's the in-process-skychat step.
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

	mLog := logging.NewMasterLogger()

	// 1. dmsg client (browser WebSocket carrier — see cmd/dmsg-wasm).
	var seedPK cipher.PubKey
	if err := seedPK.UnmarshalText([]byte(seedPKHex)); err != nil {
		return pk, fmt.Errorf("bad seed server pk: %w", err)
	}
	seed := &disc.Entry{Version: "0.0.1", Static: seedPK, Server: &disc.Server{AddressWS: seedWSURL}}
	vlog("dmsg: connecting…")
	c, _, err := dmsgclient.StartDmsgSeeded(ctx, mLog.PackageLogger("dmsg"), pk, sk, []*disc.Entry{seed}, discDmsgAddr, true)
	if err != nil {
		return pk, fmt.Errorf("dmsg: %w", err)
	}
	dmsgC = c
	vlog("dmsg: ok")

	// 2. transport manager (dmsg-only). The transport-discovery client registers
	// the tab's transport edges OVER DMSG (net/http-free), against the deployment's
	// transport_discovery_dmsg endpoint — so peers' route-finders can find paths to
	// the tab once it dials a visor↔visor transport.
	tpdPK, perr := dmsgURLPK(deployment.Prod.TransportDiscoveryDmsg)
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
	for _, s := range deployment.Prod.StunServers {
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
	rConf := &router.Config{
		Logger:             mLog.PackageLogger("router"),
		MasterLogger:       mLog,
		PubKey:             pk,
		SecKey:             sk,
		TransportManager:   tm,
		AwaitSetupListener: setupLis,
	}
	vlog("router: New…")
	r, err := router.New(dmsgC, rConf, nil)
	if err != nil {
		return pk, fmt.Errorf("router: %w", err)
	}
	rtr = r
	vlog("router: New ok; serving…")
	go func() {
		if err := r.Serve(ctx); err != nil {
			mLog.PackageLogger("router").WithError(err).Error("router.Serve returned")
		}
	}()
	vlog("router: serving")

	vlog("router: serving — EDGE up (dmsg + transport + router)")

	// 4. in-process app server (RunModeInternal). The browser-adapted
	// appserver.NewProcManager no longer net.Listen("tcp")s under TinyGo (a
	// browser can't); in-process apps connect over net.Pipe. addr "" → no TCP
	// ingress. No app registered yet — in-process skychat (an appcommon.AppFunc)
	// is the next step.
	vlog("proc_manager: New…")
	pm, err := appserver.NewProcManager(mLog, &appdisc.Factory{}, eb, "", "")
	if err != nil {
		return pk, fmt.Errorf("proc manager: %w", err)
	}
	procM = pm
	vlog("proc_manager: ok")

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
		status, respHeaders, b, err := dmsgclient.FetchOverDmsg(ctx, dmsgC, method, pkHost, path, nil, body)
		if err != nil {
			return nil, err
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

func (visorSelf) SelfOverview() wasmhv.Overview {
	ov := wasmhv.Overview{PubKey: selfPK, BuildInfo: buildinfo.Get()}
	if rtr != nil {
		ov.RoutesCount = rtr.RoutesCount()
	}
	return ov
}

func (s visorSelf) SelfSummary() wasmhv.Summary {
	ov := s.SelfOverview()
	return wasmhv.Summary{
		Overview:     &ov,
		Health:       &wasmhv.HealthInfo{ServicesHealth: "healthy"},
		Online:       true,
		IsHypervisor: true,
	}
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
