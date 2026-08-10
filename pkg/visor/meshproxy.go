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
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
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
		Transport: v.meshRoundTripper(), // dmsg/skynet dispatcher (streaming)
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
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if me, _ := r.Context().Value(meshErrKey).(string); me != "" {
				http.Error(w, "mesh proxy: "+me, http.StatusBadGateway)
				return
			}
			http.Error(w, "mesh proxy: "+err.Error(), http.StatusBadGateway)
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
	rp, err := v.newMeshReverseProxy(subdomainResolver(normalizeMeshSuffix(cfg.Suffix), aliases))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/", rp)
	return serveMeshHTTP(ctx, addr, mux, cfg.TLSCert, cfg.TLSKey)
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
	return serveMeshHTTP(ctx, addr, mux, cfg.TLSCert, cfg.TLSKey)
}
