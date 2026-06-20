//go:build js && wasm

// Package main — WASM browser dmsg client.
//
// A standard-Go js/wasm build of the dmsg client (NOT TinyGo — the client
// pulls logrus + encoding/gob via the RPC paths, which need full reflection).
// The browser sandbox can't open raw TCP/UDP sockets, so this build forces
// Config.PreferWS: every session is dialed over the dmsg server's WebSocket
// endpoint (Server.AddressWS). Inbound reachability still works — once the
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
	"io"
	"net/http"
	"strings"
	"syscall/js"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/wasmhv"
)

var (
	client   *dmsg.Client
	dmsgHTTP *http.Client // HTTP-over-dmsg client, built after connect
	hvCore   *wasmhv.Core // set in standalone-hypervisor mode
	selfPK   cipher.PubKey
	ctx      context.Context
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
	}
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
// client connects to it over WS (forced PreferWS), then upgrades discovery to
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
		// HTTP-over-dmsg client for jsFetch (the browser hypervisor UI's transport).
		dmsgHTTP = &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, c)}
		return pk.Hex(), nil
	})
}

// jsFetch(pkHostHex, method, path, bodyOrNull) -> Promise<{status, body}>.
//
// Performs an HTTP request over dmsg to dmsg://<pkHost><path>, where pkHost is a
// public key (defaulting to the dmsg-HTTP port :80) or an explicit "pk:port".
// This is the transport the browser hypervisor UI uses to talk to a remote
// visor/hypervisor BY PUBLIC KEY — no clearnet, no exposed HTTP port. The
// request rides the client's existing dmsg session(s).
func jsFetch(_ js.Value, args []js.Value) interface{} {
	pkHost := args[0].String()
	method := args[1].String()
	path := args[2].String()
	var body string
	if len(args) > 3 && !args[3].IsNull() && !args[3].IsUndefined() {
		body = args[3].String()
	}
	// Optional request headers object (args[4]) — e.g. Cookie / X-CSRF-Token, so
	// the proxied request carries the page's auth.
	var reqHeaders js.Value
	hasHeaders := false
	if len(args) > 4 && !args[4].IsNull() && !args[4].IsUndefined() {
		reqHeaders = args[4]
		hasHeaders = true
	}
	return promise(func() (interface{}, error) {
		if dmsgHTTP == nil {
			return nil, errors.New("not connected; call connect() first")
		}
		host := pkHost
		if !strings.Contains(host, ":") {
			host += ":80" // default dmsg-HTTP port
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		var bodyR io.Reader
		if body != "" {
			bodyR = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, "dmsg://"+host+path, bodyR)
		if err != nil {
			return nil, err
		}
		if hasHeaders {
			keys := js.Global().Get("Object").Call("keys", reqHeaders)
			for i := 0; i < keys.Length(); i++ {
				k := keys.Index(i).String()
				req.Header.Set(k, reqHeaders.Get(k).String())
			}
		}
		resp, err := dmsgHTTP.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close() //nolint:errcheck
		b, _ := io.ReadAll(resp.Body)

		// Binary-safe body (Uint8Array) + headers, so the Service Worker can
		// build a correct Response for any content type (JS/CSS/HTML/fonts/etc).
		res := js.Global().Get("Object").New()
		res.Set("status", resp.StatusCode)
		buf := js.Global().Get("Uint8Array").New(len(b))
		js.CopyBytesToJS(buf, b)
		res.Set("body", buf)
		headers := js.Global().Get("Object").New()
		for k := range resp.Header {
			headers.Set(k, resp.Header.Get(k))
		}
		res.Set("headers", headers)
		return res, nil
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
