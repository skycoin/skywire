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
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/tpdclient"
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
		"boot":    js.FuncOf(jsBoot),
		"status":  js.FuncOf(jsStatus),
		"hvApi":   js.FuncOf(jsHvAPI),
		"tpdEdge": js.FuncOf(jsTPDEdge),
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
	factory := network.ClientFactory{PK: pk, SK: sk, DmsgC: dmsgC, MLogger: mLog, EB: eb}
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
