package visor

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// DmsgForwardProxyPort is the default dmsg port the clearnet forward-proxy
// listens on when DmsgWeb.ForwardPort is unset.
const DmsgForwardProxyPort uint16 = 84

// forwardProxyTimeout bounds a single clearnet fetch end-to-end.
const forwardProxyTimeout = 60 * time.Second

// hopByHopHeaders are stripped from both the forwarded request and the returned
// response — they describe a single transport hop, not the end-to-end message.
var hopByHopHeaders = map[string]struct{}{
	"connection": {}, "keep-alive": {}, "proxy-authenticate": {}, "proxy-authorization": {},
	"te": {}, "trailer": {}, "transfer-encoding": {}, "upgrade": {},
}

// initDmsgForwardProxy serves a CLEARNET HTTP forward-proxy over dmsg when
// DmsgWeb.ForwardProxy is set. A whitelisted peer (e.g. a browser wasm-visor,
// which can't itself do TLS) sends an absolute-URL HTTP request over dmsg; this
// visor fetches it from the clearnet and returns the response. Default off, and
// gated by the same whitelist as the visor's other privileged dmsg surfaces —
// it is NOT an open relay. Egress prefers UpstreamSOCKS (so the exit IP is the
// skysocks node, not this visor); only when UpstreamSOCKS is empty does this
// visor connect directly (its own IP becomes the exit).
func initDmsgForwardProxy(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.conf.DmsgWeb == nil || !v.conf.DmsgWeb.ForwardProxy {
		return nil
	}
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return fmt.Errorf("cannot start dmsg forward-proxy: dmsg not configured")
	}
	port := v.conf.DmsgWeb.ForwardPort
	if port == 0 {
		port = DmsgForwardProxyPort
	}

	// The same authoritative whitelist initDmsgHTTPLogServer builds for the
	// visor's privileged surfaces: own PK + survey whitelist + hypervisors + pty.
	allowed := map[cipher.PubKey]struct{}{v.conf.PK: {}}
	for _, pk := range v.conf.EffectiveSurveyWhitelist() {
		allowed[pk] = struct{}{}
	}
	for _, pk := range v.conf.Hypervisors {
		allowed[pk] = struct{}{}
	}
	if v.conf.Pty != nil {
		for _, pk := range v.conf.Pty.Whitelist {
			allowed[pk] = struct{}{}
		}
	}

	client, err := newForwardProxyClient(v.conf.DmsgWeb.UpstreamSOCKS)
	if err != nil {
		return fmt.Errorf("dmsg forward-proxy: build egress client: %w", err)
	}
	direct := v.conf.DmsgWeb.UpstreamSOCKS == ""

	lis, err := dmsgC.Listen(port)
	if err != nil {
		return fmt.Errorf("dmsg forward-proxy: listen dmsg:%d: %w", port, err)
	}
	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck
	}()

	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           &forwardProxyHandler{client: client, direct: direct, log: log},
	}
	go func() {
		// pkGatedListener drops non-whitelisted peers before they reach the
		// HTTP server, so an unauthorized stream never gets to issue a fetch.
		_ = srv.Serve(&pkGatedListener{Listener: lis, allowed: allowed, log: log}) //nolint:errcheck
	}()

	mode := "egress via upstream " + v.conf.DmsgWeb.UpstreamSOCKS
	if direct {
		mode = "egress DIRECT (this visor's IP is the exit)"
	}
	log.WithField("dmsg_port", port).WithField("whitelisted", len(allowed)).
		Infof("Serving clearnet HTTP forward-proxy over dmsg — %s", mode)
	return nil
}

// pkGatedListener wraps a dmsg listener and drops any inbound stream whose
// remote public key is not in the allowed set, before it is handed to Serve.
type pkGatedListener struct {
	net.Listener
	allowed map[cipher.PubKey]struct{}
	log     *logging.Logger
}

func (l *pkGatedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.allows(conn) {
			return conn, nil
		}
		if l.log != nil {
			l.log.WithField("remote", conn.RemoteAddr().String()).
				Warn("dmsg forward-proxy: rejected non-whitelisted peer")
		}
		_ = conn.Close() //nolint:errcheck
	}
}

func (l *pkGatedListener) allows(conn net.Conn) bool {
	da, ok := conn.RemoteAddr().(dmsg.Addr)
	if !ok {
		return false // not a dmsg stream → cannot authenticate the caller
	}
	_, ok = l.allowed[da.PK]
	return ok
}

// forwardProxyHandler serves one forward-proxy request: an absolute-URL HTTP
// request (the standard proxy request form, which FetchOverDmsg produces when
// the caller passes the full URL as the path) is fetched from the clearnet and
// the response copied back.
type forwardProxyHandler struct {
	client *http.Client
	direct bool
	log    *logging.Logger
}

func (h *forwardProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		http.Error(w, "CONNECT tunneling not supported", http.StatusMethodNotAllowed)
		return
	}
	if !r.URL.IsAbs() || (r.URL.Scheme != "http" && r.URL.Scheme != "https") {
		http.Error(w, "forward-proxy expects an absolute http(s) URL", http.StatusBadRequest)
		return
	}
	// On DIRECT egress this visor's IP is the exit, so refuse targets that
	// resolve to loopback/private/link-local addresses — a whitelisted-but-
	// curious caller must not be able to probe the proxy operator's LAN or
	// cloud metadata endpoint. With an upstream SOCKS5 the upstream is the
	// boundary, so the check is skipped.
	if h.direct && !hostIsPublic(r.URL.Hostname()) {
		http.Error(w, "target host is not a public address", http.StatusForbidden)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), forwardProxyTimeout)
	defer cancel()
	// G704: a forward proxy fetches caller-specified URLs by design; direct-egress
	// is gated above by hostIsPublic, and non-direct egress is confined to the
	// skywire overlay. The "taint" is the proxy's whole purpose.
	out, err := http.NewRequestWithContext(ctx, r.Method, r.URL.String(), r.Body) //nolint:gosec
	if err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	copyHeaders(out.Header, r.Header)
	out.Header.Del("Host")

	resp, err := h.client.Do(out) //nolint:gosec // see G704 note above: caller-specified URL is the proxy's purpose
	if err != nil {
		if h.log != nil {
			h.log.WithError(err).WithField("url", r.URL.String()).Debug("forward-proxy fetch failed")
		}
		http.Error(w, "upstream fetch error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body) //nolint:errcheck
}

// copyHeaders copies non-hop-by-hop headers from src to dst.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		if _, hop := hopByHopHeaders[strings.ToLower(k)]; hop {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// newForwardProxyClient builds the egress http.Client. With a non-empty
// upstream it dials every connection through that SOCKS5 proxy (the skysocks
// exit); empty means direct connect.
func newForwardProxyClient(upstreamSOCKS string) (*http.Client, error) {
	transport := &http.Transport{
		Proxy:                 nil,
		MaxIdleConns:          16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if upstreamSOCKS != "" {
		d, err := proxy.SOCKS5("tcp", upstreamSOCKS, nil, proxy.Direct)
		if err != nil {
			return nil, err
		}
		if cd, ok := d.(proxy.ContextDialer); ok {
			transport.DialContext = cd.DialContext
		} else {
			transport.DialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
				return d.Dial(network, addr)
			}
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   forwardProxyTimeout,
		// Don't follow redirects server-side: return them so the caller (the
		// browsing tab) decides, keeping its address bar honest.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// hostIsPublic reports whether host (a hostname or IP literal) resolves only to
// routable public addresses. Loopback, private (RFC1918/ULA), link-local, and
// unspecified addresses make it return false.
func hostIsPublic(host string) bool {
	if host == "" {
		return false
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil || len(resolved) == 0 {
			return false
		}
		ips = resolved
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return false
		}
	}
	return true
}
