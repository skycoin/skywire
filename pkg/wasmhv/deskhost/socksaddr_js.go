//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/socksaddr_js.go c3-vis-wasm
//
// The shell's `curl -x`: a clearnet fetch names a local SOCKS5 proxy ADDRESS —
// 127.0.0.1:1080 — and we speak standard SOCKS5 to whatever listens there
// over the page's virtual loopback (github.com/0magnet/bottle vnet). The
// canonical listener is the in-process skysocks-client app started with
// `skywire cli proxy start <exit>` in a websh terminal, exactly the Linux
// ritual; but ANY listener on the port table works. TLS terminates in-tab, so
// https is end-to-end to the origin.
package deskhost

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"

	"github.com/0magnet/bottle/vnet"

	"github.com/skycoin/skywire/pkg/wasmhv/cabundle"
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
// SOCKS5-tunneled through the local proxy at addr: TLS in-tab against the
// embedded root bundle, header timeout, forward dialer on vnet.
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
			TLSClientConfig:       &tls.Config{RootCAs: cabundle.Pool(), MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   20 * time.Second,
			MaxIdleConns:          8,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		Timeout: 120 * time.Second,
	}
	socksClients[addr] = c
	return c, nil
}
