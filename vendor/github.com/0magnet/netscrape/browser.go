//go:build js && wasm

package netscrape

import (
	"encoding/base64"
	"strings"
	"syscall/js"
)

const startPage = "data:text/html,<body style='font-family:sans-serif;padding:2em'>" +
	"<h1>A browser, in Go</h1><p>The chrome is syscall/js. Each tab is an iframe. " +
	"Use + for a new tab, or type a URL above.</p></body>"

// home is the page a new tab opens. A host that has something of its own to
// show — a demo site served beside the browser, a mesh index — sets
// globalThis.__netscrapeStart to its URL; everyone else gets the built-in
// page, which is what this did before there was a way to say otherwise.
func home() string {
	if v := js.Global().Get("__netscrapeStart"); v.Type() == js.TypeString && v.String() != "" {
		return v.String()
	}
	return startPage
}

// navShim runs inside the sandboxed page. A sandboxed srcdoc has an opaque
// origin and can't navigate itself across sites, so it relays intent to the
// parent (this Go browser) via postMessage: link clicks and form GETs become a
// {shipyardNav:<absolute url>} message, which the parent loads in the tab. This
// is the seam a full port grows the fetch relay (lazy images, XHR) onto.
const navShim = `<script>
(function(){
  function abs(u){ try{ return new URL(u,document.baseURI).href; }catch(e){ return u; } }
  function nav(u){ try{ parent.postMessage({shipyardNav:abs(u)},"*"); }catch(e){} }
  document.addEventListener("click",function(e){
    var n=e.target; while(n && n.tagName!=="A") n=n.parentNode;
    if(n && n.getAttribute("href")){ e.preventDefault(); nav(n.getAttribute("href")); }
  },true);
  document.addEventListener("submit",function(e){
    var f=e.target; if(!f||f.tagName!=="FORM") return; e.preventDefault();
    var q=[]; for(var i=0;i<f.elements.length;i++){ var el=f.elements[i]; if(el.name) q.push(encodeURIComponent(el.name)+"="+encodeURIComponent(el.value||"")); }
    var a=f.getAttribute("action")||""; nav(a+(a.indexOf("?")<0?"?":"&")+q.join("&"));
  },true);

  // Resource relay: the sandbox can't reach the proxy itself, so it asks the
  // parent to fetch each stylesheet/image and hands back the bytes. CSS is
  // inlined as <style>, images as data: URIs.
  var fid=0, pend={};
  function get(u){ return new Promise(function(res){ var id=++fid; pend[id]=res; parent.postMessage({shipyardFetch:{id:id,url:abs(u)}},"*"); }); }
  window.addEventListener("message",function(e){
    var d=e.data; if(!d||!d.shipyardFetchResult) return;
    var r=d.shipyardFetchResult, cb=pend[r.id]; if(cb){ delete pend[r.id]; cb(r); }
  });
  function b64utf8(b){ try{ return decodeURIComponent(escape(atob(b))); }catch(e){ return atob(b); } }
  function inline(){
    document.querySelectorAll('link[rel~="stylesheet"][href]').forEach(function(l){
      get(l.getAttribute("href")).then(function(r){ if(r&&r.ok){ var s=document.createElement("style"); s.textContent=b64utf8(r.b64); l.parentNode.replaceChild(s,l); } });
    });
    document.querySelectorAll('img[src]').forEach(function(im){
      var s=im.getAttribute("src"); if(!s||/^data:/.test(s)) return;
      get(s).then(function(r){ if(r&&r.ok){ im.src="data:"+(r.ct||"application/octet-stream")+";base64,"+r.b64; } });
    });
  }
  if(document.readyState==="loading") document.addEventListener("DOMContentLoaded",inline); else inline();
})();
</script>`

var (
	doc    js.Value
	strip  js.Value // tab strip
	views  js.Value // stacked iframe area
	addr   js.Value // shared address bar
	back   js.Value // history buttons, greyed when there is nowhere to go
	fwd    js.Value
	reload js.Value
	tabs   []*tab
	active = -1
	nextID int
)

type tab struct {
	btn, lbl, ico, frame js.Value
	hist                 []string
	pos                  int
	title                string // the page's own <title>, when it has one
	loading              bool
}

func mk(tag string) js.Value { return doc.Call("createElement", tag) }

// fetchVia is the transport seam. A host can inject
// globalThis.__netscrapeFetch(url) → a Response-like promise (with
// .text()/.arrayBuffer()/.headers) to route through its own network — this is
// where skywire's wasm visor plugs in its dmsg mesh fetch. Absent one, the
// browser uses the same-origin /fetch clearnet proxy.
func fetchVia(url string) js.Value {
	g := js.Global()
	if t := g.Get("__netscrapeFetch"); t.Type() == js.TypeFunction {
		return t.Invoke(url)
	}
	enc := g.Get("encodeURIComponent").Invoke(url).String()
	return g.Call("fetch", "/fetch?url="+enc)
}

func btn(label, style string) js.Value {
	b := mk("button")
	b.Set("textContent", label)
	b.Get("style").Set("cssText", "background:#2a2342;color:#cdd2da;border:1px solid #3a3352;cursor:pointer;font:13px monospace;"+style)
	return b
}

// labelFor is a tab's name: the host, which is the part a person recognises.
// A generated page has no host worth showing, so it gets a plain word instead.
func labelFor(url string) string {
	switch {
	case strings.HasPrefix(url, "data:"):
		return "new tab"
	case strings.HasPrefix(url, "about:"), strings.HasPrefix(url, "blob:"):
		return url
	}
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "tab"
	}
	return s
}

// DirectLoader lets a host claim a URL for NATIVE rendering: return the src to
// put on the tab's iframe and ok, and the page loads as an ordinary document
// instead of being fetched and transcoded into a sandboxed srcdoc. It exists
// for pages the host can already serve on its own origin — skywire's
// /vnet/<port>/ service-worker URLs are the driving case, where transcoding an
// app the browser could render natively would only degrade it. Everything not
// claimed keeps going through the transport.
//
// A claimed page is same-origin and UNSANDBOXED, so only claim URLs the host
// itself serves.
var DirectLoader func(url string) (src string, ok bool)

// load renders a URL into a tab's iframe. A data:/about:/blob: URL goes straight
// to the iframe; anything else — an http(s) page, or a bare host like
// example.com or home.dmsg — goes through the transport (fetchPage), which lets
// the host's fetchVia decide clearnet vs mesh, unless DirectLoader claims it
// for native rendering. A scheme-less address is normalised to http:// so the
// transport always gets a URL.
func load(t *tab, url string) {
	// Name the tab after where it is. "tab 3" tells a person nothing once
	// three of them are open; the host is what they recognise, and it fits in
	// a strip where a whole URL never would.
	if t != nil && t.lbl.Truthy() {
		t.lbl.Set("textContent", labelFor(url))
	}
	if strings.HasPrefix(url, "data:") || strings.HasPrefix(url, "about:") || strings.HasPrefix(url, "blob:") {
		t.frame.Call("removeAttribute", "srcdoc")
		t.frame.Set("src", url)
	} else {
		if !strings.Contains(url, "://") {
			url = "http://" + url
		}
		if DirectLoader != nil {
			if src, ok := DirectLoader(url); ok {
				// Drop the transcoder's sandbox: a natively rendered page is
				// the host's own, and Angular-style apps need same-origin.
				t.frame.Call("removeAttribute", "srcdoc")
				t.frame.Call("removeAttribute", "sandbox")
				setLoading(t, true)
				t.frame.Set("src", src)
				// A natively rendered page is same-origin, so its title and
				// icon can simply be read once it has loaded — no transcoding
				// pass to pick them out of.
				watchDirect(t, url)
				if active >= 0 && tabs[active] == t {
					addr.Set("value", url)
				}
				return
			}
		}
		fetchPage(t, url)
	}
	if active >= 0 && tabs[active] == t {
		addr.Set("value", url)
	}
}

// fetchPage is the clearnet transport + first transcoding pass. It fetches the
// page over the same-origin /fetch proxy (the tab can't reach it cross-origin),
// then renders it as a sandboxed srcdoc with a <base> so the page's own
// relative URLs resolve. The heavier transcoding — inlining stylesheets and
// images, and the shims that relay the sandboxed page's navigation and fetches
// back through the transport — layers on top of this seam.
func fetchPage(t *tab, url string) {
	setLoading(t, true)
	fail := func(msg string) {
		setLoading(t, false)
		setTitle(t, "", url)
		t.frame.Call("removeAttribute", "src")
		t.frame.Set("srcdoc", "<body style=\"font:14px system-ui,sans-serif;padding:3em 2em;color:#cdd2da;background:#15131c\">"+
			"<div style=\"max-width:32em;margin:auto\"><div style=\"font-size:2em;margin-bottom:.4em\">This page did not load</div>"+
			"<div style=\"opacity:.8;line-height:1.5\">"+htmlEscape(msg)+"</div>"+
			"<div style=\"opacity:.5;margin-top:1.2em;font:12px monospace;word-break:break-all\">"+htmlEscape(url)+"</div></div></body>")
	}
	var onResp, onText, onErr js.Func
	onErr = js.FuncOf(func(_ js.Value, a []js.Value) any {
		m := "fetch failed"
		if len(a) > 0 {
			m = a[0].Call("toString").String()
		}
		fail(m)
		onResp.Release()
		onText.Release()
		onErr.Release()
		return nil
	})
	onText = js.FuncOf(func(_ js.Value, a []js.Value) any {
		html := a[0].String()
		t.frame.Call("removeAttribute", "src")
		t.frame.Call("setAttribute", "sandbox", "allow-scripts") // sandboxed: no allow-same-origin
		t.frame.Set("srcdoc", "<base href=\""+url+"\">"+navShim+html)
		// The page has arrived, so the tab can finally say what it is holding.
		setTitle(t, pageTitle(html), url)
		setLoading(t, false)
		setFavicon(t, faviconURL(html, url))
		onResp.Release()
		onText.Release()
		onErr.Release()
		return nil
	})
	onResp = js.FuncOf(func(_ js.Value, a []js.Value) any { return a[0].Call("text") })
	fetchVia(url).Call("then", onResp).Call("then", onText).Call("catch", onErr)
}

func navigate(t *tab, url string) {
	if t.pos >= 0 && t.pos < len(t.hist)-1 {
		t.hist = t.hist[:t.pos+1]
	}
	t.hist = append(t.hist, url)
	t.pos = len(t.hist) - 1
	load(t, url)
}

func activate(i int) {
	if i < 0 || i >= len(tabs) {
		return
	}
	active = i
	for j, t := range tabs {
		on := j == i
		if on {
			t.frame.Get("style").Set("display", "block")
			t.btn.Get("style").Set("background", "#2a2342")
		} else {
			t.frame.Get("style").Set("display", "none")
			t.btn.Get("style").Set("background", "transparent")
		}
	}
	addr.Set("value", tabs[i].hist[tabs[i].pos])
	syncNav()
}

// syncNav greys the history buttons when there is nowhere to go, so they say
// whether they will do anything before they are pressed.
func syncNav() {
	t := cur()
	set := func(el js.Value, on bool) {
		if !el.Truthy() {
			return
		}
		el.Set("disabled", !on)
		if on {
			el.Get("style").Set("opacity", "1")
			el.Get("style").Set("cursor", "pointer")
		} else {
			el.Get("style").Set("opacity", ".35")
			el.Get("style").Set("cursor", "default")
		}
	}
	set(back, t != nil && t.pos > 0)
	set(fwd, t != nil && t.pos < len(t.hist)-1)
}

// cur is the tab in front, or nil when the browser has not opened one yet.
func cur() *tab {
	if active >= 0 && active < len(tabs) {
		return tabs[active]
	}
	return nil
}

// setLoading marks a tab busy. The favicon slot carries the indicator, which is
// where a browser shows it and costs the tab no extra width.
func setLoading(t *tab, on bool) {
	if t == nil {
		return
	}
	t.loading = on
	if on {
		t.ico.Get("style").Set("visibility", "hidden")
		t.btn.Get("style").Set("opacity", ".7")
	} else {
		t.btn.Get("style").Set("opacity", "1")
	}
	if t == cur() && reload.Truthy() {
		if on {
			reload.Set("textContent", "×")
			reload.Set("title", "stop")
		} else {
			reload.Set("textContent", "⟳")
			reload.Set("title", "reload")
		}
	}
}

// setTitle names a tab after the page. A page's own <title> is what a person
// recognises; the host is the fallback for a page that has none.
func setTitle(t *tab, title, url string) {
	title = strings.TrimSpace(title)
	if len(title) > 120 {
		title = title[:120]
	}
	t.title = title
	if title == "" {
		title = labelFor(url)
	}
	if t.lbl.Truthy() {
		t.lbl.Set("textContent", title)
		t.btn.Set("title", title+"\n"+url)
	}
}

// setFavicon points the tab's icon at iconURL, fetched through the transport
// and inlined as a data: URI — the icon usually lives on the page's own origin,
// which the chrome cannot reach directly any more than the page could.
func setFavicon(t *tab, iconURL string) {
	if iconURL == "" || !t.ico.Truthy() {
		return
	}
	g := js.Global()
	var onResp, onBuf, onErr js.Func
	ct := "image/x-icon"
	done := func() { onResp.Release(); onBuf.Release(); onErr.Release() }
	onErr = js.FuncOf(func(_ js.Value, _ []js.Value) any { done(); return nil })
	onBuf = js.FuncOf(func(_ js.Value, a []js.Value) any {
		u8 := g.Get("Uint8Array").New(a[0])
		b := make([]byte, u8.Get("length").Int())
		js.CopyBytesToGo(b, u8)
		// An empty or error body is not an icon; leave the slot blank rather
		// than showing a broken image.
		if len(b) > 0 {
			t.ico.Set("src", "data:"+ct+";base64,"+base64.StdEncoding.EncodeToString(b))
			t.ico.Get("style").Set("visibility", "visible")
		}
		done()
		return nil
	})
	onResp = js.FuncOf(func(_ js.Value, a []js.Value) any {
		if a[0].Get("status").Truthy() && a[0].Get("status").Int() >= 400 {
			done()
			return g.Get("Promise").Call("reject")
		}
		if h := a[0].Get("headers").Call("get", "content-type"); h.Truthy() {
			ct = h.String()
		}
		return a[0].Call("arrayBuffer")
	})
	fetchVia(iconURL).Call("then", onResp).Call("then", onBuf).Call("catch", onErr)
}

// watchDirect reads a natively rendered page's title and icon once it has
// loaded. It is same-origin by construction (DirectLoader only claims URLs the
// host serves), so the document is simply readable — but a single-page app
// paints its real title a beat after load, so the title is re-read once more
// shortly after. Any access can still throw if the host redirected the frame
// somewhere cross-origin; that just means no title, not a broken tab.
func watchDirect(t *tab, url string) {
	read := func() {
		defer func() { _ = recover() }()
		d := t.frame.Get("contentDocument")
		if !d.Truthy() {
			return
		}
		if title := d.Get("title"); title.Truthy() {
			setTitle(t, title.String(), url)
		}
		if link := d.Call("querySelector", `link[rel~="icon"]`); link.Truthy() {
			if href := link.Get("href"); href.Truthy() {
				setFavicon(t, href.String())
				return
			}
		}
		setFavicon(t, absURL("/favicon.ico", url))
	}
	var onLoad js.Func
	onLoad = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		setLoading(t, false)
		read()
		// The second read catches an SPA that titles itself after routing.
		var later js.Func
		later = js.FuncOf(func(_ js.Value, _ []js.Value) any {
			read()
			later.Release()
			return nil
		})
		js.Global().Call("setTimeout", later, 1200)
		t.frame.Call("removeEventListener", "load", onLoad)
		onLoad.Release()
		return nil
	})
	t.frame.Call("addEventListener", "load", onLoad)
}

// pageTitle pulls <title> out of fetched HTML.
func pageTitle(html string) string {
	l := strings.ToLower(html)
	i := strings.Index(l, "<title")
	if i < 0 {
		return ""
	}
	j := strings.Index(l[i:], ">")
	if j < 0 {
		return ""
	}
	i += j + 1
	k := strings.Index(l[i:], "</title>")
	if k < 0 {
		return ""
	}
	return htmlUnescape(html[i : i+k])
}

// htmlEscape makes text safe to drop into the error page's markup.
func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// htmlUnescape handles the handful of entities a title realistically carries.
func htmlUnescape(s string) string {
	r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&apos;", "'", "&nbsp;", " ")
	return r.Replace(s)
}

// faviconURL finds the icon a page declares, falling back to /favicon.ico at
// the site root the way browsers do. Returns an absolute URL.
func faviconURL(html, pageURL string) string {
	l := strings.ToLower(html)
	for pos := 0; ; {
		i := strings.Index(l[pos:], "<link")
		if i < 0 {
			break
		}
		i += pos
		j := strings.Index(l[i:], ">")
		if j < 0 {
			break
		}
		tag := l[i : i+j]
		if strings.Contains(tag, "rel=") && strings.Contains(tag, "icon") {
			if href := attrValue(html[i:i+j], "href"); href != "" {
				return absURL(href, pageURL)
			}
		}
		pos = i + j
	}
	return absURL("/favicon.ico", pageURL)
}

// attrValue reads a quoted attribute out of a tag.
func attrValue(tag, name string) string {
	l := strings.ToLower(tag)
	i := strings.Index(l, name+"=")
	if i < 0 {
		return ""
	}
	rest := tag[i+len(name)+1:]
	if rest == "" {
		return ""
	}
	q := rest[0]
	if q != '"' && q != '\'' {
		if k := strings.IndexAny(rest, " \t\r\n"); k >= 0 {
			return rest[:k]
		}
		return rest
	}
	if k := strings.IndexByte(rest[1:], q); k >= 0 {
		return rest[1 : 1+k]
	}
	return ""
}

// absURL resolves ref against base using the engine's own URL parser.
func absURL(ref, base string) string {
	u := js.Global().Get("URL")
	if !u.Truthy() {
		return ref
	}
	defer func() { _ = recover() }() // a malformed base throws; no icon is fine
	return u.New(ref, base).Get("href").String()
}

func onClick(el js.Value, fn func()) {
	el.Call("addEventListener", "click", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 {
			a[0].Call("stopPropagation")
		}
		fn()
		return nil
	}))
}

// indexOf finds a tab's CURRENT position. Handlers close over the tab itself
// rather than the index it happened to have when it was created: indices shift
// every time a tab closes, and the old fix for that — cloning the button to
// drop its listeners — silently detached the label and icon nodes the tab still
// held references to, so a tab could never update its own title again.
func indexOf(t *tab) int {
	for i, x := range tabs {
		if x == t {
			return i
		}
	}
	return -1
}

func addTab(url string) {
	nextID++
	t := &tab{}
	t.btn = mk("div")
	t.btn.Get("style").Set("cssText", "display:flex;align-items:center;gap:.4em;max-width:12em;padding:.25em .6em;cursor:pointer;font:11px monospace;color:#cdd2da;border:1px solid #2a2342;border-bottom:0;border-radius:5px 5px 0 0;white-space:nowrap")
	// The favicon sits where every browser puts it, and holds its space from
	// the start so a tab does not jump sideways when the icon arrives.
	t.ico = mk("img")
	t.ico.Get("style").Set("cssText", "width:12px;height:12px;flex:0 0 12px;object-fit:contain;visibility:hidden")
	t.lbl = mk("span")
	t.lbl.Set("textContent", "new tab")
	t.lbl.Get("style").Set("cssText", "overflow:hidden;text-overflow:ellipsis;white-space:nowrap")
	x := mk("span")
	x.Set("textContent", "×")
	x.Get("style").Set("cssText", "opacity:.6;flex:0 0 auto")
	t.btn.Call("appendChild", t.ico)
	t.btn.Call("appendChild", t.lbl)
	t.btn.Call("appendChild", x)

	t.frame = mk("iframe")
	t.frame.Get("style").Set("cssText", "position:absolute;inset:0;width:100%;height:100%;border:0;background:#fff;display:none")
	if len(tabs) == 0 {
		t.frame.Set("id", "browser-frame") // the first tab's frame, for harnesses
	}

	onClick(t.btn, func() { activate(indexOf(t)) })
	onClick(x, func() { closeTab(indexOf(t)) })
	// Middle-click closes a tab, as everywhere else.
	t.btn.Call("addEventListener", "auxclick", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 && a[0].Get("button").Int() == 1 {
			a[0].Call("preventDefault")
			closeTab(indexOf(t))
		}
		return nil
	}))

	strip.Call("insertBefore", t.btn, strip.Get("lastChild")) // before the + button
	views.Call("appendChild", t.frame)
	tabs = append(tabs, t)
	navigate(t, url)
	activate(indexOf(t))
}

func closeTab(i int) {
	if i < 0 || i >= len(tabs) || len(tabs) <= 1 {
		return // keep at least one tab
	}
	t := tabs[i]
	t.btn.Call("remove")
	t.frame.Call("remove")
	tabs = append(tabs[:i], tabs[i+1:]...)
	if active >= len(tabs) {
		active = len(tabs) - 1
	}
	activate(active)
}

// relayResource fetches a resource through the /fetch proxy on the sandbox's
// behalf and posts the bytes back (base64) to the requesting iframe window.
func relayResource(source, id js.Value, url string) {
	g := js.Global()
	reply := func(ok bool, ct, b64 string) {
		res := g.Get("Object").New()
		res.Set("id", id)
		res.Set("ok", ok)
		res.Set("ct", ct)
		res.Set("b64", b64)
		msg := g.Get("Object").New()
		msg.Set("shipyardFetchResult", res)
		source.Call("postMessage", msg, "*")
	}
	var onResp, onBuf, onErr js.Func
	ct := ""
	onErr = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		reply(false, "", "")
		onResp.Release()
		onBuf.Release()
		onErr.Release()
		return nil
	})
	onBuf = js.FuncOf(func(_ js.Value, a []js.Value) any {
		u8 := g.Get("Uint8Array").New(a[0])
		b := make([]byte, u8.Get("length").Int())
		js.CopyBytesToGo(b, u8)
		reply(true, ct, base64.StdEncoding.EncodeToString(b))
		onResp.Release()
		onBuf.Release()
		onErr.Release()
		return nil
	})
	onResp = js.FuncOf(func(_ js.Value, a []js.Value) any {
		if h := a[0].Get("headers").Call("get", "content-type"); h.Truthy() {
			ct = h.String()
		}
		return a[0].Call("arrayBuffer")
	})
	fetchVia(url).Call("then", onResp).Call("then", onBuf).Call("catch", onErr)
}

// Open builds the browser chrome into root and starts it: a tab strip, an
// address bar (back/forward/reload/Go), and the stacked iframe views, plus the
// window-level message relay a sandboxed page's navShim posts navigation and
// resource-fetch intent to. It returns immediately — the browser lives on
// through its event handlers, so a host importing netscrape into its OWN wasm
// (skywire's visor page) calls Open once and keeps its runtime alive itself; it
// does NOT need a separate wasm module with its own Go runtime. The standalone
// binary (cmd/browser) is a thin wrapper that resolves root and blocks.
//
// Transport is globalThis.__netscrapeFetch(url) if the host set one (the visor
// plugs in its dmsg/clearnet fetch there); otherwise the same-origin /fetch
// proxy.
func Open(root js.Value) {
	if !root.Truthy() {
		return
	}
	doc = js.Global().Get("document")
	// Style the host WITHOUT clobbering its geometry. Overwriting cssText here
	// destroyed the positioning a window manager had already given the element:
	// mounted into a desk window's body, the browser covered the whole frame,
	// so the tab strip sat under the title bar and the window's drag handler
	// swallowed every click meant for a tab. Supply a position only when the
	// host genuinely has none.
	st := root.Get("style")
	if pos := js.Global().Call("getComputedStyle", root).Get("position").String(); pos == "" || pos == "static" {
		st.Set("position", "absolute")
		st.Set("inset", "0")
	}
	st.Set("display", "flex")
	st.Set("flexDirection", "column")
	st.Set("background", "#15131c")
	st.Set("overflow", "hidden")

	strip = mk("div")
	strip.Get("style").Set("cssText", "display:flex;gap:2px;align-items:flex-end;background:#100d18;border-bottom:1px solid #2a2342;padding:3px 3px 0;min-height:24px")
	plus := btn("+", "padding:.2em .55em;border-radius:5px 5px 0 0")
	onClick(plus, func() { addTab(home()) })
	strip.Call("appendChild", plus)

	bar := mk("div")
	bar.Get("style").Set("cssText", "display:flex;gap:4px;padding:4px;background:#100d18;border-bottom:1px solid #2a2342")
	back, fwd, reload = btn("◀", "padding:2px 8px"), btn("▶", "padding:2px 8px"), btn("⟳", "padding:2px 8px")
	back.Set("title", "back")
	fwd.Set("title", "forward")
	reload.Set("title", "reload")
	addr = mk("input")
	addr.Set("spellcheck", false)
	addr.Set("placeholder", "search or enter address")
	addr.Get("style").Set("cssText", "flex:1;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:2px 8px;font:13px monospace")
	// Clicking the address bar selects the whole URL, so typing replaces it
	// rather than landing in the middle of what is already there.
	addr.Call("addEventListener", "focus", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		addr.Call("select")
		return nil
	}))
	goBtn := btn("Go", "padding:2px 8px")
	for _, el := range []js.Value{back, fwd, reload, addr, goBtn} {
		bar.Call("appendChild", el)
	}

	views = mk("div")
	views.Get("style").Set("cssText", "position:relative;flex:1;min-height:0")

	root.Call("appendChild", strip)
	root.Call("appendChild", bar)
	root.Call("appendChild", views)

	onClick(goBtn, func() {
		if t := cur(); t != nil {
			navigate(t, addr.Get("value").String())
		}
	})
	onClick(reload, func() {
		if t := cur(); t != nil {
			load(t, t.hist[t.pos])
		}
	})
	onClick(back, func() {
		if t := cur(); t != nil && t.pos > 0 {
			t.pos--
			load(t, t.hist[t.pos])
			syncNav()
		}
	})
	onClick(fwd, func() {
		if t := cur(); t != nil && t.pos < len(t.hist)-1 {
			t.pos++
			load(t, t.hist[t.pos])
			syncNav()
		}
	})
	addr.Call("addEventListener", "keydown", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) == 0 {
			return nil
		}
		switch a[0].Get("key").String() {
		case "Enter":
			if t := cur(); t != nil {
				navigate(t, addr.Get("value").String())
				addr.Call("blur")
			}
		case "Escape":
			// Put back what the tab is actually showing, as browsers do.
			if t := cur(); t != nil {
				addr.Set("value", t.hist[t.pos])
			}
			addr.Call("blur")
		}
		return nil
	}))

	// The keyboard shortcuts a browser is expected to answer to. Bound on the
	// browser's own root, not the document: several of these may be running on
	// one page (a desk can open more than one), and each should only respond to
	// keys pressed inside itself.
	root.Set("tabIndex", -1)
	root.Call("addEventListener", "keydown", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) == 0 {
			return nil
		}
		e := a[0]
		ctrl := e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool()
		key := strings.ToLower(e.Get("key").String())
		stop := func() { e.Call("preventDefault"); e.Call("stopPropagation") }
		switch {
		case ctrl && key == "t":
			stop()
			addTab(home())
		case ctrl && key == "w":
			stop()
			closeTab(active)
		case ctrl && key == "l":
			stop()
			addr.Call("focus")
		case ctrl && key == "r", key == "f5":
			stop()
			if t := cur(); t != nil {
				load(t, t.hist[t.pos])
			}
		case ctrl && key == "tab":
			stop()
			if n := len(tabs); n > 1 {
				activate((active + 1) % n)
			}
		case key == "escape":
			if t := cur(); t != nil && t.loading {
				stop()
				setLoading(t, false)
			}
		}
		return nil
	}))

	// The relay: a sandboxed page's navShim posts navigation intent here.
	js.Global().Call("addEventListener", "message", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) == 0 {
			return nil
		}
		data := a[0].Get("data")
		if data.Type() != js.TypeObject {
			return nil
		}
		if nav := data.Get("shipyardNav"); nav.Truthy() {
			if t := cur(); t != nil {
				navigate(t, nav.String())
			}
		}
		if fr := data.Get("shipyardFetch"); fr.Truthy() {
			relayResource(a[0].Get("source"), fr.Get("id"), fr.Get("url").String())
		}
		return nil
	}))

	addTab(home())
}

// Navigate loads url in the ACTIVE tab, recording it in that tab's history —
// exactly what typing it in the address bar and pressing Go does. A no-op
// before Open has mounted the browser or when no tab exists.
//
// It exists for hosts that drive the browser programmatically: a desk that
// opens a browser window already pointed at a page (the skywire desk points
// one at the hypervisor UI on its virtual loopback) needs an entry point that
// is not a click.
func Navigate(url string) {
	if doc.IsUndefined() || active < 0 || active >= len(tabs) {
		return
	}
	navigate(tabs[active], url)
}

// NewTab opens url in a new tab. With background true the current tab keeps
// focus — the browser-style "open in background tab" a host uses to preload
// secondary pages behind the one the user is looking at. A no-op before Open.
func NewTab(url string, background bool) {
	if doc.IsUndefined() {
		return
	}
	prev := active
	addTab(url)
	if background && prev >= 0 && prev < len(tabs) {
		activate(prev)
	}
}
