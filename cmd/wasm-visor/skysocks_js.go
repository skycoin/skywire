//go:build js && wasm

// Package main cmd/wasm-visor/skysocks_js.go c3-vis-wasm
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

	"github.com/skycoin/skywire/third_party/hashicorp/yamux"
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
// page registered a per-window sink (browse.js's ⚙ proxy-log pane, or the mesh
// browser's interstitial, set globalThis.__skywireProxyLog), forwards it to that
// browser window by id — so the window shows its own connect / route-setup step
// explicitly, the way `skywire cli proxy start --verbose` surfaces session
// establishment.
func emitProxyLog(winID, msg string) {
	vlog(msg)
	emitProxyLogHook(winID, msg)
}

// emitProxyLogHook forwards a line to the JS sink ONLY (no visor-log line). Used
// for the high-frequency per-request progress trace (acquiring exit / attempt /
// sending / status) that makes the interstitial as detailed as `--verbose` but
// would flood the visor-log window if echoed there for every subresource. The
// live interstitial subscribes to the sink for the duration of a navigation, so
// it still sees every line; after the page loads nothing is subscribed and these
// are dropped.
func emitProxyLogHook(winID, msg string) {
	if h := js.Global().Get("__skywireProxyLog"); h.Type() == js.TypeFunction {
		h.Invoke(winID, msg)
	}
}

// skysocksSession returns a yamux session to the skysocks-server at serverPK,
// lazily establishing it over a fresh route group (rtr.DialRoutes — the routing
// layer). Cached per exit; a closed session is re-dialed.
func skysocksSession(winID string, serverPK cipher.PubKey) (*yamux.Session, error) {
	// Key by the SESSION OWNER, not the raw window: all user windows on the same
	// instance share one route to a given exit (a 2nd browser window reuses the
	// 1st's established session instead of cold-dialing its own). Internal pool
	// windows keep their own owner (see sessionOwner).
	key := skysocksKey(sessionOwner(winID), serverPK)
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
	// 28s cap: a reachable exit's route (p2p multihop or a freshly-created direct
	// transport) sets up in a few seconds; a dead/unroutable one should FAIL so the
	// caller can try another, not hang. Runs off skysocksMu so exits establish in
	// parallel. Was 18s, but that left no margin above the router's route reserve +
	// the (now 10s) noise-handshake window, so a slow-but-alive loaded exit that the
	// native client reaches was cut off here; 28s covers reserve + handshake + a
	// retry with headroom while still failing a truly dead exit promptly enough.
	dctx, cancel := context.WithTimeout(ctx, 28*time.Second)
	defer cancel()
	// Default dial: the router races the route-finder (a multihop route over
	// EXISTING peer-to-peer transports) against the visor's route-setup hook, which
	// creates the best DIRECT transport to the exit — for a browser edge that's
	// WEBRTC (p2p), falling back to the dmsg relay only if nothing direct works
	// (see EnsureBestTransport, wired as the wasm SetupHook in main.go). Whichever
	// lands first wins; both keep the data plane peer-to-peer. This is exactly what
	// native `skysocks-client` does by default — no --direct, no forced dmsg.
	//
	// Single route (MuxRoutes 0, DefaultDialOptions): matching the native visor's
	// working BrowseClearnet path. A 2-route mux reports "2/2 established" yet the
	// yamux session over it immediately reads EOF ("session shutdown") — the mux
	// data plane never carries the skysocks bytes. mux-auto GROW can still adapt an
	// established single route later.
	conn, err := rtr.DialRoutes(dctx, serverPK, 0, skysocksPort, router.DefaultDialOptions())
	// "already being initialized" is NOT an exit failure: another dial to this
	// exit (a probe, the pre-warm, or a sibling window) is mid-handshake on the
	// SAME router route-group descriptor, so this concurrent dial is rejected
	// until that one lands (≤ the 18s dial cap). Treat it as transient — poll our
	// session cache for the landing session and re-dial a few times — instead of
	// failing the fetch and (via reportProxyExitDead) marking a perfectly good
	// exit dead, which caused rotation churn and the "all exits failed" wedge.
	for attempt := 0; err != nil && strings.Contains(err.Error(), "already being initialized") && attempt < 4; attempt++ {
		skysocksMu.Lock()
		if s, ok := skysocksSessions[key]; ok && !s.IsClosed() {
			skysocksMu.Unlock()
			emitProxyLog(winID, fmt.Sprintf("[skysocks-lite %s] reusing concurrently-established session to exit %s", winID, serverPK.Hex()[:8]))
			return s, nil
		}
		skysocksMu.Unlock()
		select {
		case <-time.After(600 * time.Millisecond):
		case <-dctx.Done():
			return nil, dctx.Err()
		}
		conn, err = rtr.DialRoutes(dctx, serverPK, 0, skysocksPort, router.DefaultDialOptions())
	}
	if err != nil {
		emitProxyLog(winID, fmt.Sprintf("[skysocks-lite %s] route dial to exit %s FAILED (%dms): %v", winID, serverPK.Hex()[:8], time.Since(t0).Milliseconds(), err))
		// If this was the default instance's auto-selected active exit, hand it to
		// the retry policy (sticky-same-key when it had connected, else rotate).
		// Skip the visor's OWN internal windows (probe / sticky / keepalive / warm)
		// — each manages its own failure so they don't re-enter the policy.
		if !isInternalProxyWindow(winID) {
			reportProxyExitDead(serverPK)
		}
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
	// Record that a REAL route to this exit came up (probe windows excluded), so a
	// later drop is retried with the same key rather than rotated to a new random.
	if !isProbeProxyWindow(winID) {
		markProxyExitConnected(serverPK)
	}
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
			// A route/session can establish to an exit whose clearnet EGRESS is
			// dead — the SOCKS5 CONNECT succeeds but no HTTP response ever comes.
			// Cap the wait for response headers so that case fails fast and the
			// caller (jsFetchClearnet) can fail over to another exit, instead of
			// hanging on the full Timeout. Body transfer still gets the 45s below.
			ResponseHeaderTimeout: 12 * time.Second,
		},
		// Total request budget. ResponseHeaderTimeout (above) already fails a
		// dead-egress exit fast, so this only bounds BODY transfer — kept generous
		// so large pages (a multi-MB wasm app can be ~10MB) finish over a mesh
		// route at modest throughput rather than being cut off mid-download.
		Timeout: 120 * time.Second,
	}, nil
}

// cachedUA memoizes the browser User-Agent (it never changes within a tab).
var cachedUA string

// browserUserAgent returns a User-Agent to present to clearnet origins. The wasm
// visor runs INSIDE a real browser, so we present that browser's own navigator
// .userAgent — making a proxied fetch indistinguishable from a normal tab, so
// origins that gate on (or reject a missing / bot-looking) User-Agent serve the
// real page instead of a 403/robot wall. Falls back to a current desktop UA if
// navigator is unavailable (e.g. a headless WASI host).
func browserUserAgent() string {
	if cachedUA != "" {
		return cachedUA
	}
	if nav := js.Global().Get("navigator"); nav.Truthy() {
		if ua := nav.Get("userAgent"); ua.Type() == js.TypeString && ua.String() != "" {
			cachedUA = ua.String()
			return cachedUA
		}
	}
	cachedUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	return cachedUA
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
	if len(args) > 3 {
		body = jsBodyBytes(args[3])
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
		if _, err := url.ParseRequestURI(rawURL); err != nil {
			return nil, fmt.Errorf("bad url: %w", err)
		}
		// A caller-pinned exit (non-empty serverPKHex) is respected as-is — one
		// attempt, no failover. The iframe browser / wallet leave it empty to use
		// the default AUTO instance, which we fail over across exits: if a fetch
		// fails (dead egress, dropped route, …) we mark that exit dead, promote a
		// standby (rotateAwayFromExit), and retry — so a stuck exit self-heals
		// instead of leaving the page hung. Bounded so a run of bad exits ends.
		pinned := serverPKHex != ""
		var pinnedPK cipher.PubKey
		if pinned {
			if err := pinnedPK.UnmarshalText([]byte(serverPKHex)); err != nil {
				return nil, fmt.Errorf("bad skysocks server pk: %w", err)
			}
		}
		maxTries := 1
		if !pinned {
			maxTries = 4
		}
		tried := map[cipher.PubKey]bool{}
		var lastErr error
		// Total budget to acquire a working exit, spanning re-selection. A native
		// skysocks-client browser just keeps waiting for a connection, so a clearnet
		// fetch here must too: hard-failing during the pool-empty recovery window
		// (re-select can take 30-50s when a probed candidate is slow) is the gap
		// that made the iframe browser less reliable than the native chain.
		acquireDeadline := time.Now().Add(55 * time.Second)
		if pinned {
			emitProxyLogHook(winID, fmt.Sprintf("[skysocks-lite %s] %s %s — using pinned exit %s", winID, method, rawURL, pinnedPK.Hex()[:8]))
		} else {
			emitProxyLogHook(winID, fmt.Sprintf("[skysocks-lite %s] %s %s — acquiring a working exit…", winID, method, rawURL))
		}
		for attempt := 0; attempt < maxTries; attempt++ {
			var spk cipher.PubKey
			if pinned {
				spk = pinnedPK
			} else {
				// Wait for a fresh (not-yet-tried) auto exit. After a rotate/clear the
				// pool can be momentarily empty while it re-selects; kick the selector
				// and keep waiting (bounded by acquireDeadline) rather than giving up.
				pk := waitFreshProxyExit(tried, 16*time.Second)
				for pk.Null() && time.Now().Before(acquireDeadline) {
					emitProxyLogHook(winID, fmt.Sprintf("[skysocks-lite %s] no vetted exit yet — asking the selector for a fresh one…", winID))
					kickDefaultProxyReselect()
					pk = waitFreshProxyExit(tried, 12*time.Second)
				}
				if pk.Null() {
					lastErr = errors.New("no working proxy exit available")
					break
				}
				spk = pk
			}
			tried[spk] = true
			emitProxyLogHook(winID, fmt.Sprintf("[skysocks-lite %s] attempt %d/%d — routing via exit %s", winID, attempt+1, maxTries, spk.Hex()[:8]))
			sess, err := skysocksSession(winID, spk)
			if err != nil {
				// skysocksSession already reported the exit dead on a dial failure;
				// promote a standby so the next attempt uses a different exit.
				lastErr = err
				if !pinned {
					rotateAwayFromExit(spk)
				}
				continue
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
			// Present as a normal browser so origins that gate on (or reject a missing
			// / bot-looking) User-Agent serve the real page instead of a 403/robot
			// wall. Set BEFORE extraHeaders so a caller can still override. Accept-
			// Encoding is intentionally left unset — the http.Transport adds gzip and
			// transparently decompresses only when it owns that header.
			req.Header.Set("User-Agent", browserUserAgent())
			req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
			req.Header.Set("Accept-Language", "en-US,en;q=0.9")
			for k, v := range extraHeaders {
				req.Header.Set(k, v)
			}
			emitProxyLogHook(winID, fmt.Sprintf("[skysocks-lite %s] route ready — sending %s request to %s over exit %s…", winID, method, rawURL, spk.Hex()[:8]))
			t0 := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				lastErr = err
				emitProxyLog(winID, fmt.Sprintf("[skysocks-lite %s] %s %s via %s FAILED (%dms): %v", winID, method, rawURL, spk.Hex()[:8], time.Since(t0).Milliseconds(), err))
				// The route was fine but the fetch failed (dead egress / timeout).
				// Rotate straight to a standby (rotateAwayFromExit removes this exit,
				// promotes the standby, and refills the pool excluding it) — NOT
				// reportProxyExitDead, which would sticky-reconnect the same
				// egress-dead exit we're trying to get away from.
				if !pinned {
					rotateAwayFromExit(spk)
				}
				continue
			}
			defer resp.Body.Close()                               //nolint:errcheck
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20)) // 16MB cap
			// Always forward the result line to the live interstitial (so it shows the
			// final status/bytes/ms); echo to the visor-log window only when verbose,
			// so routine subresource fetches don't flood it.
			resultLine := fmt.Sprintf("[skysocks-lite %s] %s %s via %s → %d (%dB, %dms)", winID, method, rawURL, spk.Hex()[:8], resp.StatusCode, len(b), time.Since(t0).Milliseconds())
			if proxyVerbose {
				vlog(resultLine)
			}
			emitProxyLogHook(winID, resultLine)
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
		}
		if lastErr == nil {
			lastErr = errors.New("no exit")
		}
		return nil, fmt.Errorf("fetch via skysocks (all exits failed): %w", lastErr)
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
