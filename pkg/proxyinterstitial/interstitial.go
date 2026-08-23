// Package proxyinterstitial pkg/proxyinterstitial/interstitial.go c4-app-web
//
// A branded, self-contained "Building a route over skywire…" page that the
// native mesh proxies (dmsgweb / skynetweb resolving proxies, skysocks-client
// and -lite) serve to a browser while a route to the target is still being
// established — instead of a raw connection error or a mute hang.
//
// It mirrors the wasm-visor's in-tab interstitial (pkg/wasmhv/browse-bootstrap.html)
// but is fully static: no SharedWorker, no streamed log. A transient page
// auto-refreshes so the browser retries the same URL once the route is warm;
// a hard-failure page stops retrying and offers a manual retry.
//
// Injection model: these proxies dial the target inside a SOCKS5 Dial callback
// (or an io.Copy tunnel). On a *transient* dial failure for a plaintext-HTTP
// (port 80) request, the proxy returns Conn() in place of the error. The
// SOCKS5 library then splices the browser to that conn: the browser's HTTP
// request is read and discarded, and the interstitial HTTP response is written
// back. Raw-TLS (443) requests can't be intercepted without a MITM CA, so
// callers pass those straight through (no interstitial).
package proxyinterstitial

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html"
	"net"
	"strings"
	"time"
)

// refreshSeconds is how long the transient page waits before reloading the
// target URL. Short enough to feel responsive, long enough that a multi-hop
// route setup (a few seconds on first use) usually completes within one or
// two cycles.
const refreshSeconds = 3

// mechanismLabel maps a transport-mechanism key to the user-facing name shown
// in the "Fetching the page over …" step. The three native proxy surfaces that
// serve this page each pass their own: skysocks-client → "skysocks", the
// dmsgweb resolving proxy → "dmsg", the skynetweb resolving proxy → "skynet".
// An empty/unknown key degrades to the generic "skywire".
func mechanismLabel(mechanism string) string {
	switch strings.ToLower(strings.TrimSpace(mechanism)) {
	case "skysocks", "dmsg", "skynet":
		return strings.ToLower(strings.TrimSpace(mechanism))
	default:
		return "skywire"
	}
}

// Page returns the full HTML document for the interstitial. target is the host
// the browser was trying to reach (shown verbatim, HTML-escaped); detail is an
// optional one-line status/error string; mechanism names the forwarding surface
// ("skysocks" / "dmsg" / "skynet") so the fetch step is specific rather than a
// generic "over skywire". When isError is true the page renders the
// hard-failure variant (error styling, no auto-refresh, manual retry button);
// otherwise it renders the transient variant (spinner + meta-refresh auto-retry).
func Page(target, detail, mechanism string, isError bool) string {
	target = html.EscapeString(strings.TrimSpace(target))
	detail = html.EscapeString(strings.TrimSpace(detail))
	mech := mechanismLabel(mechanism)

	title := "Connecting over skywire…"
	heading := "Building a route over the mesh…"
	msg := "Establishing a private route to this site. This can take a few seconds on first use."
	cls := ""
	var refreshMeta string
	if isError {
		title = "Couldn’t reach this site over skywire"
		heading = "Couldn’t build a route"
		msg = "The page did not load through the skywire mesh proxy."
		cls = " err"
	} else {
		// Auto-retry the SAME URL. A browser proxied through the mesh will
		// re-issue the request; by then the route the first attempt kicked
		// off is usually warm.
		refreshMeta = fmt.Sprintf(`<meta http-equiv="refresh" content="%d">`, refreshSeconds)
	}

	detailBlock := ""
	if detail != "" {
		detailBlock = `<p id="mesh-detail" style="display:block">` + detail + `</p>`
	}
	hostBlock := ""
	if target != "" {
		hostBlock = `<small id="mesh-host">` + target + `</small>`
	}

	retry := ""
	if isError {
		retry = `<button class="btn" onclick="location.reload()">Retry</button>`
	}

	// Steps describe what is ACTUALLY happening. The page is served BY this
	// visor's own proxy, so "reaching your visor" is a given, not a step; and
	// the exit is already selected, so the work is route-building, not
	// "finding an exit". The fetch step names the concrete mechanism.
	fetchStep := `<li>Fetching the page over ` + html.EscapeString(mech) + `</li>`

	// Footer: a subtle brand line, plus a "view proxy status" affordance that
	// deep-links to this surface's reserved diagnostic host (served in-process by
	// the same proxy — see pkg/proxystatus). Only a concrete mechanism has a
	// status host; the generic "skywire" fallback shows just the brand line.
	footer := `<div class="foot">routed privately over the skywire mesh`
	if h := statusHost(mech); h != "" {
		footer += ` &middot; <a href="http://` + html.EscapeString(h) + `/">proxy status ›</a>`
	}
	footer += `</div>`
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1">` +
		refreshMeta +
		`<title>` + title + `</title><style>` + css + `</style></head>` +
		`<body><div id="mesh-boot"><div class="card` + cls + `" id="mesh-card">` +
		brandMark +
		`<div class="sp" id="mesh-sp"></div>` +
		`<h1 id="mesh-title">` + html.EscapeString(heading) + `</h1>` +
		`<p id="mesh-msg">` + html.EscapeString(msg) + `</p>` +
		detailBlock +
		`<ul class="steps" id="mesh-steps">` +
		`<li class="active">Building a route across the mesh</li>` +
		`<li>Opening the encrypted tunnel</li>` +
		fetchStep +
		`</ul>` +
		retry +
		hostBlock +
		footer +
		`</div></div></body></html>`
}

// statusHost returns the reserved status host for a mechanism label
// ("skysocks" → "status.skysocks"), or "" for the generic "skywire" fallback
// (which has no dedicated surface). Kept local so this package stays
// dependency-free; the value mirrors pkg/proxystatus.Host.
func statusHost(mech string) string {
	switch mech {
	case "skysocks", "dmsg", "skynet":
		return "status." + mech
	default:
		return ""
	}
}

// skywireCloudPNG is the official Skycoin/Skywire striped-cloud brand mark
// (the cloud-only icon, no wordmark). It is the exact same asset the Angular
// manager UI ships as assets/img/skywire-icon.png, copied into this package so
// it can be embedded and served under the proxies' strict, no-network context
// (a raw data: URI, no external fetch). Using the real brand cloud instead of
// a generic Material Design cloud silhouette.
//
//go:embed skywire-cloud.png
var skywireCloudPNG []byte

// brandMark is the branded header: the Skycoin cloud mark as an inline data:
// URI <img> plus the skywire wordmark. Self-contained so it renders with no
// network access, matching the interstitial's injection context.
var brandMark = `<div class="brand">` +
	`<img alt="skywire" src="data:image/png;base64,` +
	base64.StdEncoding.EncodeToString(skywireCloudPNG) +
	`"><b>skywire</b></div>`

const css = `:root{--bg:#0b0d17;--fg:#c7cbe6;--muted:#7a80a8;--accent:#7c83ff;--accent2:#a06bff;--err:#ff6b8a;--card:#131629;--line:#2b3163}` +
	`html,body{margin:0;height:100%;background:var(--bg);color:var(--fg);font:14px/1.6 system-ui,-apple-system,Segoe UI,Roboto,sans-serif}` +
	`#mesh-boot{position:fixed;inset:0;display:flex;align-items:center;justify-content:center;padding:24px;box-sizing:border-box}` +
	`.card{width:min(440px,92vw);background:var(--card);border:1px solid var(--line);border-radius:14px;padding:26px;box-shadow:0 14px 44px rgba(0,0,0,.5);text-align:center}` +
	`.brand{display:flex;align-items:center;justify-content:center;gap:9px;margin-bottom:16px}` +
	`.brand img{width:24px;height:24px;flex:none}` +
	`.brand b{font-weight:600;letter-spacing:.4px;font-size:15px;background:linear-gradient(90deg,var(--accent),var(--accent2));-webkit-background-clip:text;background-clip:text;color:transparent}` +
	`.sp{width:34px;height:34px;margin:8px auto 16px;border:3px solid var(--line);border-top-color:var(--accent);border-radius:50%;animation:spin .9s linear infinite}` +
	`@keyframes spin{to{transform:rotate(360deg)}}` +
	`#mesh-title{font-size:16px;font-weight:600;color:#e7e9ff;margin:0 0 6px}` +
	`#mesh-msg{color:var(--fg);margin:0}` +
	`#mesh-detail{color:var(--muted);font-size:12.5px;word-break:break-word;margin:9px 0 0;display:none}` +
	`.steps{list-style:none;padding:0;margin:16px auto 4px;text-align:left;font-size:12.5px;color:var(--muted);max-width:280px}` +
	`.steps li{padding:3px 0 3px 24px;position:relative}` +
	`.steps li::before{content:"○";position:absolute;left:3px;color:var(--line)}` +
	`.steps li.active{color:var(--fg)}` +
	`.steps li.active::before{content:"◐";color:var(--accent);animation:spin 1.4s linear infinite;display:inline-block}` +
	`.steps li.done{color:var(--muted)}.steps li.done::before{content:"●";color:var(--accent)}` +
	`.btn{display:inline-block;margin-top:18px;padding:8px 20px;border:1px solid var(--accent);border-radius:9px;background:transparent;color:var(--accent);font:inherit;font-size:13px;cursor:pointer}` +
	`.btn:hover{background:var(--accent);color:#0b0d17}` +
	`#mesh-host{display:block;margin-top:14px;font:12px/1.4 ui-monospace,SFMono-Regular,monospace;color:var(--muted);opacity:.75;word-break:break-all}` +
	`.foot{margin-top:16px;padding-top:12px;border-top:1px solid var(--line);font-size:11.5px;color:var(--muted)}` +
	`.foot a{color:var(--accent);text-decoration:none}.foot a:hover{text-decoration:underline}` +
	// Streaming (chunked) variant: live per-attempt progress lines. Reuses the
	// .steps list; adds a completed (◐→●), errored (✗) and "route up" style.
	`.steps li.err{color:var(--err)}.steps li.err::before{content:"✗";color:var(--err);animation:none}` +
	`.ready{color:var(--accent);margin:12px 0 0;font-size:13px;font-weight:600}` +
	`.err #mesh-title{color:var(--err)}.err .sp{display:none}`

// httpResponse wraps the HTML in a minimal HTTP/1.1 response. Connection:close
// so the browser tears the socket down and honors the meta-refresh cleanly;
// no-store so a transient page is never cached in place of the real content.
func httpResponse(target, detail, mechanism string, isError bool) []byte {
	bodyStr := Page(target, detail, mechanism, isError)
	status := "200 OK"
	if isError {
		status = "502 Bad Gateway"
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "HTTP/1.1 %s\r\n", status)
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(bodyStr))
	b.WriteString("Cache-Control: no-store, must-revalidate\r\n")
	b.WriteString("Connection: close\r\n")
	b.WriteString("\r\n")
	b.WriteString(bodyStr)
	return b.Bytes()
}

// Conn returns a net.Conn that serves the interstitial as a one-shot HTTP
// response. Reads yield the response bytes then EOF; writes (the browser's
// request) are discarded. LocalAddr/RemoteAddr return *net.TCPAddr so it can
// be handed straight to go-socks5, which unconditionally type-asserts the
// address to *net.TCPAddr when building its BND reply.
func Conn(target, detail, mechanism string, isError bool) net.Conn {
	return &interstitialConn{r: bytes.NewReader(httpResponse(target, detail, mechanism, isError))}
}

type interstitialConn struct {
	r      *bytes.Reader
	closed bool
}

func (c *interstitialConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// Write discards the browser's request bytes; the response is fixed.
func (c *interstitialConn) Write(p []byte) (int, error) { return len(p), nil }

func (c *interstitialConn) Close() error { c.closed = true; return nil }

func (c *interstitialConn) LocalAddr() net.Addr  { return loopbackAddr }
func (c *interstitialConn) RemoteAddr() net.Addr { return loopbackAddr }

func (c *interstitialConn) SetDeadline(time.Time) error      { return nil }
func (c *interstitialConn) SetReadDeadline(time.Time) error  { return nil }
func (c *interstitialConn) SetWriteDeadline(time.Time) error { return nil }

var loopbackAddr = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}

// ShouldServe reports whether a failed dial to the given target port should be
// answered with a transient interstitial rather than surfacing the error.
// Only plaintext HTTP (port 80, or an empty/implicit port) qualifies: a raw
// TLS (443) or arbitrary non-HTTP stream can't carry an HTML page, and writing
// one would corrupt the tunnel. err is classified so genuinely permanent
// failures (bad hostname, refused-by-policy) can be surfaced as the hard
// variant by the caller if it chooses; see IsTransient.
func ShouldServe(port string) bool {
	switch strings.TrimSpace(port) {
	case "", "80", "8080":
		return true
	default:
		return false
	}
}

// ServeSOCKS5 answers a not-yet-connected SOCKS5 client (e.g. a browser
// pointed at skysocks-client's :1080) with a branded interstitial when the
// mesh route to the exit is down. skysocks-client is a transparent tunnel —
// the SOCKS5 handshake normally rides end-to-end to the exit — so when the
// client can't open a stream it must speak just enough SOCKS5 itself to reach
// the point of writing an HTTP body: complete the greeting, accept the CONNECT
// request, then (only for a plaintext-HTTP target, port 80) serve the
// interstitial. HTTPS/other-port requests are declined so raw TLS is never
// corrupted. Best-effort and deadline-bounded so it never stalls the client's
// reconnect. Returns nil if it served a page, an error otherwise; the caller
// closes conn regardless.
//
// statusOverride lets the caller answer a reserved in-process host (e.g.
// status.skysocks) itself instead of the interstitial: once the CONNECT target
// host is parsed and the SOCKS5 reply + browser request have been exchanged, if
// statusOverride != nil and it returns a non-nil body for that host, the body is
// written verbatim as the HTTP response and the interstitial is skipped. This
// keeps the reserved status page reachable exactly when the exit is down — the
// moment the user most needs to see why — which the interstitial would otherwise
// shadow. Pass nil for the plain interstitial-only behaviour. The closure lives
// in the caller so this package stays free of any pkg/proxystatus dependency.
func ServeSOCKS5(conn net.Conn, detail, mechanism string, statusOverride func(host string) []byte) error {
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	defer conn.SetDeadline(time.Time{})                   //nolint:errcheck

	tmp := make([]byte, 2)
	// Greeting: VER=5, NMETHODS, METHODS[NMETHODS].
	if err := readFull(conn, tmp); err != nil {
		return err
	}
	if tmp[0] != 0x05 {
		return fmt.Errorf("not socks5 (ver=0x%02x)", tmp[0])
	}
	nMethods := int(tmp[1])
	if nMethods > 0 {
		if err := readFull(conn, make([]byte, nMethods)); err != nil {
			return err
		}
	}
	// Method selection: no-auth (0x00).
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return err
	}
	// Request: VER=5, CMD, RSV, ATYP, ADDR, PORT.
	head := make([]byte, 4)
	if err := readFull(conn, head); err != nil {
		return err
	}
	if head[0] != 0x05 {
		return fmt.Errorf("bad socks5 request ver=0x%02x", head[0])
	}
	var host string
	switch head[3] {
	case 0x01: // IPv4
		b := make([]byte, 4)
		if err := readFull(conn, b); err != nil {
			return err
		}
		host = net.IP(b).String()
	case 0x03: // domain
		l := make([]byte, 1)
		if err := readFull(conn, l); err != nil {
			return err
		}
		b := make([]byte, int(l[0]))
		if err := readFull(conn, b); err != nil {
			return err
		}
		host = string(b)
	case 0x04: // IPv6
		b := make([]byte, 16)
		if err := readFull(conn, b); err != nil {
			return err
		}
		host = net.IP(b).String()
	default:
		return fmt.Errorf("unsupported socks5 atyp 0x%02x", head[3])
	}
	portB := make([]byte, 2)
	if err := readFull(conn, portB); err != nil {
		return err
	}
	port := int(portB[0])<<8 | int(portB[1])
	// Only plaintext HTTP can carry an HTML interstitial; decline the rest so
	// the caller falls back to tearing down (browser sees a normal failure).
	if !ShouldServe(fmt.Sprintf("%d", port)) {
		return fmt.Errorf("socks5 interstitial: non-HTTP port %d", port)
	}
	// Reply success with a dummy BND.ADDR/PORT so the client proceeds to send
	// its HTTP request.
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	// Drain a chunk of the (short) HTTP request so the browser's write
	// completes, then answer. We don't need the request contents — the response
	// is fixed. Bounded single read; ignore EOF.
	_, _ = conn.Read(make([]byte, 1024)) //nolint:errcheck
	// A reserved in-process host (e.g. status.skysocks) is answered by the
	// caller's override rather than the interstitial, so it stays reachable
	// while the exit is down.
	if statusOverride != nil {
		if body := statusOverride(host); body != nil {
			_, err := conn.Write(body)
			return err
		}
	}
	_, err := conn.Write(httpResponse(host, detail, mechanism, false))
	return err
}

// readFull is io.ReadFull without pulling io into a file that otherwise
// doesn't need it; keeps the SOCKS5 parse self-contained.
func readFull(conn net.Conn, p []byte) error {
	got := 0
	for got < len(p) {
		n, err := conn.Read(p[got:])
		got += n
		if err != nil {
			return err
		}
	}
	return nil
}

// StatusLine turns a transient dial error into a short, human-facing status
// line for the interstitial's detail slot — so the page shows WHY it is waiting
// (a live-ish progress cue) instead of a blank card. It deliberately does not
// echo the raw Go error (yamux/socks internals, PKs) which is noise to a user;
// callers that want the raw error on the page can pass it as detail directly.
// The mapping mirrors the transient signals IsTransient keys on.
func StatusLine(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "202"), strings.Contains(s, "delegated"), strings.Contains(s, "not ready"):
		return "Waiting for the exit to accept the session…"
	case strings.Contains(s, "no route"), strings.Contains(s, "route not"), strings.Contains(s, "no suitable transport"):
		return "Building a route to the exit across the mesh…"
	case strings.Contains(s, "session"):
		return "Opening the encrypted session to the exit…"
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline"), strings.Contains(s, "i/o timeout"):
		return "The route is still warming up — retrying…"
	case strings.Contains(s, "refused"), strings.Contains(s, "cannot connect"), strings.Contains(s, "unreachable"):
		return "The exit is not reachable yet — retrying…"
	default:
		return "Establishing a route over the mesh…"
	}
}

// IsTransient classifies a dial error as a route-setup-in-progress condition
// (worth an auto-refreshing interstitial) versus a hard failure. The patterns
// mirror the transient signals the wasm interstitial keys on: dmsg 202 /
// delegated-server-not-ready, no-route, and generic dial/connect churn during
// multi-hop setup.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, p := range []string{
		"202", "delegated", "no route", "route not", "no suitable transport",
		"session", "timeout", "deadline", "temporarily", "connection refused",
		"cannot connect", "not ready", "unreachable", "i/o timeout",
	} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
