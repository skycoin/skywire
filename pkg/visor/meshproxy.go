// Package visor pkg/visor/meshproxy.go c3-vis-core
// meshproxy — the RFC "real-origin" native browse path (see
// pkg/wasmhv/browseui/RFC-real-origin-browser.md). Instead of the WinBox
// browser fetching a page and re-implementing the network stack inside a
// sandboxed opaque srcdoc, this serves mesh (dmsg) sites from a REAL local
// origin so the BROWSER does native subresource loading, redirects, cookies,
// WASM, and streaming — skywire only proxies the transport (over dmsg), exactly
// like pkg/dmsgweb does for a real external browser via SOCKS5. Content flows
// through THIS visor's own transports; the listeners are loopback-only.
//
// Two origin shapes (BrowseOrigin.Mode), both giving each mesh site its OWN
// isolated browser origin:
//
//   - "subdomain" (default): ONE listener; each site is the origin
//     <vhost>.<base32pk><suffix> (suffix ".mesh.localhost" resolves to loopback
//     in every target browser, so no public cert is needed; set a real domain +
//     TLS for hosted/https parity). Routed by the Host header.
//   - "port": each site gets its OWN 127.0.0.1:<port> origin from a small pool.
//     No DNS/wildcard dependency (works anywhere), weaker isolation (cookies are
//     domain-scoped, not port-scoped). A "portal" on Addr resolves a mesh host
//     to its per-site port via GET /open?host=<resolver-host> → 302.
//
// Optional TLS (BrowseOrigin.TLSCert/TLSKey) serves either mode over HTTPS with
// a real or locally-trusted cert — for local https parity with the hosted
// <pk>.<domain> origin (same scheme, secure context, real-domain cookies).
//
// It is a SEPARATE listener from the hypervisor UI (:8000) on purpose: untrusted
// mesh content must never share an origin with the control surface, and these
// listeners serve ONLY the reverse proxy — no /api, no UI.
package visor

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxyinterstitial"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynetweb"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// defaultMeshProxySuffix marks a browse-frame origin for the native localhost
// case. `*.localhost` resolves to loopback in all target browsers and each label
// is a distinct, isolated origin — so no public cert is needed. Configurable via
// BrowseOrigin.Suffix (e.g. ".haltingstate.net" or a user's own domain).
//
// The suffix MUST include the leading dot: skynetweb.ParseResolverHost strips it
// verbatim, and without the dot "foo.<pk>.mesh.localhost" strips to "foo.<pk>."
// (trailing dot → empty final label → PK parse fails).
const defaultMeshProxySuffix = ".mesh.localhost"

// defaultMeshProxyAddr is the loopback listen address used when
// BrowseOrigin.Addr is unset. Loopback-only: untrusted mesh content must never
// share a listener reachable off-box with anything else.
const defaultMeshProxyAddr = "127.0.0.1:8461"

// meshProxyDefaultPort is the dmsg HTTP port dialed on the destination visor when
// the host carries no explicit port. Mesh HTTP services (incl. skynet port-80
// forwards) conventionally answer here. TODO(realorigin): allow an explicit dmsg
// port label so non-80 forwards are addressable.
const meshProxyDefaultPort = 80

// Port-mode loopback pool defaults (per-site 127.0.0.1:<port> origins).
const (
	defaultMeshPortBase = 8470
	defaultMeshPortSpan = 64
)

type meshCtxKey int

const (
	meshFrameHostKey meshCtxKey = iota
	meshVhostKey
	meshErrKey
	meshNetKey // selected transport: "dmsg" | "skynet" | "" (auto)
)

// meshResolveFn maps an inbound browse-frame Host to the destination visor PK,
// upstream vhost (for the outbound Host header) and the selected transport
// ("dmsg"/"skynet"/"" = auto). Subdomain mode parses the Host per request; port
// mode returns a fixed dest baked into the per-site listener.
type meshResolveFn func(inHost string) (dest cipher.PubKey, vhost, network string, err error)

// meshTransport dispatches each browse request to the dmsg or skynet round-
// tripper by the network stashed in the request context. "" (auto) tries skynet
// first (a visor mirrors :80 over skynet AND dmsg) and falls back to dmsg. skynet
// is nil when the router isn't available (e.g. before routing is up) → dmsg only.
type meshTransport struct {
	dmsg   http.RoundTripper
	skynet http.RoundTripper
}

func (t *meshTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	network, _ := req.Context().Value(meshNetKey).(string)
	switch network {
	case "dmsg":
		return t.dmsg.RoundTrip(req)
	case "skynet":
		if t.skynet == nil {
			return nil, fmt.Errorf("skynet transport unavailable (router not ready)")
		}
		return t.skynet.RoundTrip(req)
	default: // auto: skynet first, dmsg fallback
		if t.skynet != nil {
			if resp, err := t.skynet.RoundTrip(req); err == nil {
				return resp, nil
			}
			// rewind the body (if replayable) before the dmsg attempt
			if req.GetBody != nil {
				if b, e := req.GetBody(); e == nil {
					req.Body = b
				}
			}
		}
		return t.dmsg.RoundTrip(req)
	}
}

// skynetHTTPTransport is a streaming http.RoundTripper over skynet: it dials a
// skywire route to the destination's forwarding server, performs the skynet
// port-handshake, then speaks HTTP/1.1 over the route conn. The outbound URL host
// (set in Rewrite) is "<hexpk>:<port>", which DialContext parses to (PK, port).
func (v *Visor) skynetHTTPTransport() http.RoundTripper {
	return &http.Transport{
		DisableCompression: true,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			if v.router == nil {
				return nil, fmt.Errorf("skynet: router not ready")
			}
			host, portStr, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			var pk cipher.PubKey
			if err := pk.Set(host); err != nil {
				return nil, fmt.Errorf("skynet: bad dest %q: %w", host, err)
			}
			port, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				return nil, fmt.Errorf("skynet: bad port %q: %w", portStr, err)
			}
			conn, err := v.router.DialRoutes(ctx, pk, 0, routing.Port(skyenv.SkyForwardingServerPort), nil)
			if err != nil {
				return nil, fmt.Errorf("skynet dial: %w", err)
			}
			if err := skynetweb.PerformHandshake(conn, uint16(port)); err != nil {
				_ = conn.Close() //nolint:errcheck
				return nil, fmt.Errorf("skynet handshake: %w", err)
			}
			return conn, nil
		},
	}
}

// meshTransport builds the dmsg+skynet dispatching round-tripper. Caller must
// have verified v.dmsgHTTP.Transport is non-nil.
func (v *Visor) meshRoundTripper() *meshTransport {
	// The skynet transport is always constructed; its DialContext guards v.router
	// at dial time (nil until routing is up), so a router that comes up after this
	// proxy is built still works.
	return &meshTransport{dmsg: v.dmsgHTTP.Transport, skynet: v.skynetHTTPTransport()}
}

// meshInterstitialSoftTimeout bounds how long a COLD (not-yet-warm) top-level
// navigation waits for real content before we hand the browser the branded,
// auto-refreshing interstitial instead. Kept a touch above a typical warm dmsg
// stream dial but well under the ~13 s a cold multi-hop route can take, so a
// route that warms quickly still streams real content on the first load (no
// interstitial flash) while a genuinely cold one shows the page fast.
const meshInterstitialSoftTimeout = 4 * time.Second

// meshInterstitialRT wraps the raw dmsg/skynet dispatcher with a fast-path for
// cold-route warm-up. httputil.ReverseProxy only invokes ErrorHandler on a
// PROMPT error, but the failure mode we care about is the opposite: a cold-but-
// reachable route makes the round-trip BLOCK for many seconds and then succeed,
// so the browser just hangs on a blank frame while the route warms. This RT
// detects that case and serves the interstitial within meshInterstitialSoftTimeout
// while the real round-trip keeps running on a DETACHED context — establishing
// the dmsg session (pooled in dmsgC) / skynet route and recycling the warmed
// stream into the dmsghttp idle pool — so the browser's meta-refresh reload
// finds the route warm and streams real content.
//
// Why the warm-up converges rather than restarting every refresh:
//   - The background round-trip runs on context.WithoutCancel(req context): when
//     ServeHTTP returns after we write the interstitial and the inbound request is
//     canceled, the in-flight dmsg/route setup is NOT torn down. A naive per-request
//     deadline would cancel it and every refresh would start setup from scratch.
//   - Only ONE warm-up runs per destination at a time (inFlight dedup), so a burst
//     of refreshes to a still-cold host can't pile up detached dials/streams.
//   - Once any round-trip to a destination succeeds it is marked warm, and warm
//     destinations bypass the soft deadline entirely and block for the real
//     response. This bounds the interstitial to genuine route warm-up: a slow but
//     reachable ORIGIN (route already up, server just slow) is waited out, not
//     papered over with a refresh loop.
type meshInterstitialRT struct {
	next http.RoundTripper
	soft time.Duration

	mu       sync.Mutex
	warm     map[string]struct{} // destinations with a confirmed round-trip
	inFlight map[string]struct{} // destinations with a warm-up dial in progress
}

func newMeshInterstitialRT(next http.RoundTripper, soft time.Duration) *meshInterstitialRT {
	return &meshInterstitialRT{
		next:     next,
		soft:     soft,
		warm:     map[string]struct{}{},
		inFlight: map[string]struct{}{},
	}
}

// meshDestKey identifies the destination a request targets, for the warm/in-flight
// registries. The outbound URL host is "<hexpk>:<port>" (set in Rewrite); the net
// label keeps the dmsg and skynet variants of one PK distinct.
func meshDestKey(req *http.Request) string {
	network, _ := req.Context().Value(meshNetKey).(string)
	return network + "|" + req.URL.Host
}

// interstitialEligible reports whether a request may be answered with an HTML
// interstitial. Only a top-level document GET qualifies: injecting an HTML page
// in place of a subresource (image/script/style/XHR) or a non-idempotent method
// would corrupt the page or silently drop a body. Fetch Metadata
// (Sec-Fetch-Dest) is authoritative when present; otherwise we fall back to an
// HTML-accepting Accept header. This is what bounds a slow SUBRESOURCE from ever
// getting the interstitial — it always waits for (or errors on) real content.
func interstitialEligible(req *http.Request) bool {
	if req.Method != http.MethodGet {
		return false
	}
	if dest := req.Header.Get("Sec-Fetch-Dest"); dest != "" {
		return dest == "document"
	}
	return strings.Contains(strings.ToLower(req.Header.Get("Accept")), "text/html")
}

func (rt *meshInterstitialRT) isWarm(key string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	_, ok := rt.warm[key]
	return ok
}

func (rt *meshInterstitialRT) markWarm(key string) {
	rt.mu.Lock()
	rt.warm[key] = struct{}{}
	rt.mu.Unlock()
}

// coolDown forgets a warm destination after a warm-path round-trip failed, so the
// next navigation may show the interstitial again instead of blocking on a route
// that has since gone cold (e.g. the pooled dmsg session dropped).
func (rt *meshInterstitialRT) coolDown(key string) {
	rt.mu.Lock()
	delete(rt.warm, key)
	rt.mu.Unlock()
}

// acquireWarmup marks key as having a warm-up in progress and reports whether THIS
// caller won the right to run it. Losers serve the interstitial immediately without
// starting a second dial.
func (rt *meshInterstitialRT) acquireWarmup(key string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if _, busy := rt.inFlight[key]; busy {
		return false
	}
	rt.inFlight[key] = struct{}{}
	return true
}

func (rt *meshInterstitialRT) releaseWarmup(key string) {
	rt.mu.Lock()
	delete(rt.inFlight, key)
	rt.mu.Unlock()
}

type meshRTResult struct {
	resp *http.Response
	err  error
}

// RoundTrip implements http.RoundTripper.
func (rt *meshInterstitialRT) RoundTrip(req *http.Request) (*http.Response, error) {
	// A resolve failure stashed by Rewrite is a hard error (bad host / no suffix);
	// render the branded error page rather than dialing "invalid.mesh".
	if me, _ := req.Context().Value(meshErrKey).(string); me != "" {
		return rt.interstitialResponse(req, me, true), nil
	}

	key := meshDestKey(req)

	// Warm destination, or a request that can't carry an HTML page: block for the
	// real response (any delay here is a slow origin, not route warm-up).
	if !interstitialEligible(req) || rt.isWarm(key) {
		resp, err := rt.next.RoundTrip(req)
		switch {
		case err == nil:
			rt.markWarm(key)
		case rt.isWarm(key):
			rt.coolDown(key)
		}
		return resp, err
	}

	// Cold + eligible. Only one warm-up dial per destination: concurrent cold
	// requests (or rapid refreshes) get the interstitial without piling up dials.
	if !rt.acquireWarmup(key) {
		return rt.interstitialResponse(req, "", false), nil
	}

	// Run the real round-trip on a DETACHED context so it survives this request
	// being canceled once we write the interstitial — that is what lets the route
	// warm and PERSIST for the next (meta-refresh) request instead of restarting.
	detached := req.WithContext(context.WithoutCancel(req.Context()))
	ch := make(chan meshRTResult, 1)
	go func() {
		resp, err := rt.next.RoundTrip(detached)
		ch <- meshRTResult{resp: resp, err: err}
	}()

	timer := time.NewTimer(rt.soft)
	defer timer.Stop()
	select {
	case res := <-ch:
		rt.releaseWarmup(key)
		if res.err != nil {
			// Failed before the soft deadline: auto-refresh for a transient
			// warm-up signal, hard error otherwise.
			return rt.interstitialResponse(req, res.err.Error(), !proxyinterstitial.IsTransient(res.err)), nil
		}
		rt.markWarm(key)
		return res.resp, nil
	case <-timer.C:
		// Still warming. Keep the detached round-trip running in the background so
		// the dmsg session / skynet route establishes and the stream recycles into
		// the idle pool; then serve the interstitial now.
		go func() {
			res := <-ch
			rt.releaseWarmup(key)
			if res.err == nil {
				rt.markWarm(key)
				drainAndClose(res.resp)
			}
		}()
		return rt.interstitialResponse(req, "", false), nil
	}
}

// interstitialResponse builds a synthetic *http.Response carrying the branded
// interstitial for req, so ReverseProxy streams it to the browser exactly like an
// upstream response (ModifyResponse's rewriteMeshLocation only touches 3xx, so a
// 200/502 interstitial passes through untouched).
func (rt *meshInterstitialRT) interstitialResponse(req *http.Request, detail string, isError bool) *http.Response {
	target := meshInterstitialTarget(req)
	body := proxyinterstitial.Page(target, detail, meshInterstitialMechanism(target), isError)
	status := http.StatusOK
	if isError {
		status = http.StatusBadGateway
	}
	h := make(http.Header)
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store, must-revalidate")
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// meshInterstitialTarget is the human-facing host shown on the interstitial:
// prefer the upstream vhost, fall back to the inbound frame host.
// meshInterstitialMechanism picks the forwarding-surface label for the
// interstitial's fetch step from the target host: a ".skynet" host is served
// over skynet, a ".dmsg" host over dmsg. Empty (→ generic "skywire") when the
// target carries neither suffix.
func meshInterstitialMechanism(target string) string {
	switch {
	case strings.Contains(target, "skynet"):
		return "skynet"
	case strings.Contains(target, "dmsg"):
		return "dmsg"
	default:
		return ""
	}
}

func meshInterstitialTarget(req *http.Request) string {
	ctx := req.Context()
	if vhost, _ := ctx.Value(meshVhostKey).(string); vhost != "" {
		return vhost
	}
	frameHost, _ := ctx.Value(meshFrameHostKey).(string)
	return frameHost
}

// drainAndClose recycles a warmed-up response's dmsg stream back into the
// dmsghttp idle pool (pooledBody.Close returns a fully-drained stream to the
// pool), so the browser's next request reuses the warm stream.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body) //nolint:errcheck
	_ = resp.Body.Close()                 //nolint:errcheck
}

// peelNetLabel strips an optional "<net>" selector label ("dmsg"/"skynet") that
// sits immediately before the origin suffix, returning the remaining host-core
// and the selected network ("" = auto). core is the host with the suffix already
// removed, e.g. "magnetosphere.net.<pk>.skynet" → ("magnetosphere.net.<pk>", "skynet").
func peelNetLabel(core string) (rest, network string) {
	for _, n := range []string{"skynet", "dmsg"} {
		if strings.HasSuffix(core, "."+n) {
			return strings.TrimSuffix(core, "."+n), n
		}
	}
	return core, ""
}

// newMeshReverseProxy builds the httputil.ReverseProxy that forwards a browse-
// frame request over dmsg and streams the response back verbatim, using resolve
// to pick the destination.
func (v *Visor) newMeshReverseProxy(resolve meshResolveFn) (*httputil.ReverseProxy, error) {
	if v.dmsgHTTP == nil || v.dmsgHTTP.Transport == nil {
		return nil, fmt.Errorf("dmsg HTTP client not ready")
	}
	return &httputil.ReverseProxy{
		// dmsg/skynet dispatcher (streaming), wrapped with the cold-route fast-path
		// that serves the branded auto-refreshing interstitial while a route warms.
		Transport: newMeshInterstitialRT(v.meshRoundTripper(), meshInterstitialSoftTimeout),
		// Rewrite (not Director): does NOT auto-add X-Forwarded-* (privacy) and lets
		// us stash per-request state in the outbound context instead of leaking
		// X-Mesh-* headers to the upstream site.
		Rewrite: func(pr *httputil.ProxyRequest) {
			dest, vhost, network, err := resolve(hostWithoutPort(pr.In.Host))
			ctx := pr.In.Context()
			if err != nil {
				pr.Out.URL.Scheme = "http"
				pr.Out.URL.Host = "invalid.mesh"
				pr.Out = pr.Out.WithContext(context.WithValue(ctx, meshErrKey, err.Error()))
				return
			}
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = fmt.Sprintf("%s:%d", dest.Hex(), meshProxyDefaultPort)
			// Upstream sees the real vhost (name-based backends like caddy need it).
			if vhost != "" {
				pr.Out.Host = vhost
			} else {
				pr.Out.Host = dest.Hex()
			}
			ctx = context.WithValue(ctx, meshFrameHostKey, pr.In.Host)
			ctx = context.WithValue(ctx, meshVhostKey, vhost)
			ctx = context.WithValue(ctx, meshNetKey, network)
			pr.Out = pr.Out.WithContext(ctx)
		},
		ModifyResponse: func(resp *http.Response) error {
			ctx := resp.Request.Context()
			frameHost, _ := ctx.Value(meshFrameHostKey).(string)
			vhost, _ := ctx.Value(meshVhostKey).(string)
			rewriteMeshLocation(resp, frameHost, vhost)
			return nil // headers pass through verbatim (the whole point vs. the old bridge)
		},
		// ErrorHandler only fires for the residual error paths the interstitial RT
		// forwards verbatim (warm-but-now-failing, or a non-navigation request that
		// errored). Render the branded page for navigations; a subresource that
		// errored gets a plain status so we never inject HTML into it.
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			detail := err.Error()
			isError := true
			if me, _ := r.Context().Value(meshErrKey).(string); me != "" {
				detail = me
			} else {
				isError = !proxyinterstitial.IsTransient(err)
			}
			if !interstitialEligible(r) {
				http.Error(w, "mesh proxy: "+detail, http.StatusBadGateway)
				return
			}
			status := http.StatusOK
			if isError {
				status = http.StatusBadGateway
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store, must-revalidate")
			w.WriteHeader(status)
			// proxyinterstitial.Page HTML-escapes target and detail, so the rendered
			// document carries no unescaped request-derived content (no XSS).
			mt := meshInterstitialTarget(r)
			_, _ = io.WriteString(w, proxyinterstitial.Page(mt, detail, meshInterstitialMechanism(mt), isError)) //nolint:errcheck,gosec // G705: Page escapes all interpolated values
		},
	}, nil
}

// subdomainResolver parses <vhost>.<base32pk>[.<net>]<suffix> per request, where
// <net> is an optional "dmsg"/"skynet" transport selector (default auto).
// aliases maps a bare name label (e.g. "home") to a PK, mirroring the dmsgweb
// resolver.
func subdomainResolver(suffix string, aliases map[string]cipher.PubKey) meshResolveFn {
	return func(inHost string) (cipher.PubKey, string, string, error) {
		if !strings.HasSuffix(inHost, suffix) {
			return cipher.PubKey{}, "", "", fmt.Errorf("host %q has no suffix %q", inHost, suffix)
		}
		core, network := peelNetLabel(strings.TrimSuffix(inHost, suffix))
		vhost, _, dest, _, err := skynetweb.ParseResolverHost(core+suffix, suffix, aliases)
		return dest, vhost, network, err
	}
}

// rewriteMeshLocation rewrites a redirect Location whose host is the upstream
// vhost (or is relative) into a frame-origin-relative URL, so the browser stays
// within this reverse-proxy origin instead of leaving to clearnet. A Location to
// a genuinely different host is left as-is.
func rewriteMeshLocation(resp *http.Response, frameHost, vhost string) {
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return
	}
	loc := resp.Header.Get("Location")
	if loc == "" || frameHost == "" {
		return
	}
	u, err := url.Parse(loc)
	if err != nil {
		return
	}
	if u.Host == "" || strings.EqualFold(u.Host, vhost) {
		np := u.Path
		if np == "" {
			np = "/"
		}
		if u.RawQuery != "" {
			np += "?" + u.RawQuery
		}
		resp.Header.Set("Location", np) // relative → browser resolves against the frame origin
	}
}

func hostWithoutPort(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

// normalizeMeshSuffix returns the suffix with a guaranteed leading dot, defaulting
// to .mesh.localhost when empty.
func normalizeMeshSuffix(s string) string {
	if s == "" {
		return defaultMeshProxySuffix
	}
	if !strings.HasPrefix(s, ".") {
		return "." + s
	}
	return s
}

// serveMeshHTTP listens on addr and serves h until ctx is done, over HTTPS when
// both certFile and keyFile are set, otherwise plain HTTP.
func serveMeshHTTP(ctx context.Context, addr string, h http.Handler, certFile, keyFile string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("mesh proxy listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: h} //nolint:gosec // local loopback reverse proxy
	go func() {
		<-ctx.Done()
		_ = srv.Close() //nolint:errcheck
	}()
	if certFile != "" && keyFile != "" {
		err = srv.ServeTLS(lis, certFile, keyFile)
	} else {
		err = srv.Serve(lis)
	}
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// initMeshProxy is the vinit module entry: it starts the native real-origin
// browse proxy when v.conf.BrowseOrigin is enabled. Modeled on
// initDmsgForwardProxy — nil-guard the optional config block and return nil when
// disabled so init proceeds. Unlike forward-proxy, ServeMeshProxy blocks until
// ctx is done, so it runs in its own goroutine. Registered depending on the dmsg
// module (which assigns v.dmsgHTTP) so the round-tripper is ready before this runs.
func initMeshProxy(ctx context.Context, v *Visor, log *logging.Logger) error {
	bo := v.conf.BrowseOrigin
	if bo == nil || !bo.Enable {
		return nil
	}
	mode := bo.Mode
	if mode == "" {
		mode = "subdomain"
	}
	log.WithField("mode", mode).WithField("addr", bo.Addr).
		WithField("suffix", bo.Suffix).WithField("tls", bo.TLSCert != "" && bo.TLSKey != "").
		Info("starting real-origin browse proxy")
	go func() {
		if err := v.ServeMeshProxy(ctx, bo, v.browseAliases()); err != nil {
			log.WithError(err).Warn("real-origin browse proxy stopped")
		}
	}()
	return nil
}

// ServeMeshProxy runs the browse-origin listener(s) until ctx is done, dispatched
// by cfg.Mode ("subdomain" default, or "port"). aliases maps friendly labels to
// PKs (same set the dmsgweb resolver uses).
func (v *Visor) ServeMeshProxy(ctx context.Context, cfg *visorconfig.BrowseOriginConfig, aliases map[string]cipher.PubKey) error {
	addr := cfg.Addr
	if addr == "" {
		addr = defaultMeshProxyAddr
	}
	switch strings.ToLower(cfg.Mode) {
	case "port":
		return v.serveMeshPortMode(ctx, addr, cfg)
	default: // "subdomain" or ""
		return v.serveMeshSubdomain(ctx, addr, cfg, aliases)
	}
}

// serveMeshSubdomain runs the single Host-routed reverse-proxy origin. The browse
// iframe targets <scheme>://<vhost>.<base32pk><suffix>[:port]/ against addr.
func (v *Visor) serveMeshSubdomain(ctx context.Context, addr string, cfg *visorconfig.BrowseOriginConfig, aliases map[string]cipher.PubKey) error {
	suffix := normalizeMeshSuffix(cfg.Suffix)
	rp, err := v.newMeshReverseProxy(subdomainResolver(suffix, aliases))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	// Reserved proxy-status hosts (status-<surface><suffix>) served over THIS
	// listener so the browse-origin's real (wildcard) TLS cert covers them —
	// warning-free HTTPS status pages. Any non-status host falls through to the
	// reverse proxy. See meshStatusHandler.
	mux.Handle("/", meshStatusHandler(suffix, v.proxyStatusProvider(), rp))
	return serveMeshHTTP(ctx, addr, mux, cfg.TLSCert, cfg.TLSKey)
}

// meshStatusHandler serves the reserved proxy-status pages over the browse-origin
// listener. They are reached at status-<surface>.<suffix> (e.g.
// status-skysocks.haltingstate.net) — a SINGLE label under the suffix, so the
// deployment's real single-level wildcard cert (*.<suffix>, terminated here or by
// a fronting Caddy) covers them and the page loads over https:// with no browser
// warning. This is the real-cert alternative to the self-signed skynetca leaf
// path (pkg/skynetweb): no CA install needed. Any non-status host falls through
// to next (the reverse proxy). A nil provider disables status here entirely.
func meshStatusHandler(suffix string, status proxystatus.Provider, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != nil {
			if surface, ok := meshStatusSurface(hostWithoutPort(r.Host), suffix); ok {
				snap, serr := status.StatusSnapshot(surface)
				if serr != nil {
					snap = proxystatus.Snapshot{Surface: surface, Note: "status unavailable: " + serr.Error()}
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				_, _ = w.Write(proxystatus.Render(snap)) //nolint:errcheck
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// meshStatusSurface reports whether host is a browse-origin status host
// (status-<surface><suffix>) and which surface it names. The single
// "status-<surface>" label keeps it under a single-level wildcard cert. Matching
// is case-insensitive and exact — a multi-label host (a real browse frame) never
// matches, so status never shadows a <vhost>.<pk><suffix> lookup.
func meshStatusSurface(host, suffix string) (proxystatus.Surface, bool) {
	h := strings.ToLower(strings.TrimSpace(host))
	sfx := strings.ToLower(suffix)
	if !strings.HasSuffix(h, sfx) {
		return "", false
	}
	label := strings.TrimSuffix(h, sfx)
	if !strings.HasPrefix(label, "status-") {
		return "", false
	}
	rest := strings.TrimPrefix(label, "status-")
	if strings.Contains(rest, ".") {
		return "", false
	}
	switch proxystatus.Surface(rest) {
	case proxystatus.SurfaceDmsg, proxystatus.SurfaceSkynet, proxystatus.SurfaceSkysocks:
		return proxystatus.Surface(rest), true
	default:
		return "", false
	}
}

// --- port mode ---------------------------------------------------------------

type meshPortEntry struct {
	key  string
	url  string
	srv  *http.Server
	port int
}

// meshPortManager hands each mesh site its own 127.0.0.1:<port> reverse-proxy
// origin from a bounded pool, with LRU eviction when the pool is full.
type meshPortManager struct {
	v         *Visor
	parentCtx context.Context
	cert, key string
	scheme    string
	base      int
	span      int

	mu     sync.Mutex
	byKey  map[string]*meshPortEntry
	byPort map[int]*meshPortEntry
	order  []string // LRU: front = least-recently-used
}

// originFor resolves a resolving-proxy host (<pk>.dmsg / alias.dmsg / bare pk /
// <name>.<pk>.dmsg) to its dedicated loopback origin URL, creating the per-site
// listener on first use and reusing it thereafter.
func (m *meshPortManager) originFor(host string) (string, error) {
	pk, _, vhost, err := m.v.resolveBrowseHost(host, 0)
	if err != nil {
		return "", err
	}
	network := ""
	switch lh := strings.ToLower(host); {
	case strings.Contains(lh, ".skynet"):
		network = "skynet"
	case strings.Contains(lh, ".dmsg"):
		network = "dmsg"
	}
	key := pk.Hex() + "|" + vhost + "|" + network
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.byKey[key]; ok {
		m.touchLocked(key)
		return e.url, nil
	}
	port, lis, err := m.listenFreeLocked()
	if err != nil {
		if len(m.order) == 0 {
			return "", err
		}
		m.evictLocked(m.order[0]) // pool full → drop LRU and retry once
		port, lis, err = m.listenFreeLocked()
		if err != nil {
			return "", err
		}
	}
	dest := pk
	rp, rerr := m.v.newMeshReverseProxy(func(string) (cipher.PubKey, string, string, error) { return dest, vhost, network, nil })
	if rerr != nil {
		_ = lis.Close() //nolint:errcheck
		return "", rerr
	}
	srv := &http.Server{Handler: rp} //nolint:gosec // local loopback reverse proxy
	e := &meshPortEntry{key: key, url: fmt.Sprintf("%s://127.0.0.1:%d/", m.scheme, port), srv: srv, port: port}
	m.byKey[key] = e
	m.byPort[port] = e
	m.order = append(m.order, key)
	go func() {
		var serr error
		if m.cert != "" && m.key != "" {
			serr = srv.ServeTLS(lis, m.cert, m.key)
		} else {
			serr = srv.Serve(lis)
		}
		_ = serr // per-site listener closed on evict or ctx cancel
	}()
	go func() {
		<-m.parentCtx.Done()
		_ = srv.Close() //nolint:errcheck
	}()
	return e.url, nil
}

// listenFreeLocked binds the first free port in [base, base+span). Caller holds m.mu.
func (m *meshPortManager) listenFreeLocked() (int, net.Listener, error) {
	for p := m.base; p < m.base+m.span; p++ {
		if _, used := m.byPort[p]; used {
			continue
		}
		lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			continue // port taken by something else; try the next
		}
		return p, lis, nil
	}
	return 0, nil, fmt.Errorf("mesh port pool exhausted (%d..%d)", m.base, m.base+m.span-1)
}

// evictLocked closes and forgets one entry. Caller holds m.mu.
func (m *meshPortManager) evictLocked(key string) {
	e, ok := m.byKey[key]
	if !ok {
		return
	}
	_ = e.srv.Close() //nolint:errcheck
	delete(m.byKey, key)
	delete(m.byPort, e.port)
	for i, k := range m.order {
		if k == key {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
}

// touchLocked marks key most-recently-used. Caller holds m.mu.
func (m *meshPortManager) touchLocked(key string) {
	for i, k := range m.order {
		if k == key {
			m.order = append(append(m.order[:i:i], m.order[i+1:]...), key)
			return
		}
	}
}

// serveMeshPortMode runs the port-mode "portal" on addr: GET /open?host=<host>
// resolves the mesh host to a dedicated 127.0.0.1:<port> origin and 302-redirects
// there (so an iframe pointed at .../open?host=… lands on the isolated origin).
func (v *Visor) serveMeshPortMode(ctx context.Context, addr string, cfg *visorconfig.BrowseOriginConfig) error {
	if v.dmsgHTTP == nil || v.dmsgHTTP.Transport == nil {
		return fmt.Errorf("dmsg HTTP client not ready")
	}
	base := cfg.PortBase
	if base == 0 {
		base = defaultMeshPortBase
	}
	span := cfg.PortSpan
	if span <= 0 {
		span = defaultMeshPortSpan
	}
	scheme := "http"
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		scheme = "https"
	}
	mgr := &meshPortManager{
		v: v, parentCtx: ctx, cert: cfg.TLSCert, key: cfg.TLSKey, scheme: scheme,
		base: base, span: span,
		byKey: map[string]*meshPortEntry{}, byPort: map[int]*meshPortEntry{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/open", func(w http.ResponseWriter, r *http.Request) {
		h := r.URL.Query().Get("host")
		if h == "" {
			http.Error(w, "missing ?host=", http.StatusBadRequest)
			return
		}
		u, err := mgr.originFor(h)
		if err != nil {
			http.Error(w, "mesh proxy: "+err.Error(), http.StatusBadGateway)
			return
		}
		// u is not user-controlled: originFor resolves h to a PK and returns this
		// manager's own scheme://127.0.0.1:<port> origin from the local pool.
		http.Redirect(w, r, u, http.StatusFound) //nolint:gosec // G710: redirect target is a local loopback origin we minted, not tainted input
	})
	// Reserved proxy-status hosts on the portal listener too (served under the
	// same suffix + real cert). "/open" is the more specific pattern, so this
	// root handler only sees other paths; a status host renders, everything else
	// 404s (the portal has no content of its own).
	mux.Handle("/", meshStatusHandler(normalizeMeshSuffix(cfg.Suffix), v.proxyStatusProvider(), http.NotFoundHandler()))
	return serveMeshHTTP(ctx, addr, mux, cfg.TLSCert, cfg.TLSKey)
}
