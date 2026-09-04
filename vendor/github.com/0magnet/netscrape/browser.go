//go:build js && wasm

package netscrape

import (
	"encoding/base64"
	"strconv"
	"strings"
	"syscall/js"
)

const startPage = "data:text/html,<body style='font-family:sans-serif;padding:2em'>" +
	"<h1>A browser, in Go</h1><p>The chrome is syscall/js. Each tab is an iframe. " +
	"Use + for a new tab, or type a URL above.</p></body>"

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
	tabs   []*tab
	active = -1
	nextID int
)

type tab struct {
	btn, lbl, frame js.Value
	hist            []string
	pos             int
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

// load renders a URL into a tab's iframe. A data:/about:/blob: URL goes straight
// to the iframe; anything else — an http(s) page, or a bare host like
// example.com or home.dmsg — goes through the transport (fetchPage), which lets
// the host's fetchVia decide clearnet vs mesh. A scheme-less address is
// normalised to http:// so the transport always gets a URL.
func load(t *tab, url string) {
	if strings.HasPrefix(url, "data:") || strings.HasPrefix(url, "about:") || strings.HasPrefix(url, "blob:") {
		t.frame.Call("removeAttribute", "srcdoc")
		t.frame.Set("src", url)
	} else {
		if !strings.Contains(url, "://") {
			url = "http://" + url
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
	fail := func(msg string) {
		t.frame.Call("removeAttribute", "src")
		t.frame.Set("srcdoc", "<body style='font:14px sans-serif;padding:2em;color:#a33'>"+msg+"</body>")
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

func addTab(url string) {
	nextID++
	t := &tab{}
	t.btn = mk("div")
	t.btn.Get("style").Set("cssText", "display:flex;align-items:center;gap:.4em;max-width:12em;padding:.25em .6em;cursor:pointer;font:11px monospace;color:#cdd2da;border:1px solid #2a2342;border-bottom:0;border-radius:5px 5px 0 0;white-space:nowrap")
	t.lbl = mk("span")
	t.lbl.Set("textContent", "tab "+strconv.Itoa(nextID))
	x := mk("span")
	x.Set("textContent", "×")
	x.Get("style").Set("cssText", "opacity:.6")
	t.btn.Call("appendChild", t.lbl)
	t.btn.Call("appendChild", x)

	t.frame = mk("iframe")
	t.frame.Get("style").Set("cssText", "position:absolute;inset:0;width:100%;height:100%;border:0;background:#fff;display:none")
	if len(tabs) == 0 {
		t.frame.Set("id", "browser-frame") // the first tab's frame, for harnesses
	}

	idx := len(tabs)
	onClick(t.btn, func() { activate(idx) })
	onClick(x, func() { closeTab(idx) })

	strip.Call("insertBefore", t.btn, strip.Get("lastChild")) // before the + button
	views.Call("appendChild", t.frame)
	tabs = append(tabs, t)
	navigate(t, url)
	activate(idx)
}

func closeTab(i int) {
	if len(tabs) <= 1 {
		return // keep at least one tab
	}
	t := tabs[i]
	t.btn.Call("remove")
	t.frame.Call("remove")
	tabs = append(tabs[:i], tabs[i+1:]...)
	// rebind indices by reactivating the neighbour
	if active >= len(tabs) {
		active = len(tabs) - 1
	}
	rebindClicks()
	activate(active)
}

// rebindClicks re-points each tab button at its current index after a removal.
func rebindClicks() {
	for i, t := range tabs {
		idx := i
		nb := t.btn.Call("cloneNode", true) // drop old listeners
		t.btn.Get("parentNode").Call("replaceChild", nb, t.btn)
		t.btn = nb
		onClick(t.btn, func() { activate(idx) })
		onClick(t.btn.Get("lastChild"), func() { closeTab(idx) })
	}
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
	root.Get("style").Set("cssText", "position:absolute;inset:0;display:flex;flex-direction:column;background:#15131c")

	strip = mk("div")
	strip.Get("style").Set("cssText", "display:flex;gap:2px;align-items:flex-end;background:#100d18;border-bottom:1px solid #2a2342;padding:3px 3px 0;min-height:24px")
	plus := btn("+", "padding:.2em .55em;border-radius:5px 5px 0 0")
	onClick(plus, func() { addTab(startPage) })
	strip.Call("appendChild", plus)

	bar := mk("div")
	bar.Get("style").Set("cssText", "display:flex;gap:4px;padding:4px;background:#100d18;border-bottom:1px solid #2a2342")
	back, fwd, reload := btn("◀", "padding:2px 8px"), btn("▶", "padding:2px 8px"), btn("⟳", "padding:2px 8px")
	addr = mk("input")
	addr.Get("style").Set("cssText", "flex:1;background:#0e0c14;color:#cdd2da;border:1px solid #2a2342;padding:2px 8px;font:13px monospace")
	goBtn := btn("Go", "padding:2px 8px")
	for _, el := range []js.Value{back, fwd, reload, addr, goBtn} {
		bar.Call("appendChild", el)
	}

	views = mk("div")
	views.Get("style").Set("cssText", "position:relative;flex:1;min-height:0")

	root.Call("appendChild", strip)
	root.Call("appendChild", bar)
	root.Call("appendChild", views)

	cur := func() *tab {
		if active >= 0 {
			return tabs[active]
		}
		return nil
	}
	onClick(goBtn, func() { navigate(cur(), addr.Get("value").String()) })
	onClick(reload, func() { t := cur(); load(t, t.hist[t.pos]) })
	onClick(back, func() {
		if t := cur(); t.pos > 0 {
			t.pos--
			load(t, t.hist[t.pos])
		}
	})
	onClick(fwd, func() {
		if t := cur(); t.pos < len(t.hist)-1 {
			t.pos++
			load(t, t.hist[t.pos])
		}
	})
	addr.Call("addEventListener", "keydown", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 && a[0].Get("key").String() == "Enter" {
			navigate(cur(), addr.Get("value").String())
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

	addTab(startPage)
}
