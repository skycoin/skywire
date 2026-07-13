// Package main — in-tab BTC gateway: the wasm twin of the native HV BTC gateway
// (pkg/btcgateway). The browser skycoin-web wallet does BTC key derivation, tx
// building and signing itself (skycoin-lite WASM); it only needs chain data.
// This runs the electrum→HTTP gateway inside the tab, dialing a clearnet ssl://
// electrum server through skysocks-client-lite (over the mesh, IP-anonymous),
// and exposes it as skywireVisor.btcFetch. Keys + signing stay in the browser;
// only chain queries cross.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"syscall/js"

	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/btcgateway"
	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	btcGWOnce sync.Once
	btcGWInst *btcgateway.Gateway
	// btcExitPK is the skysocks-server exit the BTC electrum connection egresses
	// through; set per btcFetch call from localStorage['skywire-upstream-proxy']
	// (the same exit clearnet browsing uses). A single stable exit, so caching a
	// gateway with one dial closure is fine.
	btcExitPK cipher.PubKey
)

func btcGateway() *btcgateway.Gateway {
	btcGWOnce.Do(func() { btcGWInst = btcgateway.New(btcSkysocksDial) })
	return btcGWInst
}

// btcSkysocksDial dials addr (the clearnet electrum host:port) through
// skysocks-client-lite: a yamux stream to the exit + SOCKS5 CONNECT. The raw
// TCP conn is returned; the electrum client layers TLS on top for ssl://, so the
// exit relays ciphertext end-to-end.
func btcSkysocksDial(network, addr string) (net.Conn, error) {
	if btcExitPK == (cipher.PubKey{}) {
		return nil, errors.New("no skysocks exit configured for BTC (set an upstream proxy PK)")
	}
	sess, err := skysocksSession("btc-gateway", btcExitPK)
	if err != nil {
		return nil, err
	}
	sd, err := proxy.SOCKS5("tcp", "skysocks", nil, streamDialer{sess: sess})
	if err != nil {
		return nil, err
	}
	return sd.Dial(network, addr)
}

// jsBtcFetch(backend, method, path, bodyOrNull, exitPKHex) → Promise<{status, body}>.
// Runs one /v1/btc/* request against the in-tab gateway; backend is the electrum
// server (ssl://host:port), reached over the mesh through the exitPKHex skysocks
// server.
func jsBtcFetch(_ js.Value, args []js.Value) interface{} {
	if len(args) < 5 {
		return js.Global().Get("Error").New("btcFetch(backend, method, path, body, exitPK)")
	}
	backend := args[0].String()
	method := args[1].String()
	if method == "" {
		method = "GET"
	}
	path := args[2].String()
	var body []byte
	if !args[3].IsNull() && !args[3].IsUndefined() {
		body = []byte(args[3].String())
	}
	exitHex := args[4].String()
	return promise(func() (interface{}, error) {
		if exitHex != "" {
			var pk cipher.PubKey
			if err := pk.UnmarshalText([]byte(exitHex)); err != nil {
				return nil, fmt.Errorf("bad skysocks exit pk: %w", err)
			}
			btcExitPK = pk
		}
		req, err := http.NewRequest(method, path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Skywire-Btc-Backend", backend)
		rec := httptest.NewRecorder()
		btcGateway().ServeHTTP(rec, req)
		res := rec.Result()
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close() //nolint:errcheck
		out := js.Global().Get("Object").New()
		out.Set("status", res.StatusCode)
		out.Set("body", string(b))
		return out, nil
	})
}
