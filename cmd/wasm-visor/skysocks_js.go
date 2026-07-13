//go:build js && wasm

// Package main — in-tab skysocks CLIENT: fetch CLEARNET content through a remote
// skysocks-server over a skywire route, so a browser tab can browse the clearnet
// IP-anonymously (the exit does the egress).
//
// The tab is NOT an app-framework client (no app procs in a browser); it calls
// the ROUTER directly — rtr.DialRoutes(serverPK, port 3) originates the route group
// (the routing layer, proven working), then yamux + SOCKS5 ride it exactly as a
// normal skysocks-client does. http.Transport terminates TLS in-tab for https
// (the std-Go js/wasm build has crypto/tls), so the exit only sees encrypted
// bytes to the origin.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/hashicorp/yamux"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// caBundle is a Mozilla CA root bundle, embedded because Go's js/wasm runtime has
// NO system cert pool (crypto/x509.SystemCertPool fails), so https verification
// would otherwise fail. We MUST verify — the tab does TLS end-to-end to the origin
// so the skysocks exit can't read or MITM the https stream; skipping verification
// would hand the exit exactly that power.
//
//go:embed cacert.pem
var caBundle []byte

var caPool = func() *x509.CertPool {
	p := x509.NewCertPool()
	p.AppendCertsFromPEM(caBundle)
	return p
}()

// skysocksPort is the skywire app port a skysocks-server listens on.
const skysocksPort = routing.Port(skyenv.SkysocksPort)

var (
	skysocksMu sync.Mutex
	// key = winID + "|" + exitPK.Hex(): each browser WINDOW gets its OWN
	// skysocks-client-lite session (its own route to the exit), so two windows
	// pointed at the same exit don't share one session — independent routing +
	// independently loggable. The resolving proxy (fetchDmsg) stays shared.
	skysocksSessions = map[string]*yamux.Session{}
)

func skysocksKey(winID string, pk cipher.PubKey) string { return winID + "|" + pk.Hex() }

// proxyVerbose gates detailed per-request [skysocks-lite] / [resolve-proxy]
// logging (the log lines surface in the "visor log" window). Off by default so
// normal browsing doesn't spam the log; toggle from JS via
// skywireVisor.proxyVerbose(true) to "start it with verbose debug logging".
// Route/session establishment is always logged (one line each, not per-request).
var proxyVerbose bool

// emitProxyLog logs a per-window skysocks-lite line to the visor log AND, when the
// page registered a per-window sink (browse.js's ⚙ proxy-log pane sets
// globalThis.__skywireProxyLog), forwards it to that browser window by id — so the
// window shows its own connect / route-setup step explicitly, the way
// `skywire cli proxy start --verbose` surfaces session establishment.
func emitProxyLog(winID, msg string) {
	vlog(msg)
	if h := js.Global().Get("__skywireProxyLog"); h.Type() == js.TypeFunction {
		h.Invoke(winID, msg)
	}
}

// skysocksSession returns a yamux session to the skysocks-server at serverPK,
// lazily establishing it over a fresh route group (rtr.DialRoutes — the routing
// layer). Cached per exit; a closed session is re-dialed.
func skysocksSession(winID string, serverPK cipher.PubKey) (*yamux.Session, error) {
	key := skysocksKey(winID, serverPK)
	// Fast path under the lock: an established, still-open session is reused.
	skysocksMu.Lock()
	if s, ok := skysocksSessions[key]; ok && !s.IsClosed() {
		skysocksMu.Unlock()
		if proxyVerbose {
			emitProxyLog(winID, fmt.Sprintf("[skysocks-lite %s] reuse session to exit %s", winID, serverPK.Hex()[:8]))
		}
		return s, nil
	}
	skysocksMu.Unlock()
	if rtr == nil {
		return nil, errors.New("not booted; call boot() first")
	}
	// Route setup can take many seconds. Do it WITHOUT holding skysocksMu — the
	// lock only guards the map. Holding it across DialRoutes serialized every
	// concurrent session (a 2nd browser window dialing a different exit would
	// block up to 45s behind the first), piling up goroutines. Now different
	// exits (and different windows) establish in parallel.
	t0 := time.Now()
	emitProxyLog(winID, fmt.Sprintf("[skysocks-lite %s] connecting to exit %s — setting up route…", winID, serverPK.Hex()[:8]))
	dctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	// Optimized routing for the browsing proxy: request a small mux (2 parallel
	// routes) for throughput + mid-browse resilience — if one route degrades the
	// other carries and the router's mux-auto GROW adapts. Latency weighting is
	// the route-finder default. The policy DialHook clamps this, and it degrades
	// gracefully to a single route when mux isn't available, so it's safe.
	dopts := router.DefaultDialOptions()
	dopts.MuxRoutes = 2
	conn, err := rtr.DialRoutes(dctx, serverPK, 0, skysocksPort, dopts)
	if err != nil {
		emitProxyLog(winID, fmt.Sprintf("[skysocks-lite %s] route dial to exit %s FAILED (%dms): %v", winID, serverPK.Hex()[:8], time.Since(t0).Milliseconds(), err))
		return nil, fmt.Errorf("dial skysocks route: %w", err)
	}
	sess, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("yamux client: %w", err)
	}
	// Re-acquire to publish. If a concurrent call for the SAME key won the race
	// while we were dialing, discard ours and reuse theirs (avoid leaking a route).
	skysocksMu.Lock()
	if s, ok := skysocksSessions[key]; ok && !s.IsClosed() {
		skysocksMu.Unlock()
		_ = sess.Close() //nolint:errcheck
		return s, nil
	}
	skysocksSessions[key] = sess
	skysocksMu.Unlock()
	emitProxyLog(winID, fmt.Sprintf("[skysocks-lite %s] route+session to exit %s established (%dms)", winID, serverPK.Hex()[:8], time.Since(t0).Milliseconds()))
	return sess, nil
}

// closeSkysocksWindow closes + drops every skysocks-lite session belonging to a
// browser window (called when the window closes), releasing its routes.
func closeSkysocksWindow(winID string) int {
	skysocksMu.Lock()
	defer skysocksMu.Unlock()
	prefix := winID + "|"
	n := 0
	for k, s := range skysocksSessions {
		if strings.HasPrefix(k, prefix) {
			_ = s.Close() //nolint:errcheck
			delete(skysocksSessions, k)
			n++
		}
	}
	return n
}

// streamDialer opens a fresh yamux stream per Dial. proxy.SOCKS5 runs the SOCKS5
// handshake + CONNECT over it — the skysocks-server runs a SOCKS5 server on each
// accepted stream, so this is exactly the client half.
type streamDialer struct{ sess *yamux.Session }

func (d streamDialer) Dial(_, _ string) (net.Conn, error) { return d.sess.Open() }

// skysocksHTTPClient builds an http.Client whose every connection is SOCKS5-
// tunneled through the skysocks session. http.Transport terminates TLS for https
// URLs in-tab, so https works end-to-end (the exit relays ciphertext).
func skysocksHTTPClient(sess *yamux.Session) (*http.Client, error) {
	sd, err := proxy.SOCKS5("tcp", "skysocks", nil, streamDialer{sess: sess})
	if err != nil {
		return nil, err
	}
	dialCtx := func(_ context.Context, network, addr string) (net.Conn, error) {
		return sd.Dial(network, addr) // a yamux stream + SOCKS5 CONNECT to addr
	}
	if cd, ok := sd.(proxy.ContextDialer); ok {
		dialCtx = cd.DialContext
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext:         dialCtx,
			TLSClientConfig:     &tls.Config{RootCAs: caPool, MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: 20 * time.Second,
			MaxIdleConns:        8,
		},
		Timeout: 45 * time.Second,
	}, nil
}

// jsFetchClearnet(serverPKHex, method, url, bodyOrNull) → Promise<{status, body,
// headers}>. Fetches a CLEARNET url through the skysocks-server serverPKHex over a
// skywire route: DialRoutes → yamux → SOCKS5 CONNECT → (https) TLS-in-tab → HTTP.
// IP-anonymous: the exit does the egress; the origin sees the exit's IP.
func jsFetchClearnet(_ js.Value, args []js.Value) interface{} {
	if len(args) < 3 {
		return js.Global().Get("Error").New("fetchClearnet(serverPK, method, url[, body])")
	}
	serverPKHex := args[0].String()
	method := "GET"
	if args[1].String() != "" {
		method = args[1].String()
	}
	rawURL := args[2].String()
	var body []byte
	if len(args) > 3 && !args[3].IsNull() && !args[3].IsUndefined() {
		body = []byte(args[3].String())
	}
	// Optional 5th arg: the browser window id, so each window gets its own
	// skysocks-lite session (see skysocksSessions). Absent → shared default.
	winID := "w0"
	if len(args) > 4 && !args[4].IsNull() && !args[4].IsUndefined() && args[4].String() != "" {
		winID = args[4].String()
	}
	// Optional 6th arg: a request-headers object (e.g. Content-Type), forwarded
	// verbatim so form-encoded POSTs aren't mislabeled — the clearnet twin of
	// fetchDmsg's header forwarding.
	var extraHeaders map[string]string
	if len(args) > 5 && !args[5].IsNull() && !args[5].IsUndefined() && args[5].Type() == js.TypeObject {
		keys := js.Global().Get("Object").Call("keys", args[5])
		for i := 0; i < keys.Length(); i++ {
			k := keys.Index(i).String()
			if extraHeaders == nil {
				extraHeaders = map[string]string{}
			}
			extraHeaders[k] = args[5].Get(k).String()
		}
	}
	return promise(func() (interface{}, error) {
		var spk cipher.PubKey
		if err := spk.UnmarshalText([]byte(serverPKHex)); err != nil {
			return nil, fmt.Errorf("bad skysocks server pk: %w", err)
		}
		if _, err := url.ParseRequestURI(rawURL); err != nil {
			return nil, fmt.Errorf("bad url: %w", err)
		}
		sess, err := skysocksSession(winID, spk)
		if err != nil {
			return nil, err
		}
		client, err := skysocksHTTPClient(sess)
		if err != nil {
			return nil, err
		}
		var rdr io.Reader
		if body != nil {
			rdr = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
		if err != nil {
			return nil, err
		}
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}
		t0 := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			vlog(fmt.Sprintf("[skysocks-lite %s] %s %s via %s FAILED (%dms): %v", winID, method, rawURL, spk.Hex()[:8], time.Since(t0).Milliseconds(), err))
			return nil, fmt.Errorf("fetch via skysocks: %w", err)
		}
		defer resp.Body.Close()                               //nolint:errcheck
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16MB cap
		if proxyVerbose {
			vlog(fmt.Sprintf("[skysocks-lite %s] %s %s via %s → %d (%dB, %dms)", winID, method, rawURL, spk.Hex()[:8], resp.StatusCode, len(b), time.Since(t0).Milliseconds()))
		}
		res := js.Global().Get("Object").New()
		res.Set("status", resp.StatusCode)
		buf := js.Global().Get("Uint8Array").New(len(b))
		js.CopyBytesToJS(buf, b)
		res.Set("body", buf)
		hdrs := js.Global().Get("Object").New()
		for k := range resp.Header {
			hdrs.Set(k, resp.Header.Get(k))
		}
		res.Set("headers", hdrs)
		return res, nil
	})
}

// jsProxyVerbose(bool) toggles detailed per-request logging for BOTH the
// skysocks-lite (fetchClearnet) and resolving-proxy (fetchDmsg) paths. With it
// on, every request logs method/url/status/bytes/ms under a [skysocks-lite] /
// [resolve-proxy] prefix in the "visor log" window. Returns the new state.
func jsProxyVerbose(_ js.Value, args []js.Value) interface{} {
	if len(args) > 0 {
		proxyVerbose = args[0].Truthy()
	}
	vlog(fmt.Sprintf("[skysocks-lite] verbose request logging %v", proxyVerbose))
	return proxyVerbose
}

// jsCloseWindow(winID) releases the skysocks-lite sessions of a browser window
// (called on window close) so its routes to the exit don't linger.
func jsCloseWindow(_ js.Value, args []js.Value) interface{} {
	if len(args) < 1 || args[0].String() == "" {
		return nil
	}
	winID := args[0].String()
	if n := closeSkysocksWindow(winID); n > 0 {
		emitProxyLog(winID, fmt.Sprintf("[skysocks-lite %s] stopped — released %d route/session(s)", winID, n))
	}
	return nil
}
