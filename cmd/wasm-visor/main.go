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
	"fmt"
	"syscall/js"
	"time"

	"github.com/google/uuid"

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
)

var (
	ctx context.Context

	selfPK cipher.PubKey
	dmsgC  *dmsg.Client
	tpM    *transport.Manager
	rtr    router.Router
	procM  appserver.ProcManager
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
		"boot":   js.FuncOf(jsBoot),
		"status": js.FuncOf(jsStatus),
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
		"transports": 0, "routes": 0,
	}
	if !selfPK.Null() {
		st["pk"] = selfPK.Hex()
	}
	st["dmsg"] = dmsgC != nil
	st["tpManager"] = tpM != nil
	st["router"] = rtr != nil
	st["procManager"] = procM != nil
	// The EDGE is dmsg + transport + router (rule reception + packet forwarding).
	// procManager (in-process app hosting) is a separate, later concern — it
	// net.Listen("tcp")s, which a browser can't do, so it is not on the edge
	// boot path yet.
	st["booted"] = dmsgC != nil && tpM != nil && rtr != nil
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

	// 2. transport manager (dmsg-only; no AR, no TPD registration yet).
	eb := appevent.NewBroadcaster(mLog.PackageLogger("eb"), time.Second)
	factory := network.ClientFactory{PK: pk, SK: sk, DmsgC: dmsgC, MLogger: mLog, EB: eb}
	tmConf := &transport.ManagerConfig{
		PubKey:          pk,
		SecKey:          sk,
		DiscoveryClient: noopTPD{},
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

	// EDGE booted: dmsg + transport + router are up. The tab now receives route
	// rules and forwards/consumes packets — a routable skynet edge.
	vlog("EDGE booted: dmsg + transport + router up")
	fmt.Printf("wasm-visor: edge booted pk=%s\n", pk.Hex())

	// 4. in-process app server (RunModeInternal) — DEFERRED (procM stays nil).
	// appserver.NewProcManager net.Listen("tcp")s for the external-app IPC
	// ingress, which a browser cannot do (it blocks under TinyGo/js). In-process
	// skychat needs a browser-adapted ProcManager (no TCP ingress; net.Pipe
	// in-process conns only) — the next step toward "edge + in-process skychat".
	// Wiring NewProcManager here would hang the boot.

	return pk, nil
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

// noopTPD is a no-op transport.DiscoveryClient: a browser edge does not (yet)
// register its transports in the transport discovery (that path is HTTP/native;
// a dmsg-based TPD client is a follow-up). Route setup TO this edge therefore
// relies on the dialing peer already knowing the edge's transport.
type noopTPD struct{}

func (noopTPD) RegisterTransports(context.Context, ...*transport.SignedEntry) error { return nil }
func (noopTPD) RegisterTransportsV3(context.Context, string, ...*transport.Entry) error {
	return nil
}
func (noopTPD) GetTransportByID(context.Context, uuid.UUID) (*transport.Entry, error) {
	return nil, nil
}
func (noopTPD) GetTransportsByEdge(context.Context, cipher.PubKey) ([]*transport.Entry, error) {
	return nil, nil
}
func (noopTPD) GetAllTransports(context.Context) ([]*transport.Entry, error) { return nil, nil }
func (noopTPD) GetTransportStats(context.Context, cipher.PubKey) (*transport.TransportStats, error) {
	return nil, nil
}
func (noopTPD) GetAllTransportsStats(context.Context) (*transport.NetworkTransportStats, error) {
	return nil, nil
}
func (noopTPD) GetAllTransportsPerKeyStats(context.Context) (transport.PerKeyStats, error) {
	return nil, nil
}
func (noopTPD) DeleteTransport(context.Context, uuid.UUID) error { return nil }
func (noopTPD) DeleteTransports(context.Context, []uuid.UUID) (int, error) {
	return 0, nil
}
