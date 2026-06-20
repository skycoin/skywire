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
	"net/http"
	"syscall/js"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

var (
	client *dmsg.Client
	ctx    context.Context
)

func main() {
	ctx = context.Background()
	api := map[string]interface{}{
		"connect": js.FuncOf(jsConnect),
		"dial":    js.FuncOf(jsDial),
		"listen":  js.FuncOf(jsListen),
	}
	js.Global().Set("skywireDmsg", js.ValueOf(api))
	// Keep the Go runtime alive for the page lifetime.
	select {}
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

// jsConnect(skHexOrEmpty, discoveryURL) -> Promise<pkHex>. Empty secret key
// generates an ephemeral identity. Forces PreferWS so the session is dialed
// over the server's WebSocket endpoint (the only transport a browser has).
func jsConnect(_ js.Value, args []js.Value) interface{} {
	skHex := args[0].String()
	discoveryURL := args[1].String()
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

		log := logging.NewMasterLogger().PackageLogger("dmsg_wasm")
		dc := disc.NewHTTP(discoveryURL, http.DefaultClient, log)
		conf := dmsg.DefaultConfig()
		conf.PreferWS = true // browser: WebSocket transport only
		client = dmsg.NewClient(pk, sk, dc, conf)
		client.SetLogger(log)
		go client.Serve(ctx)

		select {
		case <-client.Ready():
		case <-ctx.Done():
			return nil, ctx.Err()
		}
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
