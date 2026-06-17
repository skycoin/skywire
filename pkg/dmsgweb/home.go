package dmsgweb

import (
	"bufio"
	"fmt"
	"html"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
)

// HomeAlias is a reserved resolver label that serves a synthetic directory of
// every alias this resolver knows — a navigation index for the proxy. It is
// generated in-process by the resolver and is reachable ONLY through the proxy
// (e.g. http://home.dmsg/); the visor never serves it on a real port. The label
// is reserved: it shadows any same-named configured alias.
const HomeAlias = "home"

var (
	homeServerLabelRe = regexp.MustCompile(`^dmsg[0-9]+$`)
	homeSetupLabelRe  = regexp.MustCompile(`^(?:rsn|tsn)[0-9]*$`)
)

// isHomeHost reports whether host (sans the resolver suffix) is exactly the
// reserved home label — so "home.dmsg" matches but "x.home.dmsg" does not.
func isHomeHost(host, suffix string) bool {
	return strings.EqualFold(strings.TrimSuffix(host, suffix), HomeAlias)
}

type homeEntry struct {
	label string
	pk    cipher.PubKey
}

// renderHomePage builds the self-contained HTML directory from the resolver's
// alias map. Links use the resolver's own suffix so they resolve back through
// the same proxy. Aliases are grouped (this visor / deployment services / setup
// nodes / dmsg servers) and naturally sorted so dmsg2 precedes dmsg10.
func renderHomePage(aliases map[string]cipher.PubKey, suffix string, localPK cipher.PubKey) []byte {
	var self, services, setup, servers []homeEntry
	for label, pk := range aliases {
		e := homeEntry{label, pk}
		switch {
		case localPK != (cipher.PubKey{}) && pk == localPK:
			self = append(self, e)
		case homeServerLabelRe.MatchString(label):
			servers = append(servers, e)
		case homeSetupLabelRe.MatchString(label):
			setup = append(setup, e)
		default:
			services = append(services, e)
		}
	}
	for _, g := range [][]homeEntry{self, services, setup, servers} {
		sort.Slice(g, func(i, j int) bool { return homeNatLess(g[i].label, g[j].label) })
	}

	var b strings.Builder
	b.WriteString("<!doctype html><html lang=en><head><meta charset=utf-8>")
	b.WriteString(`<meta name=viewport content="width=device-width, initial-scale=1">`)
	b.WriteString("<title>skywire resolver</title><style>")
	b.WriteString("body{font:14px/1.5 system-ui,sans-serif;max-width:48rem;margin:2rem auto;padding:0 1rem;color:#222}")
	b.WriteString("h1{font-size:1.4rem}h2{font-size:1rem;margin:1.5rem 0 .3rem;color:#555}")
	b.WriteString("ul{list-style:none;padding:0;margin:0}li{padding:.15rem 0}")
	b.WriteString("a{text-decoration:none;font-weight:600}code{color:#999;font-size:11px;word-break:break-all}")
	b.WriteString("@media(prefers-color-scheme:dark){body{background:#1a1a1a;color:#ddd}h2{color:#aaa}code{color:#777}}")
	b.WriteString("</style></head><body>")
	b.WriteString("<h1>skywire resolver</h1>")
	b.WriteString("<p>Services reachable by name through this resolving proxy. Each link tunnels over the mesh — no clearnet hop.</p>")
	// The visor serves a real landing page at "/"; the deployment services,
	// setup nodes and dmsg servers don't, but they all answer "/health".
	writeHomeGroup(&b, "This visor", self, suffix, "/")
	writeHomeGroup(&b, "Deployment services", services, suffix, "/health")
	writeHomeGroup(&b, "Setup nodes", setup, suffix, "/health")
	writeHomeGroup(&b, "dmsg servers", servers, suffix, "/health")
	if len(self)+len(services)+len(setup)+len(servers) == 0 {
		b.WriteString("<p><em>No aliases configured.</em></p>")
	}
	b.WriteString("</body></html>")
	return []byte(b.String())
}

func writeHomeGroup(b *strings.Builder, title string, es []homeEntry, suffix, path string) {
	if len(es) == 0 {
		return
	}
	fmt.Fprintf(b, "<h2>%s</h2><ul>", html.EscapeString(title))
	for _, e := range es {
		host := html.EscapeString(e.label + suffix)
		href := html.EscapeString("http://" + e.label + suffix + path)
		fmt.Fprintf(b, `<li><a href="%s">%s%s</a> <code>%s</code></li>`, href, host, html.EscapeString(path), e.pk.Hex())
	}
	b.WriteString("</ul>")
}

// homeNatLess compares labels with trailing-number awareness so an indexed set
// (dmsg0, dmsg2, dmsg10) sorts numerically rather than lexically.
func homeNatLess(a, b string) bool {
	ap, an := homeSplitNum(a)
	bp, bn := homeSplitNum(b)
	if ap != bp {
		return ap < bp
	}
	if an != bn {
		return an < bn
	}
	return a < b
}

func homeSplitNum(s string) (string, int) {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) {
		return s, -1
	}
	n, _ := strconv.Atoi(s[i:]) //nolint:errcheck // s[i:] is all digits by construction
	return s[:i], n
}

// serveHomeInProcess returns one end of an in-memory pipe; the other end is fed
// the rendered home page as a single HTTP/1.1 response. Nothing is dialed out —
// the page exists only as seen through the proxy. The returned conn is handed to
// go-socks5, which pipes the browser's request in and the response back.
func serveHomeInProcess(aliases map[string]cipher.PubKey, suffix string, localPK cipher.PubKey) net.Conn {
	body := renderHomePage(aliases, suffix, localPK)
	srvConn, cliConn := net.Pipe()
	go func() {
		defer srvConn.Close() //nolint:errcheck
		br := bufio.NewReader(srvConn)
		// Consume the request line + headers so the peer's write completes;
		// the path is ignored (the directory is the same for every path).
		if _, err := http.ReadRequest(br); err != nil {
			return
		}
		var resp strings.Builder
		resp.WriteString("HTTP/1.1 200 OK\r\n")
		resp.WriteString("Content-Type: text/html; charset=utf-8\r\n")
		fmt.Fprintf(&resp, "Content-Length: %d\r\n", len(body))
		resp.WriteString("Cache-Control: no-store\r\n")
		resp.WriteString("Connection: close\r\n\r\n")
		if _, err := srvConn.Write([]byte(resp.String())); err != nil {
			return
		}
		_, _ = srvConn.Write(body) //nolint:errcheck // best-effort; peer may have gone
	}()
	return cliConn
}
