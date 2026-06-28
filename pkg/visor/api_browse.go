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

// resolveBrowseHost turns a resolving-proxy host into a (PK, port). Accepts a
// bare hex PK (optionally :port), "home.dmsg"/"home.skynet" (this visor's landing
// page), "<pk>.dmsg", or an "alias.dmsg"/"alias.skynet" resolved via the same
// alias map the dmsgweb/skynetweb resolving proxy uses.
func (v *Visor) resolveBrowseHost(host string, reqPort uint16) (cipher.PubKey, uint16, error) {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimPrefix(host, "http://"), "https://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return cipher.PubKey{}, 0, fmt.Errorf("empty host")
	}
	port := uint16(80)
	if reqPort != 0 {
		port = reqPort
	}
	lower := strings.ToLower(host)
	if lower == "home.dmsg" || lower == "home.skynet" {
		return v.conf.PK, port, nil
	}
	// bare hex PK, optionally with :port (a hex PK never contains a colon).
	hp := host
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		if p, err := strconv.Atoi(host[i+1:]); err == nil {
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
	aliases, _ := resolverAliasesAndDmsgServers(v)
	for label, lpk := range resolverAliasMap("", v.conf.PK) {
		if _, ok := aliases[label]; !ok {
			aliases[label] = lpk
		}
	}
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
