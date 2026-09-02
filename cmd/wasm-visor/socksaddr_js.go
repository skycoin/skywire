//go:build js && wasm

// Package main cmd/wasm-visor/socksaddr_js.go c3-vis-wasm
//
// The Firefox-parity proxy channel: instead of an exit PUBLIC KEY, a clearnet
// fetch (or the shell's curl -x) names a local SOCKS5 proxy ADDRESS —
// 127.0.0.1:1080 — and we speak standard SOCKS5 to whatever listens there
// over the page's virtual loopback (github.com/0magnet/bottle vnet). The
// canonical listener is the in-process skysocks-client app started with
// `skywire cli proxy start <exit>` in a websh terminal, exactly the Linux
// ritual; but ANY listener on the port table works. TLS still terminates
// in-tab, so https is end-to-end to the origin.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"golang.org/x/net/proxy"

	"github.com/0magnet/bottle/vnet"
)

// socksAddrRe matches the accepted spellings of a local proxy address:
// socks5h://host:port, socks5://host:port, host:port, :port.
var socksAddrRe = regexp.MustCompile(`^(?:socks5h?://)?([A-Za-z0-9.\-]*:\d{1,5})$`)

// socksAddrOf normalizes a proxy-address spelling to "host:port", or returns
// "" when s is not address-shaped (e.g. it is an exit public key).
func socksAddrOf(s string) string {
	m := socksAddrRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return ""
	}
	hp := m[1]
	if strings.HasPrefix(hp, ":") {
		hp = "127.0.0.1" + hp
	}
	return hp
}

// vnetForward adapts bottle/vnet dialing to the proxy.Dialer contract.
// Loopback addresses ride the page port table; anything else falls through to
// net dialing inside vnet.DialTimeout (which errors under js — honest).
type vnetForward struct{}

func (vnetForward) Dial(network, addr string) (net.Conn, error) {
	return vnet.DialTimeout(network, addr, 20*time.Second)
}

var (
	socksClientMu sync.Mutex
	socksClients  = map[string]*http.Client{}
)

// socksProxyHTTPClient returns a cached http.Client whose every connection is
// SOCKS5-tunneled through the local proxy at addr. Mirrors
// skysocksHTTPClient's transport shape (TLS-in-tab, header timeout) with the
// forward dialer swapped from a yamux stream to a vnet dial.
func socksProxyHTTPClient(addr string) (*http.Client, error) {
	socksClientMu.Lock()
	defer socksClientMu.Unlock()
	if c, ok := socksClients[addr]; ok {
		return c, nil
	}
	sd, err := proxy.SOCKS5("tcp", addr, nil, vnetForward{})
	if err != nil {
		return nil, err
	}
	dialCtx := func(_ context.Context, network, a string) (net.Conn, error) {
		return sd.Dial(network, a)
	}
	if cd, ok := sd.(proxy.ContextDialer); ok {
		dialCtx = cd.DialContext
	}
	c := &http.Client{
		Transport: &http.Transport{
			DialContext:           dialCtx,
			TLSClientConfig:       &tls.Config{RootCAs: caPool, MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   20 * time.Second,
			MaxIdleConns:          8,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		Timeout: 120 * time.Second,
	}
	socksClients[addr] = c
	return c, nil
}

// fetchViaSocks performs one request through the local proxy at addr and
// returns the response (body capped at 16MB, matching jsFetchClearnet).
func fetchViaSocks(ctx context.Context, addr, method, rawURL string, body []byte, extraHeaders map[string]string) (int, []byte, http.Header, error) {
	client, err := socksProxyHTTPClient(addr)
	if err != nil {
		return 0, nil, nil, err
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("User-Agent", browserUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("via proxy %s: %w", addr, err)
	}
	defer resp.Body.Close()                               //nolint:errcheck
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20)) //nolint:errcheck
	return resp.StatusCode, b, resp.Header, nil
}

// shapeFetchResult builds the {status, body, headers} object every fetch hook
// resolves with.
func shapeFetchResult(status int, b []byte, hdr http.Header) js.Value {
	res := js.Global().Get("Object").New()
	res.Set("status", status)
	buf := js.Global().Get("Uint8Array").New(len(b))
	js.CopyBytesToJS(buf, b)
	res.Set("body", buf)
	hdrs := js.Global().Get("Object").New()
	for k := range hdr {
		hdrs.Set(k, hdr.Get(k))
	}
	res.Set("headers", hdrs)
	return res
}
