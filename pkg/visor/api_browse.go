// Package visor api_browse.go — HTTP fetch endpoints backing the in-HV-UI
// virtual browser (the native analog of the wasm-visor's skywireVisor.fetchDmsg
// / fetchClearnet). The same browse.js engine runs in the native hypervisor UI;
// instead of the wasm JS hooks it calls these over /api/browse/*.
package visor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsgweb"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skynetweb"
)

const browseFetchTimeout = 60 * time.Second
const browseMaxBody = 16 << 20 // 16 MiB

// BrowseFetchRequest fetches a dmsg/skynet site for the HV-UI browser. Host is
// the resolving-proxy form (NOT dmsg://): a bare PK, "<pk>:<port>", "<pk>.dmsg",
// a friendly alias like "home.dmsg"/"tpd.dmsg", or "name.skynet" — resolved the
// same way the visor's resolving SOCKS5 proxy resolves http://name.dmsg.
type BrowseFetchRequest struct {
	Host   string `json:"host"`
	Port   uint16 `json:"port"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   []byte `json:"body,omitempty"`
	// Scheme: "" / "auto" (skynet route first, dmsg-HTTP fallback), "skynet", or
	// "dmsg". A visor serves port 80 over BOTH a skynet mirror and dmsg (see
	// goServeSkynetMirror), so "auto" reaches the same content either way.
	Scheme string `json:"scheme,omitempty"`
}

// browseStripHost normalizes a resolving-proxy host: drops any http(s):// scheme
// and trailing path, leaving just the host[:port] the resolver keys on.
func browseStripHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimPrefix(host, "http://"), "https://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	return host
}

// browseAliases returns the full resolver alias map (deployment services, setup
// nodes, dmsg servers) plus this visor's own "self" alias — the same set the
// embedded dmsgweb resolving proxy resolves names against.
func (v *Visor) browseAliases() map[string]cipher.PubKey {
	aliases, _ := resolverAliasesAndDmsgServers(v)
	for label, lpk := range resolverAliasMap("", v.conf.PK) {
		if _, ok := aliases[label]; !ok {
			aliases[label] = lpk
		}
	}
	return aliases
}

// browseHomePage renders the resolver's synthetic "home" alias directory if host
// is the reserved home label (home.dmsg / home.skynet), else returns nil. The
// home page is generated in-process and reachable ONLY through the resolver — it
// is NOT a real endpoint, so resolve+fetch would reach nothing. This mirrors the
// SOCKS5 resolving proxy's isHomeHost → serveHomeInProcess path (dmsgweb.runtime)
// so the native browser's home.dmsg matches `curl -x socks5h://… http://home.dmsg/`.
func (v *Visor) browseHomePage(host string) *SkynetHTTPResponse {
	host = browseStripHost(host)
	for _, suffix := range []string{".dmsg", ".skynet"} {
		if dmsgweb.IsHomeHost(host, suffix) {
			body := dmsgweb.RenderHomePage(v.browseAliases(), suffix, v.conf.PK)
			return &SkynetHTTPResponse{
				StatusCode: 200,
				Status:     "200 OK",
				Header:     map[string]string{"Content-Type": "text/html; charset=utf-8"},
				Body:       body,
			}
		}
	}
	return nil
}

// resolveBrowseHost turns a resolving-proxy host into a (PK, port). Accepts a
// bare hex PK (optionally :port), "<pk>.dmsg", or an "alias.dmsg"/"alias.skynet"
// resolved via the same alias map the dmsgweb/skynetweb resolving proxy uses.
// The reserved "home" label is handled earlier by browseHomePage (synthetic
// directory), not here.
func (v *Visor) resolveBrowseHost(host string, reqPort uint16) (cipher.PubKey, uint16, error) {
	host = browseStripHost(host)
	if host == "" {
		return cipher.PubKey{}, 0, fmt.Errorf("empty host")
	}
	port := uint16(80)
	if reqPort != 0 {
		port = reqPort
	}
	lower := strings.ToLower(host)
	// bare hex PK, optionally with :port (a hex PK never contains a colon).
	hp := host
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		// ParseUint with bitSize 16 bounds the value to a valid port, so the
		// uint16 conversion can't overflow.
		if p, err := strconv.ParseUint(host[i+1:], 10, 16); err == nil {
			hp = host[:i]
			if reqPort == 0 {
				port = uint16(p)
			}
		}
	}
	var pk cipher.PubKey
	if err := pk.Set(hp); err == nil {
		return pk, port, nil
	}
	// alias.dmsg / <pk>.dmsg / alias.skynet
	aliases := v.browseAliases()
	for _, suffix := range []string{".dmsg", ".skynet"} {
		if strings.HasSuffix(lower, suffix) {
			_, _, dest, p, err := skynetweb.ParseResolverHost(host, suffix, aliases)
			if err != nil {
				return cipher.PubKey{}, 0, err
			}
			if reqPort == 0 && p != 0 {
				port = p
			}
			return dest, port, nil
		}
	}
	return cipher.PubKey{}, 0, fmt.Errorf("cannot resolve %q (use a pk, <pk>.dmsg, or alias.dmsg)", host)
}

// BrowseFetch performs the request over skynet and/or dmsg per Scheme, returning
// the response in the shared SkynetHTTPResponse shape.
func (v *Visor) BrowseFetch(req BrowseFetchRequest) (*SkynetHTTPResponse, error) {
	// home.dmsg / home.skynet is the resolver's synthetic alias directory, served
	// in-process (it has no real endpoint) — handle it before resolve+fetch.
	if resp := v.browseHomePage(req.Host); resp != nil {
		return resp, nil
	}
	pk, port, err := v.resolveBrowseHost(req.Host, req.Port)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", req.Host, err)
	}
	method := req.Method
	if method == "" {
		method = "GET"
	}
	path := req.Path
	if path == "" {
		path = "/"
	}
	scheme := req.Scheme
	if scheme == "" {
		scheme = "auto"
	}

	// Self-fetch (home.dmsg and friends) has no skynet route to itself — a
	// DialRoutes to our own PK blocks for the whole timeout. The self landing
	// page is served on the dmsg:80 peer interface, so go straight to dmsg.
	if pk == v.conf.PK && (scheme == "auto" || scheme == "skynet") {
		scheme = "dmsg"
	}

	switch scheme {
	case "skynet":
		return v.SkynetHTTP(SkynetHTTPRequest{PK: pk, Port: port, Method: method, Path: path, Body: req.Body})
	case "dmsg":
		return v.dmsgHTTPFetch(pk, port, method, path, req.Body)
	case "auto":
		resp, err := v.SkynetHTTP(SkynetHTTPRequest{PK: pk, Port: port, Method: method, Path: path, Body: req.Body})
		if err == nil {
			return resp, nil
		}
		// skynet route miss / no skynet web server on that port → dmsg-HTTP.
		return v.dmsgHTTPFetch(pk, port, method, path, req.Body)
	default:
		return nil, fmt.Errorf("invalid scheme %q (use auto|skynet|dmsg)", scheme)
	}
}

func (v *Visor) dmsgHTTPFetch(pk cipher.PubKey, port uint16, method, path string, body []byte) (*SkynetHTTPResponse, error) {
	if v.dmsgHTTP == nil {
		return nil, fmt.Errorf("dmsg HTTP client not ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), browseFetchTimeout)
	defer cancel()
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://%s:%d%s", pk.Hex(), port, path), rdr)
	if err != nil {
		return nil, err
	}
	resp, err := v.dmsgHTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("dmsg fetch: %w", err)
	}
	return readBrowseResp(resp)
}

// BrowseClearnetRequest fetches a CLEARNET url through a skysocks exit over a
// skywire route (IP-anonymous; the exit does the egress).
type BrowseClearnetRequest struct {
	ExitPK cipher.PubKey `json:"exit_pk"`
	Method string        `json:"method"`
	URL    string        `json:"url"`
	Body   []byte        `json:"body,omitempty"`
}

// BrowseClearnet originates a route group to the skysocks server, runs SOCKS5
// over a yamux stream, and performs the HTTP(S) request — TLS terminates here
// (system cert pool), so the exit only relays ciphertext for https.
func (v *Visor) BrowseClearnet(req BrowseClearnetRequest) (*SkynetHTTPResponse, error) {
	// Self-PK exit = "fall through to clearnet directly": the local visor does the
	// egress itself (a plain http.Get over the host's default route), NOT a skysocks
	// route to its own PK (which would hang dialing itself, like the home.dmsg
	// self-route). This is the opt-in non-anonymous path — the user set the browse
	// upstream proxy to the local visor's own PK. The visor fetches and the caller
	// inlines, so unlike a browser-direct iframe load it isn't blocked by the target
	// site's X-Frame-Options/frame-ancestors.
	if req.ExitPK == v.conf.PK {
		return v.directClearnetFetch(req)
	}
	if v.router == nil {
		return nil, fmt.Errorf("router not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), browseFetchTimeout)
	defer cancel()
	conn, err := v.router.DialRoutes(ctx, req.ExitPK, 0, routing.Port(skyenv.SkysocksPort), nil)
	if err != nil {
		return nil, fmt.Errorf("dial skysocks route: %w", err)
	}
	sess, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, fmt.Errorf("yamux client: %w", err)
	}
	defer sess.Close() //nolint:errcheck
	sd, err := proxy.SOCKS5("tcp", "skysocks", nil, yamuxStreamDialer{sess})
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext:         func(_ context.Context, network, addr string) (net.Conn, error) { return sd.Dial(network, addr) },
			TLSHandshakeTimeout: 20 * time.Second,
		},
		Timeout: browseFetchTimeout,
	}
	method := req.Method
	if method == "" {
		method = "GET"
	}
	var rdr io.Reader
	if len(req.Body) > 0 {
		rdr = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, rdr)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fetch via skysocks: %w", err)
	}
	return readBrowseResp(resp)
}

// directClearnetFetch performs the request straight to the clearnet over the
// host's default network (no skysocks hop) — the self-PK browse-upstream path.
func (v *Visor) directClearnetFetch(req BrowseClearnetRequest) (*SkynetHTTPResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), browseFetchTimeout)
	defer cancel()
	method := req.Method
	if method == "" {
		method = "GET"
	}
	var rdr io.Reader
	if len(req.Body) > 0 {
		rdr = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, rdr)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: browseFetchTimeout}).Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("direct clearnet fetch: %w", err)
	}
	return readBrowseResp(resp)
}

func readBrowseResp(resp *http.Response) (*SkynetHTTPResponse, error) {
	defer resp.Body.Close()                                         //nolint:errcheck
	body, _ := io.ReadAll(io.LimitReader(resp.Body, browseMaxBody)) //nolint:errcheck
	headers := make(map[string]string)
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}
	return &SkynetHTTPResponse{StatusCode: resp.StatusCode, Status: resp.Status, Header: headers, Body: body}, nil
}

// yamuxStreamDialer opens a fresh yamux stream per SOCKS5 Dial.
type yamuxStreamDialer struct{ sess *yamux.Session }

func (d yamuxStreamDialer) Dial(_, _ string) (net.Conn, error) { return d.sess.Open() }
