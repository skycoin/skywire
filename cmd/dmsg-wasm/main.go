//go:build js && wasm

// Package main — WASM browser dmsg client.
//
// A standard-Go js/wasm build of the dmsg client (NOT TinyGo — the client
// pulls logrus + encoding/gob via the RPC paths, which need full reflection).
// The browser sandbox can't open raw TCP/UDP sockets, so this build forces the
// WS carrier (Config.Carriers = ["ws"]): every session is dialed over the dmsg
// server's WebSocket endpoint (Server.AddressWS). Inbound reachability still works — once the
// client has an outbound WSS session to a dmsg server, peers dial it BY PUBLIC
// KEY and the server bridges those streams back down the same WSS connection.
// So a browser tab is a first-class, inbound-reachable dmsg peer.
//
// It exposes a small JS API on globalThis.skywireDmsg:
//
//	const pk = await skywireDmsg.connect(skHexOrEmpty, "https://dmsgd...")
//	skywireDmsg.listen(80, stream => { stream.onMessage(m => ...); stream.send("hi") })
//	const s = await skywireDmsg.dial(remotePkHex, 80); s.send("hello")
//
// Build: GOOS=js GOARCH=wasm go build -o dmsg.wasm ./cmd/dmsg-wasm
package main

import (
	"context"
	"errors"
	"fmt"
	"syscall/js"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/wasmhv"
)

var (
	client *dmsg.Client
	hvCore *wasmhv.Core // set in standalone-hypervisor mode
	selfPK cipher.PubKey
	ctx    context.Context
)

func main() {
	ctx = context.Background()
	api := map[string]interface{}{
		"connect":         js.FuncOf(jsConnect),
		"dial":            js.FuncOf(jsDial),
		"listen":          js.FuncOf(jsListen),
		"fetch":           js.FuncOf(jsFetch),
		"serveHypervisor": js.FuncOf(jsServeHypervisor),
		"hvApi":           js.FuncOf(jsHvAPI),
		"webrtcDial":      js.FuncOf(jsWebrtcDial),
		"webrtcListen":    js.FuncOf(jsWebrtcListen),
	}
	// Expose the deployment's own STUN servers (the ones the visor uses for sudph
	// NAT detection) as WebRTC ICE server URLs — so WebRTC uses skywire infra, not
	// a third-party STUN.
	//
	// TODO(wasm-visor): this uses the EMBEDDED default deployment config
	// (deployment.Prod). When this UI is generated/embedded BY a visor (the
	// standalone-HV generator, `cli hv gen`), it must instead inject THAT visor's
	// runtime config — its configured StunServers, dmsg servers, discovery, and
	// service URLs — which may differ from the embedded defaults (custom/private
	// deployments). The generator already inlines config; STUN should ride along.
	var stun []interface{}
	for _, s := range deployment.Prod.StunServers {
		stun = append(stun, "stun:"+s)
	}
	api["stunServers"] = stun
	js.Global().Set("skywireDmsg", js.ValueOf(api))
	// Keep the Go runtime alive for the page lifetime.
	select {}
}

// jsServeHypervisor() -> nil. STANDALONE hypervisor mode: start accepting visor
// dials on the hypervisor dmsg port (46). Visors that list this client's PK as
// a hypervisor dial in and are RPC'd by the in-wasm hypervisor core. Call after
// connect(). The UI then routes /api to hvApi() (in-wasm) instead of fetch()
// (remote hypervisor over dmsg).
func jsServeHypervisor(js.Value, []js.Value) interface{} {
	if client == nil {
		return js.Global().Get("Error").New("not connected; call connect() first")
	}
	hvCore = wasmhv.NewCore(selfPK, client)
	go hvCore.Serve(ctx) //nolint:errcheck
	return nil
}

// jsHvAPI(method, path, bodyOrNull) -> Promise<{status, body}>. Serves a
// hypervisor /api request from the in-wasm core (standalone mode).
func jsHvAPI(_ js.Value, args []js.Value) interface{} {
	method := args[0].String()
	path := args[1].String()
	var body []byte
	if len(args) > 2 && !args[2].IsNull() && !args[2].IsUndefined() {
		body = []byte(args[2].String())
	}
	return promise(func() (interface{}, error) {
		if hvCore == nil {
			return nil, errors.New("hypervisor not serving; call serveHypervisor() first")
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

// promise wraps a goroutine result as a JS Promise: fn returns (value, error),
// resolving or rejecting accordingly. All blocking dmsg work happens off the JS
// event loop in the goroutine.
func promise(fn func() (interface{}, error)) interface{} {
	handler := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		resolve, reject := args[0], args[1]
		go func() {
			v, err := fn()
			if err != nil {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
				return
			}
			resolve.Invoke(v)
		}()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}

// jsConnect(skHexOrEmpty, seedServerPKHex, seedServerWSURL, discDmsgAddr)
// -> Promise<pkHex>.
//
// A browser can't reach a dmsg-only discovery until it has a server, so we seed
// one server directly: its PK + WebSocket URL (e.g. "ws://host:port/dmsg"). The
// client connects to it over WS (forced WS carrier), then upgrades discovery to
// run over dmsg (discDmsgAddr = "dmsg://<disc-pk>:80") so it can register itself
// + resolve peers. Empty secret key → ephemeral identity. See
// dmsgclient.StartDmsgSeeded.
func jsConnect(_ js.Value, args []js.Value) interface{} {
	skHex := args[0].String()
	seedPKHex := args[1].String()
	seedWSURL := args[2].String()
	discDmsgAddr := args[3].String()
	return promise(func() (interface{}, error) {
		var pk cipher.PubKey
		var sk cipher.SecKey
		if skHex == "" {
			pk, sk = cipher.GenerateKeyPair()
		} else {
			if err := sk.UnmarshalText([]byte(skHex)); err != nil {
				return nil, fmt.Errorf("bad secret key: %w", err)
			}
			var err error
			if pk, err = sk.PubKey(); err != nil {
				return nil, fmt.Errorf("derive public key: %w", err)
			}
		}

		var seedPK cipher.PubKey
		if err := seedPK.UnmarshalText([]byte(seedPKHex)); err != nil {
			return nil, fmt.Errorf("bad seed server pk: %w", err)
		}
		seed := &disc.Entry{
			Version: "0.0.1",
			Static:  seedPK,
			Server:  &disc.Server{AddressWS: seedWSURL},
		}

		log := logging.NewMasterLogger().PackageLogger("dmsg_wasm")
		c, _, err := dmsgclient.StartDmsgSeeded(ctx, log, pk, sk, []*disc.Entry{seed}, discDmsgAddr, true)
		if err != nil {
			return nil, err
		}
		client = c
		selfPK = pk
		// Wire up the jsFetch transport (the browser hypervisor UI's HTTP-over-dmsg
		// path to a REMOTE hypervisor). Build-tagged: native uses net/http +
		// dmsghttp; TinyGo uses the net/http-free dmsgclient.FetchOverDmsg.
		onConnected(c)
		return pk.Hex(), nil
	})
}

// jsDial(remotePkHex, port) -> Promise<streamHandle>.
func jsDial(_ js.Value, args []js.Value) interface{} {
	remoteHex := args[0].String()
	port := uint16(args[1].Int())
	return promise(func() (interface{}, error) {
		if client == nil {
			return nil, errors.New("not connected; call connect() first")
		}
		var rPK cipher.PubKey
		if err := rPK.UnmarshalText([]byte(remoteHex)); err != nil {
			return nil, fmt.Errorf("bad remote public key: %w", err)
		}
		str, err := client.DialStream(ctx, dmsg.Addr{PK: rPK, Port: port})
		if err != nil {
			return nil, err
		}
		return streamHandle(str), nil
	})
}

// jsListen(port, onStream) -> nil. onStream(streamHandle) fires per inbound
// stream — these arrive over the existing outbound WSS session (peers dial this
// client by PK; the dmsg server bridges).
func jsListen(_ js.Value, args []js.Value) interface{} {
	port := uint16(args[0].Int())
	onStream := args[1]
	if client == nil {
		return js.Global().Get("Error").New("not connected; call connect() first")
	}
	lis, err := client.Listen(port)
	if err != nil {
		return js.Global().Get("Error").New(err.Error())
	}
	go func() {
		for {
			str, err := lis.AcceptStream()
			if err != nil {
				return
			}
			onStream.Invoke(streamHandle(str))
		}
	}()
	return nil
}

// streamHandle wraps a *dmsg.Stream as a JS object: { send(str), onMessage(cb),
// close() }. A read loop pumps inbound bytes to the registered callback. Framing
// here is naive (one Read = one message) — adequate for the signaling/chat
// payloads this is meant for; a real protocol would length-prefix.
func streamHandle(str *dmsg.Stream) js.Value {
	obj := js.Global().Get("Object").New()

	obj.Set("send", js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		msg := a[0].String()
		go str.Write([]byte(msg)) //nolint:errcheck
		return nil
	}))

	obj.Set("onMessage", js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		cb := a[0]
		go func() {
			buf := make([]byte, 32*1024)
			for {
				n, err := str.Read(buf)
				if n > 0 {
					cb.Invoke(string(buf[:n]))
				}
				if err != nil {
					return
				}
			}
		}()
		return nil
	}))

	obj.Set("close", js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
		str.Close() //nolint:errcheck
		return nil
	}))

	return obj
}
