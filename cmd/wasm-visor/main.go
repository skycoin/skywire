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
//	skywireVisor.status()  // → { pk, transports, routes }
//
// Build: tinygo build -target wasm -o wasm-visor.wasm ./cmd/wasm-visor
package main

import (
	"context"
	"fmt"
	"syscall/js"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
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

// jsStatus() → { pk, transports, routes }.
func jsStatus(js.Value, []js.Value) interface{} {
	st := map[string]interface{}{"pk": "", "transports": 0, "routes": 0}
	if !selfPK.Null() {
		st["pk"] = selfPK.Hex()
	}
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
	c, _, err := dmsgclient.StartDmsgSeeded(ctx, mLog.PackageLogger("dmsg"), pk, sk, []*disc.Entry{seed}, discDmsgAddr, true)
	if err != nil {
		return pk, fmt.Errorf("dmsg: %w", err)
	}
	dmsgC = c

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
	tm.InitDmsgClient(ctx, dmsgC)
	go tm.Serve(ctx)

	// 3. edge router (receives route rules + forwards/consumes packets). nil
	// RouteFinder/RouteGroupDialer → the route-SOURCE path is the build-tagged
	// TinyGo stub (a browser leaf does not originate routes).
	rConf := &router.Config{
		Logger:           mLog.PackageLogger("router"),
		MasterLogger:     mLog,
		PubKey:           pk,
		SecKey:           sk,
		TransportManager: tm,
	}
	r, err := router.New(dmsgC, rConf, nil)
	if err != nil {
		return pk, fmt.Errorf("router: %w", err)
	}
	rtr = r
	go func() {
		if err := r.Serve(ctx); err != nil {
			mLog.PackageLogger("router").WithError(err).Error("router.Serve returned")
		}
	}()

	// 4. in-process app server (RunModeInternal). No app registered yet — the
	// in-process skychat AppFunc is the next step.
	pm, err := appserver.NewProcManager(mLog, &appdisc.Factory{}, eb, ":0", "")
	if err != nil {
		return pk, fmt.Errorf("proc manager: %w", err)
	}
	procM = pm

	fmt.Printf("wasm-visor: edge booted pk=%s\n", pk.Hex())
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
