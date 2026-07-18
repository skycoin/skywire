// Package dmsgweb pkg/dmsgweb/home.go c4-app-web
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
	"github.com/skycoin/skywire/pkg/skyenv/svcendpoints"
)

// serviceEndpointAlias maps a svcendpoints.Manifest service id to the resolver
// alias label that points at that service's base dmsg address. The id and the
// label coincide for every service except "reward" (aliased "rewards"). Only
// services with a configured, resolvable alias are rendered — a service whose
// base address is empty has no alias entry and is skipped.
var serviceEndpointAlias = map[string]string{
	"dmsgd":  "dmsgd",
	"tpd":    "tpd",
	"ar":     "ar",
	"rf":     "rf",
	"sd":     "sd",
	"ut":     "ut",
	"reward": "rewards",
}

// serviceEndpointOrder fixes the render order of the per-service endpoint
// directory so the page is stable across runs (map iteration is random).
var serviceEndpointOrder = []string{"dmsgd", "tpd", "ar", "rf", "sd", "ut", "reward"}

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

// explorerScript is the dependency-free inline JS that powers the interactive
// rows. It shares two helpers (skyURL builds the target URL from a button's
// row; skyOpen navigates; skySend POSTs and shows the response). All values are
// read from DOM nodes the renderer emitted (data-base / data-path / inputs), so
// there is no server-side string interpolation into JS — no XSS surface beyond
// the attribute escaping already applied.
const explorerScript = `<script>
function skyURL(btn){
  var base=btn.getAttribute('data-base');
  var path=btn.getAttribute('data-path');
  var row=btn.parentNode;
  var inputs=row.querySelectorAll('input');
  var qs=[];
  for(var i=0;i<inputs.length;i++){
    var inp=inputs[i];
    var name=inp.getAttribute('data-name');
    var val=inp.value.trim();
    if(inp.getAttribute('data-kind')==='path'){
      path=path.replace('{'+name+'}',encodeURIComponent(val));
    }else if(val!==''){
      qs.push(encodeURIComponent(name)+'='+encodeURIComponent(val));
    }
  }
  var url='http://'+base+path;
  if(qs.length){url+='?'+qs.join('&');}
  return url;
}
function skyOpen(btn){window.location.href=skyURL(btn);}
function skySend(btn){
  var url=skyURL(btn);
  var ep=btn.closest('.ep');
  var ta=ep.querySelector('textarea[data-kind="body"]');
  var out=ep.querySelector('pre[data-kind="resp"]');
  out.hidden=false;out.textContent='Sending '+url+' …';
  fetch(url,{method:'POST',headers:{'Content-Type':'application/json'},body:ta?ta.value:''})
    .then(function(r){return r.text().then(function(t){
      out.textContent=r.status+' '+r.statusText+'\n\n'+t;});})
    .catch(function(e){out.textContent='error: '+e;});
}
</script>`

// isHomeHost reports whether host (sans the resolver suffix) is exactly the
// reserved home label — so "home.dmsg" matches but "x.home.dmsg" does not.
func isHomeHost(host, suffix string) bool {
	return strings.EqualFold(strings.TrimSuffix(host, suffix), HomeAlias)
}

// IsHomeHost and RenderHomePage export the home-label check + directory renderer
// so the wasm-visor's in-tab resolving fetch (browse overlay) serves the same
// home.dmsg page + alias directory as the socks5 resolving proxy.
func IsHomeHost(host, suffix string) bool { return isHomeHost(host, suffix) }

// RenderHomePage exports renderHomePage (the self-contained alias directory).
func RenderHomePage(aliases map[string]cipher.PubKey, suffix string, localPK cipher.PubKey) []byte {
	return renderHomePage(aliases, suffix, localPK)
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
	b.WriteString("h3{font-size:.9rem;margin:.8rem 0 .2rem;color:#444}")
	b.WriteString("ul{list-style:none;padding:0;margin:0}li{padding:.15rem 0}")
	b.WriteString("a{text-decoration:none;font-weight:600}code{color:#999;font-size:11px;word-break:break-all}")
	b.WriteString("small{color:#888;font-size:10px;font-weight:600;text-transform:uppercase}")
	// Explorer rows: a flex line per endpoint, with inline param inputs and a
	// response area for POST.
	b.WriteString(".ep{padding:.25rem 0;border-top:1px solid #eee}")
	b.WriteString(".ep .row{display:flex;flex-wrap:wrap;gap:.3rem;align-items:baseline}")
	b.WriteString(".ep .desc{color:#888;font-size:11px;flex-basis:100%}")
	b.WriteString(".ep input,.ep textarea{font:inherit;font-size:12px;padding:.1rem .3rem;border:1px solid #ccc;border-radius:3px;background:#fff;color:#222}")
	b.WriteString(".ep input{width:9rem}.ep textarea{width:100%;min-height:3rem;font-family:ui-monospace,monospace}")
	b.WriteString(".ep button{font:inherit;font-size:12px;padding:.1rem .5rem;cursor:pointer;border:1px solid #888;border-radius:3px;background:#f4f4f4;color:#222}")
	b.WriteString(".ep pre{margin:.3rem 0 0;padding:.3rem;background:#f6f6f6;border:1px solid #ddd;border-radius:3px;font-size:11px;white-space:pre-wrap;word-break:break-all;max-height:14rem;overflow:auto}")
	b.WriteString("@media(prefers-color-scheme:dark){body{background:#1a1a1a;color:#ddd}h2{color:#aaa}h3{color:#bbb}code{color:#777}")
	b.WriteString(".ep{border-color:#333}.ep input,.ep textarea{background:#222;color:#ddd;border-color:#444}.ep button{background:#2a2a2a;color:#ddd;border-color:#555}.ep pre{background:#222;border-color:#333}}")
	b.WriteString("</style></head><body>")
	b.WriteString("<h1>skywire resolver</h1>")
	b.WriteString("<p>Services reachable by name through this resolving proxy. Each link tunnels over the mesh — no clearnet hop.</p>")
	// The visor serves a real landing page at "/"; the deployment services,
	// setup nodes and dmsg servers don't, but they all answer "/health". A
	// service that has a manifest section below is omitted from the flat
	// "Deployment services" index so its /health renders exactly once (from the
	// manifest); the rest still get a plain /health link here.
	manifested := manifestedLabels(aliases)
	writeHomeGroup(&b, "This visor", self, suffix, "/")
	var plainServices []homeEntry
	for _, e := range services {
		if !manifested[e.label] {
			plainServices = append(plainServices, e)
		}
	}
	writeHomeGroup(&b, "Deployment services", plainServices, suffix, "/health")
	writeServiceEndpoints(&b, aliases, suffix)
	writeHomeGroup(&b, "Setup nodes", setup, suffix, "/health")
	writeHomeGroup(&b, "dmsg servers", servers, suffix, "/health")
	if len(self)+len(services)+len(setup)+len(servers) == 0 {
		b.WriteString("<p><em>No aliases configured.</em></p>")
	}
	b.WriteString(explorerScript)
	b.WriteString("</body></html>")
	return []byte(b.String())
}

// manifestedLabels returns the set of resolver labels that will be rendered as
// a full manifest section (i.e. the service has manifest endpoints AND a
// configured, resolvable base address). Those labels are dropped from the flat
// "Deployment services" index so /health is not listed twice.
func manifestedLabels(aliases map[string]cipher.PubKey) map[string]bool {
	out := make(map[string]bool)
	for _, id := range serviceEndpointOrder {
		if len(svcendpoints.Manifest[id]) == 0 {
			continue
		}
		label, ok := serviceEndpointAlias[id]
		if !ok {
			continue
		}
		if _, ok := aliases[label]; ok {
			out[label] = true
		}
	}
	return out
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

// writeServiceEndpoints renders a per-service directory of each deployment
// service's notable PUBLIC API endpoints (not just /health). Paths come from
// the deployment-independent svcendpoints.Manifest; the base host comes from
// the resolver's alias map (this visor's resolved config), so links resolve as
// "<alias><suffix><path>" through the same proxy. A service whose base address
// is not configured has no alias entry and is skipped entirely.
func writeServiceEndpoints(b *strings.Builder, aliases map[string]cipher.PubKey, suffix string) {
	var any bool
	for _, id := range serviceEndpointOrder {
		eps := svcendpoints.Manifest[id]
		if len(eps) == 0 {
			continue
		}
		label, ok := serviceEndpointAlias[id]
		if !ok {
			continue
		}
		// Skip services with no configured (resolvable) base address.
		if _, ok := aliases[label]; !ok {
			continue
		}
		if !any {
			b.WriteString("<h2>Service API endpoints</h2>")
			b.WriteString("<p><small>Fill any inputs and Open/Send — requests tunnel through this proxy.</small></p>")
			any = true
		}
		host := label + suffix
		pk := aliases[label]
		fmt.Fprintf(b, "<h3>%s <code>%s</code></h3>", html.EscapeString(label), html.EscapeString(host+" "+pk.Hex()))
		for _, ep := range eps {
			writeEndpoint(b, host, ep)
		}
	}
}

// pathParamRe matches a chi-style "{name}" path-parameter segment. The name is
// captured so it can label the input the renderer emits in its place.
var pathParamRe = regexp.MustCompile(`\{([^/}]+)\}`)

// writeEndpoint renders one endpoint as an interactive row. The control shape is
// chosen from the endpoint: a plain GET with no params is a link; anything with
// path/query params gets inputs + an "Open" button; a POST gets a body textarea
// (plus any path/query inputs) + a "Send" button that fetches through the proxy
// and shows the response without navigating away. base is "<alias><suffix>";
// the inline JS finds this row's inputs by DOM traversal, so no id is needed.
func writeEndpoint(b *strings.Builder, base string, ep svcendpoints.Endpoint) {
	pathParams := pathParamRe.FindAllStringSubmatch(ep.Path, -1)
	isPost := strings.EqualFold(ep.Method, "POST")
	simple := !isPost && len(pathParams) == 0 && len(ep.Query) == 0

	b.WriteString(`<div class="ep">`)

	if simple {
		// No params, GET — a plain link, exactly as before.
		href := html.EscapeString("http://" + base + ep.Path)
		fmt.Fprintf(b, `<div class="row"><a href="%s">%s</a> <small>%s</small> — %s</div>`,
			href, html.EscapeString(base+ep.Path), html.EscapeString(ep.Method), html.EscapeString(ep.Desc))
		b.WriteString("</div>")
		return
	}

	// Header row: method + the path template (params shown literally) + desc.
	b.WriteString(`<div class="row">`)
	fmt.Fprintf(b, `<small>%s</small> <code>%s%s</code>`,
		html.EscapeString(ep.Method), html.EscapeString(base), html.EscapeString(ep.Path))
	b.WriteString("</div>")
	fmt.Fprintf(b, `<div class="desc">%s</div>`, html.EscapeString(ep.Desc))

	// Inputs row: one input per path param, then one per query param.
	b.WriteString(`<div class="row">`)
	for _, m := range pathParams {
		name := m[1]
		fmt.Fprintf(b, `<input data-kind="path" data-name="%s" placeholder="%s" aria-label="%s">`,
			html.EscapeString(name), html.EscapeString(name), html.EscapeString(name))
	}
	for _, q := range ep.Query {
		ph := q.Name
		if q.Example != "" {
			ph = q.Name + "=" + q.Example
		}
		title := q.Name
		if q.Desc != "" {
			title = q.Name + " — " + q.Desc
		}
		fmt.Fprintf(b, `<input data-kind="query" data-name="%s" placeholder="%s" title="%s" aria-label="%s">`,
			html.EscapeString(q.Name), html.EscapeString(ph), html.EscapeString(title), html.EscapeString(q.Name))
	}

	// data-base / data-path carry the (already-safe) values the JS substitutes
	// into. They are attribute-escaped; the JS treats them as opaque strings.
	dataAttrs := fmt.Sprintf(`data-base="%s" data-path="%s"`,
		html.EscapeString(base), html.EscapeString(ep.Path))

	if isPost {
		fmt.Fprintf(b, `<button type="button" onclick="skySend(this)" %s>Send</button>`, dataAttrs)
		b.WriteString("</div>") // close inputs row
		body := ep.Body
		if body == "" {
			body = "request body"
		}
		fmt.Fprintf(b, `<div class="row"><textarea data-kind="body" placeholder="%s" aria-label="request body"></textarea></div>`,
			html.EscapeString(body))
		b.WriteString(`<pre data-kind="resp" hidden></pre>`)
	} else {
		fmt.Fprintf(b, `<button type="button" onclick="skyOpen(this)" %s>Open</button>`, dataAttrs)
		b.WriteString("</div>") // close inputs row
	}
	b.WriteString("</div>")
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
