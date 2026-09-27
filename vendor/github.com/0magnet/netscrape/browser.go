//go:build js && wasm

package netscrape

import (
	"encoding/base64"
	"strings"
	"syscall/js"
)

// home is the page a new tab opens. A host that has something of its own to
// show — a demo site served beside the browser, a mesh index — sets
// globalThis.__netscrapeStart to its URL; everyone else gets the built-in
// page, which is what this did before there was a way to say otherwise.
func home() string {
	if v := js.Global().Get("__netscrapeStart"); v.Type() == js.TypeString && v.String() != "" {
		return v.String()
	}
	return startURL
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

// browser holds one window's worth of chrome state: the DOM elements, the
// tab list, which tab is active, and the tab currently mid-drag. A desk can
// open more than one browser window on one page (see Open's root keydown
// comment), so this used to be a single set of package globals that every
// open() call reassigned to the newest window — switching tabs in one window
// then hid another window's iframes (activate walked the shared tab list),
// and closing the newer window left the survivor's tab switches writing into
// a shared address bar the closed window still pointed at. Every per-window
// handler now closes over its own *browser instead.
type browser struct {
	root                                       js.Value // the mounted element; Get("isConnected") says whether this window is still open
	doc, strip, views, addr, back, fwd, reload js.Value
	tabs                                       []*tab
	active                                     int
	dragging                                   *tab // the tab being dragged across the strip, nil between drags
}

// browsers lists every window Open has built, oldest first.
var browsers []*browser

// registerBrowser adds b as the newest window and drops any earlier ones that
// have since closed, so the list stays no bigger than the windows actually
// open. See currentBrowser.
func registerBrowser(b *browser) {
	live := browsers[:0]
	for _, x := range browsers {
		if x.root.Truthy() && x.root.Get("isConnected").Bool() {
			live = append(live, x)
		}
	}
	browsers = append(live, b)
}

// currentBrowser is the most recently opened window that is still on the
// page, or nil when none is. Navigate, NewTab and TabStrip take no window
// argument, so this is what they act on: the newest LIVE one, never a closed
// one — a caller that means a particular window has to reach it some other
// way (skywire's desk gets there via each window's own DOM subtree instead).
func currentBrowser() *browser {
	for i := len(browsers) - 1; i >= 0; i-- {
		if browsers[i].root.Truthy() && browsers[i].root.Get("isConnected").Bool() {
			return browsers[i]
		}
	}
	return nil
}

type tab struct {
	br                   *browser // the window this tab belongs to
	btn, lbl, ico, frame js.Value
	hist                 []string
	pos                  int
	title                string // the page's own <title>, when it has one
	loading              bool
	// directSrc is the frame src this browser last set for a natively
	// rendered page. A frame that reports a DIFFERENT URL navigated itself —
	// the reader clicked a link inside it — which is the only way to notice,
	// since that navigation never passes through this browser. See watchDirect.
	directSrc string
	// directNavWired guards the one-time load listener that notices it.
	directNavWired bool
}

// isFront reports whether t is the tab its own window currently has in
// front, so a handler knows whether it should touch that window's address
// bar rather than some other tab's.
func (t *tab) isFront() bool {
	return t.br != nil && t.br.active >= 0 && t.br.tabs[t.br.active] == t
}

func (b *browser) mk(tag string) js.Value { return b.doc.Call("createElement", tag) }

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

func (b *browser) btn(label, style string) js.Value {
	el := b.mk("button")
	el.Set("textContent", label)
	el.Get("style").Set("cssText", "background:#2a2342;color:#cdd2da;border:1px solid #3a3352;cursor:pointer;font:13px monospace;"+style)
	return el
}

// labelFor is a tab's name: the host, which is the part a person recognizes.
// A generated page has no host worth showing, so it gets a plain word instead.
func labelFor(url string) string {
	switch {
	case url == startURL, strings.HasPrefix(url, "data:"):
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

// OriginLoader lets a host claim a URL for rendering on a REAL, ISOLATED
// ORIGIN the host mints per site: a page with genuine cookies, storage, a
// service worker, a secure context and native subresource loading, instead of
// one transcoded into a sandboxed srcdoc. Everything DirectLoader does not
// claim is offered here before the transcoder gets it.
//
// It is ASYNCHRONOUS where DirectLoader is not, because minting the origin is
// a round trip inside the host — skywire hashes the target to a
// content-addressed label so one wildcard certificate covers every site — so
// it returns a Promise of the src rather than the src. A rejection is not
// fatal: the URL falls back to the transcoder, which is what the reader had
// before any of this.
//
// The claimed frame is CROSS-ORIGIN, and that is the point — it cannot reach
// this page's storage or the host's identity key. Two consequences are
// deliberate rather than missing: this browser cannot read the frame's title,
// so a claimed tab is named after its URL, and it cannot read the frame's
// location, so a link the reader clicks INSIDE the page does not reach the
// tab's history the way watchDirectNav records one for a same-origin page.
// Recovering those needs the origin to report its own navigations; it is not
// something the embedder can read out of a cross-origin frame.
var OriginLoader func(url string) (promise js.Value, ok bool)

// DirectAddress is the inverse of DirectLoader, for DISPLAY. A frame the host
// claimed navigates itself to more of the host's served URLs, and those are
// the rewritten form — skywire's "<origin>/vnet/<port>/path" — which is an
// implementation detail of how the host serves the page, not an address anyone
// typed or could type. Showing it in the address bar (and recording it in
// history) told the reader a path that does not exist as far as the mesh is
// concerned.
//
// A host that rewrites URLs should map one back to the address it stands for
// ("http://vnet:<port>/path"). Returning ok=false, or leaving this nil, keeps
// the served URL, which is right for a host that does not rewrite.
var DirectAddress func(src string) (url string, ok bool)

// displayURL is DirectAddress with the identity fallback.
func displayURL(src string) string {
	if DirectAddress != nil {
		if u, ok := DirectAddress(src); ok && u != "" {
			return u
		}
	}
	return src
}

// load renders a URL into a tab's iframe. A data:/about:/blob: URL goes straight
// to the iframe; anything else — an http(s) page, or a bare host like
// example.com or home.dmsg — goes through the transport (fetchPage), which lets
// the host's fetchVia decide clearnet vs mesh, unless DirectLoader claims it
// for native rendering. A scheme-less address is normalized to http:// so the
// transport always gets a URL.
func load(t *tab, url string) {
	// Name the tab after where it is. "tab 3" tells a person nothing once
	// three of them are open; the host is what they recognize, and it fits in
	// a strip where a whole URL never would.
	if t != nil && t.lbl.Truthy() {
		t.lbl.Set("textContent", labelFor(url))
	}
	if url == startURL {
		renderStart(t)
		if t.isFront() {
			t.br.addr.Set("value", "")
		}
		return
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
				t.directSrc = src
				t.frame.Set("src", src)
				watchDirectNav(t)
				// A natively rendered page is same-origin, so its title and
				// icon can simply be read once it has loaded — no transcoding
				// pass to pick them out of.
				watchDirect(t, url)
				if t.isFront() {
					t.br.addr.Set("value", url)
				}
				return
			}
		}
		if OriginLoader != nil {
			if p, ok := OriginLoader(url); ok && p.Truthy() && p.Get("then").Type() == js.TypeFunction {
				loadOrigin(t, url, p)
				return
			}
		}
		fetchPage(t, url)
	}
	if t.isFront() {
		t.br.addr.Set("value", url)
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

func (b *browser) activate(i int) {
	if i < 0 || i >= len(b.tabs) {
		return
	}
	b.active = i
	for j, t := range b.tabs {
		on := j == i
		if on {
			t.frame.Get("style").Set("display", "block")
			t.btn.Get("style").Set("background", "#2a2342")
		} else {
			t.frame.Get("style").Set("display", "none")
			t.btn.Get("style").Set("background", "transparent")
		}
	}
	b.addr.Set("value", addrText(b.tabs[i].hist[b.tabs[i].pos]))
	b.syncNav()
}

// syncNav greys the history buttons when there is nowhere to go, so they say
// whether they will do anything before they are pressed.
func (b *browser) syncNav() {
	t := b.cur()
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
	set(b.back, t != nil && t.pos > 0)
	set(b.fwd, t != nil && t.pos < len(t.hist)-1)
}

// cur is the tab in front, or nil when this window has not opened one yet.
func (b *browser) cur() *tab {
	if b.active >= 0 && b.active < len(b.tabs) {
		return b.tabs[b.active]
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
	if t == t.br.cur() && t.br.reload.Truthy() {
		if on {
			t.br.reload.Set("textContent", "×")
			t.br.reload.Set("title", "stop")
		} else {
			t.br.reload.Set("textContent", "⟳")
			t.br.reload.Set("title", "reload")
		}
	}
}

// setTitle names a tab after the page. A page's own <title> is what a person
// recognizes; the host is the fallback for a page that has none.
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
			// Reject and let onErr do the releasing. Calling done() here first
			// freed onErr and then handed the rejection straight to it, so the
			// catch invoked a js.Func that no longer existed — Go reports that
			// as "call to released function". A site without a favicon takes
			// this path on every page load, which is most of them.
			return g.Get("Promise").Call("reject")
		}
		if h := a[0].Get("headers").Call("get", "content-type"); h.Truthy() {
			ct = h.String()
		}
		// A favicon is an image. A same-origin fallback that answers /favicon.ico
		// with an HTML page (a single-page app's catch-all) is not one, and
		// showing it as the icon painted a broken image in the tab.
		if !strings.HasPrefix(strings.ToLower(ct), "image/") {
			return g.Get("Promise").Call("reject")
		}
		return a[0].Call("arrayBuffer")
	})
	fetchVia(iconURL).Call("then", onResp).Call("then", onBuf).Call("catch", onErr)
}

// watchDirectNav records navigations the FRAME makes on its own.
//
// A natively rendered page is loaded by setting frame.src and is then its own
// browsing context: a link the reader clicks inside it navigates that frame
// directly, without passing through this browser at all. So the tab's history
// never grew, Back stayed greyed out for the whole visit, and a reader who had
// walked several pages deep into a doc tree had no way back — the one place
// the button is most obviously wanted.
//
// The frame is same-origin by construction (DirectLoader only claims URLs the
// host serves), so its location is readable, and every navigation fires load.
// Anything that does not match the src this browser set is the frame moving
// itself, and gets recorded as an ordinary history entry — replaying it later
// works because DirectLoader claims the served form too.
//
// Wired once per tab. Reads can throw if the host sent the frame somewhere
// cross-origin after all; that costs a history entry, not the tab.
func watchDirectNav(t *tab) {
	if t == nil || t.directNavWired || !t.frame.Truthy() {
		return
	}
	t.directNavWired = true
	t.frame.Call("addEventListener", "load", js.FuncOf(func(js.Value, []js.Value) any {
		defer func() { recover() }() //nolint:errcheck // a cross-origin read is not fatal
		w := t.frame.Get("contentWindow")
		if !w.Truthy() {
			return nil
		}
		href := w.Get("location").Get("href")
		if href.Type() != js.TypeString {
			return nil
		}
		cur := href.String()
		if cur == "" || cur == "about:blank" || cur == t.directSrc {
			return nil
		}
		// Also not a self-navigation when it matches where history already
		// says we are: Back and Forward re-set the src, and a DirectLoader that
		// hands back a different spelling than it was given would otherwise
		// push a duplicate on every press and make Back walk in place.
		if t.pos >= 0 && t.pos < len(t.hist) && (cur == t.hist[t.pos] || displayURL(cur) == t.hist[t.pos]) {
			t.directSrc = cur
			return nil
		}
		// The frame moved itself. Record it WITHOUT reloading: the page the
		// entry names is already on screen. What is recorded and shown is the
		// ADDRESS the served URL stands for — see DirectAddress.
		t.directSrc = cur
		shown := displayURL(cur)
		if t.pos >= 0 && t.pos < len(t.hist)-1 {
			t.hist = t.hist[:t.pos+1]
		}
		t.hist = append(t.hist, shown)
		t.pos = len(t.hist) - 1
		t.lbl.Set("textContent", labelFor(shown))
		if t.isFront() {
			t.br.addr.Set("value", shown)
		}
		t.br.syncNav()
		return nil
	}))
}

// watchDirect reads a natively rendered page's title and icon once it has
// loaded. It is same-origin by construction (DirectLoader only claims URLs the
// host serves), so the document is simply readable — but a single-page app
// paints its real title a beat after load, so the title is re-read once more
// shortly after. Any access can still throw if the host redirected the frame
// somewhere cross-origin; that just means no title, not a broken tab.
func watchDirect(t *tab, url string) {
	read := func() {
		defer func() { recover() }() //nolint:errcheck // deliberate: a throw here is not fatal
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
	defer func() { recover() }() //nolint:errcheck // deliberate: a throw here is not fatal // a malformed base throws; no icon is fine
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

// indexOf finds a tab's CURRENT position in its own window. Handlers close
// over the tab itself rather than the index it happened to have when it was
// created: indices shift every time a tab closes, and the old fix for that —
// cloning the button to drop its listeners — silently detached the label and
// icon nodes the tab still held references to, so a tab could never update
// its own title again.
func indexOf(t *tab) int {
	if t == nil || t.br == nil {
		return -1
	}
	for i, x := range t.br.tabs {
		if x == t {
			return i
		}
	}
	return -1
}

func (b *browser) addTab(url string) {
	t := &tab{br: b}
	t.btn = b.mk("div")
	t.btn.Get("style").Set("cssText", "display:flex;align-items:center;gap:.4em;max-width:12em;padding:.25em .6em;cursor:pointer;font:11px monospace;color:#cdd2da;border:1px solid #2a2342;border-bottom:0;border-radius:5px 5px 0 0;white-space:nowrap")
	// The favicon sits where every browser puts it, and holds its space from
	// the start so a tab does not jump sideways when the icon arrives.
	t.ico = b.mk("img")
	t.ico.Get("style").Set("cssText", "width:12px;height:12px;flex:0 0 12px;object-fit:contain;visibility:hidden")
	t.lbl = b.mk("span")
	t.lbl.Set("textContent", "new tab")
	t.lbl.Get("style").Set("cssText", "overflow:hidden;text-overflow:ellipsis;white-space:nowrap")
	x := b.mk("span")
	x.Set("textContent", "×")
	x.Get("style").Set("cssText", "opacity:.6;flex:0 0 auto")
	t.btn.Call("appendChild", t.ico)
	t.btn.Call("appendChild", t.lbl)
	t.btn.Call("appendChild", x)

	t.frame = b.mk("iframe")
	t.frame.Get("style").Set("cssText", "position:absolute;inset:0;width:100%;height:100%;border:0;background:#fff;display:none")
	if len(b.tabs) == 0 {
		t.frame.Set("id", "browser-frame") // the first tab's frame, for harnesses
	}

	onClick(t.btn, func() { b.activate(indexOf(t)) })
	onClick(x, func() { b.closeTab(indexOf(t)) })
	wireTabDrag(t)
	// Middle-click closes a tab, as everywhere else.
	t.btn.Call("addEventListener", "auxclick", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 && a[0].Get("button").Int() == 1 {
			a[0].Call("preventDefault")
			b.closeTab(indexOf(t))
		}
		return nil
	}))

	b.strip.Call("insertBefore", t.btn, b.strip.Get("lastChild")) // before the + button
	b.views.Call("appendChild", t.frame)
	b.tabs = append(b.tabs, t)
	navigate(t, url)
	b.activate(indexOf(t))
	// A new tab is for typing an address into, so the bar takes the cursor.
	if url == startURL {
		b.addr.Call("focus")
	}
}

func (b *browser) closeTab(i int) {
	if i < 0 || i >= len(b.tabs) || len(b.tabs) <= 1 {
		return // keep at least one tab
	}
	t := b.tabs[i]
	t.btn.Call("remove")
	t.frame.Call("remove")
	b.tabs = append(b.tabs[:i], b.tabs[i+1:]...)
	if b.active >= len(b.tabs) {
		b.active = len(b.tabs) - 1
	}
	b.activate(b.active)
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
//
// Each call builds its own *browser and registers it as the current one (see
// currentBrowser) — a desk that opens several of these windows gets several
// independent instances, none of them able to reach into another's tabs.
func Open(root js.Value) {
	if !root.Truthy() {
		return
	}
	b := &browser{root: root, active: -1}
	b.doc = js.Global().Get("document")
	registerBrowser(b)

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

	b.strip = b.mk("div")
	b.strip.Get("style").Set("cssText", "display:flex;gap:2px;align-items:flex-end;background:#100d18;border-bottom:1px solid #2a2342;padding:3px 3px 0;min-height:24px")
	plus := b.btn("+", "padding:.2em .55em;border-radius:5px 5px 0 0")
	onClick(plus, func() { b.addTab(home()) })
	b.strip.Call("appendChild", plus)

	bar := b.mk("div")
	bar.Get("style").Set("cssText", "display:flex;gap:4px;padding:4px;background:#100d18;border-bottom:1px solid #2a2342")
	b.back, b.fwd, b.reload = b.btn("◀", "padding:2px 8px"), b.btn("▶", "padding:2px 8px"), b.btn("⟳", "padding:2px 8px")
	b.back.Set("title", "back")
	b.fwd.Set("title", "forward")
	b.reload.Set("title", "reload")
	b.addr = b.mk("input")
	b.addr.Set("spellcheck", false)
	b.addr.Set("placeholder", "search or enter address")
	b.addr.Get("style").Set("cssText", "flex:1;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:2px 8px;font:13px monospace")
	// Clicking the address bar selects the whole URL, so typing replaces it
	// rather than landing in the middle of what is already there.
	b.addr.Call("addEventListener", "focus", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		b.addr.Call("select")
		return nil
	}))
	goBtn := b.btn("Go", "padding:2px 8px")
	proxyBtn, proxyRow := b.proxyPanel()
	for _, el := range []js.Value{b.back, b.fwd, b.reload, b.addr, goBtn, proxyBtn} {
		bar.Call("appendChild", el)
	}

	b.views = b.mk("div")
	b.views.Get("style").Set("cssText", "position:relative;flex:1;min-height:0")

	root.Call("appendChild", b.strip)
	root.Call("appendChild", bar)
	root.Call("appendChild", proxyRow)
	root.Call("appendChild", b.views)

	onClick(goBtn, func() {
		if t := b.cur(); t != nil {
			navigate(t, b.addr.Get("value").String())
		}
	})
	onClick(b.reload, func() {
		if t := b.cur(); t != nil {
			load(t, t.hist[t.pos])
		}
	})
	onClick(b.back, func() {
		if t := b.cur(); t != nil && t.pos > 0 {
			t.pos--
			load(t, t.hist[t.pos])
			b.syncNav()
		}
	})
	onClick(b.fwd, func() {
		if t := b.cur(); t != nil && t.pos < len(t.hist)-1 {
			t.pos++
			load(t, t.hist[t.pos])
			b.syncNav()
		}
	})
	b.addr.Call("addEventListener", "keydown", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) == 0 {
			return nil
		}
		switch a[0].Get("key").String() {
		case "Enter":
			if t := b.cur(); t != nil {
				navigate(t, b.addr.Get("value").String())
				b.addr.Call("blur")
			}
		case "Escape":
			// Put back what the tab is actually showing, as browsers do.
			if t := b.cur(); t != nil {
				b.addr.Set("value", addrText(t.hist[t.pos]))
			}
			b.addr.Call("blur")
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
			b.addTab(home())
		case ctrl && key == "w":
			stop()
			b.closeTab(b.active)
		case ctrl && key == "l":
			stop()
			b.addr.Call("focus")
		case ctrl && key == "r", key == "f5":
			stop()
			if t := b.cur(); t != nil {
				load(t, t.hist[t.pos])
			}
		case ctrl && key == "tab":
			stop()
			if n := len(b.tabs); n > 1 {
				b.activate((b.active + 1) % n)
			}
		case key == "escape":
			if t := b.cur(); t != nil && t.loading {
				stop()
				setLoading(t, false)
			}
		}
		return nil
	}))

	// The relay: a sandboxed page's navShim posts navigation intent here. This
	// listens on the window, which every browser instance shares, so it acts
	// on THIS window's own current tab rather than a package-level one — the
	// bug this file used to have.
	js.Global().Call("addEventListener", "message", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) == 0 {
			return nil
		}
		data := a[0].Get("data")
		if data.Type() != js.TypeObject {
			return nil
		}
		if nav := data.Get("shipyardNav"); nav.Truthy() {
			if t := b.cur(); t != nil {
				navigate(t, nav.String())
			}
		}
		if fr := data.Get("shipyardFetch"); fr.Truthy() {
			relayResource(a[0].Get("source"), fr.Get("id"), fr.Get("url").String())
		}
		return nil
	}))

	b.addTab(home())
}

// Navigate loads url in the ACTIVE tab, recording it in that tab's history —
// exactly what typing it in the address bar and pressing Go does. A no-op
// before Open has mounted a browser or when no tab exists.
//
// It exists for hosts that drive the browser programmatically: a desk that
// opens a browser window already pointed at a page (the skywire desk points
// one at the hypervisor UI on its virtual loopback) needs an entry point that
// is not a click.
//
// Takes no window: it acts on currentBrowser, the most recently opened window
// still on the page. A host driving a specific window among several has no
// way to say which through this call.
func Navigate(url string) {
	b := currentBrowser()
	if b == nil || b.active < 0 || b.active >= len(b.tabs) {
		return
	}
	navigate(b.tabs[b.active], url)
}

// NewTab opens url in a new tab. With background true the current tab keeps
// focus — the browser-style "open in background tab" a host uses to preload
// secondary pages behind the one the user is looking at. A no-op before Open.
//
// The first host-driven tab REPLACES the start page rather than joining it.
// Open has to show something, so it opens the built-in page; a host that then
// names its own first page (the visor desk opens its hypervisor UI) was left
// with a "new tab" nobody asked for sitting first in the strip. The
// replacement happens only while that tab is still the untouched start page —
// one tab, no history — so a person who has begun using it keeps it.
//
// Takes no window, for the same reason as Navigate: it acts on currentBrowser.
func NewTab(url string, background bool) {
	b := currentBrowser()
	if b == nil {
		return
	}
	if len(b.tabs) == 1 && isUntouchedStart(b.tabs[0]) {
		navigate(b.tabs[0], url)
		b.activate(0)
		return
	}
	prev := b.active
	b.addTab(url)
	if background && prev >= 0 && prev < len(b.tabs) {
		b.activate(prev)
	}
}

// isUntouchedStart reports whether t is still the tab Open created and nobody
// has used: its only history entry is the start page.
func isUntouchedStart(t *tab) bool {
	return t != nil && len(t.hist) == 1 && t.pos == 0 && t.hist[0] == home()
}

// TabStrip returns the tab strip element once Open has built it, so a host can
// move it out of the browser's own box — into its window's title bar, where a
// browser keeps its tabs, level with the window controls. The strip keeps
// working wherever it lives: tabs are wired to their frames, not to their
// parent. Undefined before Open.
//
// Takes no window, for the same reason as Navigate: it acts on currentBrowser,
// which is right for the caller in practice — a host calls this once, right
// after the Open call that just created the window it wants the strip for.
func TabStrip() js.Value {
	if b := currentBrowser(); b != nil {
		return b.strip
	}
	return js.Value{}
}

// wireTabDrag lets a tab be picked up and dropped on another to reorder them,
// the way every browser's strip works. HTML5 drag events, not pointer math:
// the strip may live in a window's title bar whose own pointer handler moves
// the window, and a native drag does not start one of those.
func wireTabDrag(t *tab) {
	t.btn.Set("draggable", true)
	t.btn.Call("addEventListener", "dragstart", js.FuncOf(func(_ js.Value, a []js.Value) any {
		t.br.dragging = t
		if len(a) > 0 {
			if dt := a[0].Get("dataTransfer"); dt.Truthy() {
				dt.Set("effectAllowed", "move")
				// Some engines cancel the drag with an empty payload.
				dt.Call("setData", "text/plain", labelFor(t.hist[t.pos]))
			}
		}
		return nil
	}))
	t.btn.Call("addEventListener", "dragover", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if t.br.dragging != nil && t.br.dragging != t && len(a) > 0 {
			a[0].Call("preventDefault") // allow the drop
			if dt := a[0].Get("dataTransfer"); dt.Truthy() {
				dt.Set("dropEffect", "move")
			}
		}
		return nil
	}))
	t.btn.Call("addEventListener", "drop", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 {
			a[0].Call("preventDefault")
		}
		if t.br.dragging != nil && t.br.dragging != t {
			t.br.moveTab(indexOf(t.br.dragging), indexOf(t))
		}
		t.br.dragging = nil
		return nil
	}))
	t.btn.Call("addEventListener", "dragend", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		t.br.dragging = nil
		return nil
	}))
}

// moveTab moves the tab at from to position to, in the slice and in the strip,
// keeping the active tab active.
func (b *browser) moveTab(from, to int) {
	if from < 0 || to < 0 || from >= len(b.tabs) || to >= len(b.tabs) || from == to {
		return
	}
	cur := b.tabs[b.active]
	t := b.tabs[from]
	b.tabs = append(b.tabs[:from], b.tabs[from+1:]...)
	rest := append([]*tab{}, b.tabs[to:]...)
	b.tabs = append(append(b.tabs[:to], t), rest...)
	// Re-place the button before the one now following it, or before the +
	// button when it moved to the end.
	if to+1 < len(b.tabs) {
		b.strip.Call("insertBefore", t.btn, b.tabs[to+1].btn)
	} else {
		b.strip.Call("insertBefore", t.btn, b.strip.Get("lastChild"))
	}
	b.active = indexOf(cur)
}

// Proxy setting. netscrape does not carry traffic itself — the host's
// __netscrapeFetch does — so the proxy control is a published preference the
// host reads: globalThis.__netscrapeProxy = {proxy}. proxy is one
// [scheme://]host:port, the address of a SOCKS5 (default scheme) or HTTP proxy
// every page goes through, as a browser's proxy setting is. A host running in
// a browser maps a loopback address (vnet:<port>, localhost:<port>) to its own
// virtual-loopback port and hands anything else to the visor behind the page
// to dial.
//
// The host names its default in globalThis.__netscrapeDefaultProxy before the
// browser opens — a skywire desk names its resolving proxy, the one a native
// browser beside a visor is pointed at. The field always shows the address in
// effect: the person's choice, or that default when they have made none.
// Clearing the field returns to the default. The choice persists in
// localStorage so it survives a reload.
//
// This one preference is genuinely process-wide, not per-window: it is
// published under one globalThis key and stored under one localStorage key,
// so every browser window's proxy panel reads and writes the same setting —
// unlike the per-window chrome state above, there is no "which window's
// proxy" question to get wrong.
const proxyStoreKey = "netscrape.proxy"

// proxyAddr is the person's choice; "" means the host's default.
var proxyAddr = ""

// defaultProxy is the host's default, read from
// globalThis.__netscrapeDefaultProxy.
func defaultProxy() string {
	if d := js.Global().Get("__netscrapeDefaultProxy"); d.Type() == js.TypeString {
		return strings.TrimSpace(d.String())
	}
	return ""
}

// Proxy reports the proxy in effect: the person's choice, else the host's
// default, else "" when the host names none.
func Proxy() string {
	if proxyAddr != "" {
		return proxyAddr
	}
	return defaultProxy()
}

func loadProxy() {
	defer publishProxy()
	ls := js.Global().Get("localStorage")
	if !ls.Truthy() {
		return
	}
	raw := ls.Call("getItem", proxyStoreKey)
	if raw.Type() != js.TypeString || raw.String() == "" {
		return
	}
	obj := js.Global().Get("JSON").Call("parse", raw.String())
	if p := obj.Get("proxy"); p.Type() == js.TypeString {
		proxyAddr = strings.TrimSpace(p.String())
	}
	// An older {mode, exit} value is dropped: the address field replaced it.
}

func saveProxy() {
	if ls := js.Global().Get("localStorage"); ls.Truthy() {
		v := js.Global().Get("Object").New()
		v.Set("proxy", proxyAddr)
		ls.Call("setItem", proxyStoreKey, js.Global().Get("JSON").Call("stringify", v).String())
	}
	publishProxy()
}

func publishProxy() {
	obj := js.Global().Get("Object").New()
	obj.Set("proxy", Proxy())
	js.Global().Set("__netscrapeProxy", obj)
}

// validProxyAddr reports whether s is "[scheme://]host:port" with a numeric
// port; scheme, when present, is socks5, socks5h, http or https.
func validProxyAddr(s string) bool {
	if s == "" {
		return true
	}
	if i := strings.Index(s, "://"); i >= 0 {
		switch strings.ToLower(s[:i]) {
		case "socks5", "socks5h", "http", "https":
		default:
			return false
		}
		s = s[i+3:]
	}
	j := strings.LastIndex(s, ":")
	if j <= 0 || j == len(s)-1 {
		return false
	}
	for _, c := range s[j+1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// proxyPanel builds the ⚙ button and the settings row it toggles: one field,
// the proxy address clearnet pages go through. Returns the button (for the
// address bar) and the panel (for below it).
func (b *browser) proxyPanel() (button, panel js.Value) {
	button = b.btn("⚙", "padding:2px 8px")
	button.Set("title", "proxy settings")
	panel = b.mk("div")
	panel.Get("style").Set("cssText", "display:none;gap:8px;align-items:center;padding:4px 6px;background:#100d18;border-bottom:1px solid #2a2342;font:12px monospace;color:#cdd2da")
	label := b.mk("span")
	label.Set("textContent", "proxy")
	addr := b.mk("input")
	addr.Set("spellcheck", false)
	addr.Set("placeholder", "[scheme://]host:port — e.g. vnet:4445 or socks5://192.168.1.2:1080; empty returns to the default")
	addr.Get("style").Set("cssText", "flex:1;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:1px 6px;font:12px monospace")
	status := b.mk("span")
	status.Get("style").Set("cssText", "opacity:.7")
	sync := func() {
		addr.Set("value", Proxy())
		switch {
		case !validProxyAddr(Proxy()):
			status.Set("textContent", "needs [scheme://]host:port")
		case proxyAddr == "" && Proxy() != "":
			status.Set("textContent", "default")
		case Proxy() == "":
			status.Set("textContent", "no proxy")
		default:
			status.Set("textContent", "")
		}
	}
	addr.Call("addEventListener", "change", js.FuncOf(func(_ js.Value, _ []js.Value) any {
		proxyAddr = strings.TrimSpace(addr.Get("value").String())
		// The default typed back in is the default: keep following it.
		if proxyAddr == defaultProxy() {
			proxyAddr = ""
		}
		saveProxy()
		sync()
		return nil
	}))
	onClick(button, func() {
		if panel.Get("style").Get("display").String() == "none" {
			sync() // another window may have changed it
			panel.Get("style").Set("display", "flex")
		} else {
			panel.Get("style").Set("display", "none")
		}
	})
	for _, el := range []js.Value{label, addr, status} {
		panel.Call("appendChild", el)
	}
	loadProxy()
	sync()
	return button, panel
}

// loadOrigin renders a URL the host claimed for a real isolated origin. The
// host's promise resolves to the src to put on the frame; this awaits it, drops
// the transcoder's sandbox and hands the whole load to the browser.
//
// A rejection — or a resolve with nothing — falls back to the transcoder rather
// than erroring the tab. A site the host could not mint an origin for is still
// a site the reader asked to see, and the sandboxed render is exactly what they
// would have got before the real-origin path existed. The one thing that must
// not happen is a blank tab.
//
// The address bar keeps the address the reader typed, never the minted one:
// the origin is a content-addressed hash, an implementation detail of how the
// host serves the page, and nobody can type it or would recognize it.
func loadOrigin(t *tab, url string, p js.Value) {
	setLoading(t, true)
	var onOK, onErr js.Func
	release := func() {
		onOK.Release()
		onErr.Release()
	}
	onOK = js.FuncOf(func(_ js.Value, a []js.Value) any {
		defer release()
		src := ""
		if len(a) > 0 && a[0].Type() == js.TypeString {
			src = a[0].String()
		}
		if src == "" {
			fetchPage(t, url)
			return nil
		}
		t.frame.Call("removeAttribute", "srcdoc")
		// Unsandboxed, like the DirectLoader path: the isolation here comes
		// from the frame being a different ORIGIN, which is stronger than the
		// sandbox and, unlike it, leaves the platform intact.
		t.frame.Call("removeAttribute", "sandbox")
		t.directSrc = src
		t.frame.Set("src", src)
		// Named from the URL: the frame's own <title> is unreadable across the
		// origin boundary. watchDirectNav is deliberately NOT wired for the
		// same reason — it reads contentWindow.location, which throws here.
		setTitle(t, "", url)
		setLoading(t, false)
		if t.isFront() {
			t.br.addr.Set("value", url)
		}
		return nil
	})
	onErr = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer release()
		fetchPage(t, url)
		return nil
	})
	p.Call("then", onOK).Call("catch", onErr)
}
