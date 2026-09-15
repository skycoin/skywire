//go:build js && wasm

package netscrape

import (
	"strings"
	"syscall/js"
)

// startURL names the built-in start page in a tab's history. It is not a URL
// a frame could load: load recognises it and renders the page itself, and the
// address bar shows it as empty, the way every browser shows a new tab — a
// blank prompt for an address, which is what a new tab is for.
const startURL = "about:newtab"

// StartLink is one shortcut on the start page: a page the host thinks a person
// opening a new tab is likely to want.
type StartLink struct {
	Label string
	URL   string
}

// startLinks reads the host's shortcuts from globalThis.__netscrapeStartLinks,
// an array of {label, url} objects. Read at render time rather than at Open,
// so a host can set them after the browser is up — the skywire desk learns
// its pages one at a time as its servers come up — and change them later.
func startLinks() []StartLink {
	v := js.Global().Get("__netscrapeStartLinks")
	if v.Type() != js.TypeObject || !v.InstanceOf(js.Global().Get("Array")) {
		return nil
	}
	var out []StartLink
	for i := 0; i < v.Length(); i++ {
		e := v.Index(i)
		if e.Type() != js.TypeObject {
			continue
		}
		u := e.Get("url")
		if u.Type() != js.TypeString || u.String() == "" {
			continue
		}
		label := u.String()
		if l := e.Get("label"); l.Type() == js.TypeString && l.String() != "" {
			label = l.String()
		}
		out = append(out, StartLink{Label: label, URL: u.String()})
	}
	return out
}

// addrText is what the address bar shows for a history entry: the entry
// itself, except the start page, which shows as nothing.
func addrText(url string) string {
	if url == startURL {
		return ""
	}
	return url
}

// renderStart draws the start page into a tab: the host's shortcuts as tiles
// on the chrome's own dark ground, and a line saying where to type. It is a
// sandboxed srcdoc like a fetched page, so a tile click travels the navShim
// and lands in the tab's history as an ordinary navigation.
//
// This replaced a data: URL holding a paragraph about the browser being
// written in Go. That was the demo's page; as a new tab it showed its own
// source in the address bar and said nothing a person could use.
func renderStart(t *tab) {
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset="utf-8"><title>new tab</title><style>` + startCSS + `</style>`)
	b.WriteString(`<main><h1>netscrape</h1>`)
	if links := startLinks(); len(links) > 0 {
		b.WriteString(`<div class="tiles">`)
		for _, l := range links {
			b.WriteString(`<a class="tile" href="` + htmlEscape(l.URL) + `"><b>` + htmlEscape(l.Label) + `</b><span>` + htmlEscape(l.URL) + `</span></a>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`<p>type an address above</p></main>`)
	t.frame.Call("removeAttribute", "src")
	t.frame.Call("setAttribute", "sandbox", "allow-scripts") // sandboxed: no allow-same-origin
	t.frame.Set("srcdoc", navShim+b.String())
	setTitle(t, "new tab", startURL)
	if t.ico.Truthy() {
		t.ico.Get("style").Set("visibility", "hidden")
	}
	setLoading(t, false)
}

// startCSS is the start page's whole look: the chrome's colours, tiles that
// read as buttons, nothing that competes with the address bar above.
const startCSS = `html,body{margin:0;height:100%;background:#15131c;color:#cdd2da;font:14px system-ui,sans-serif}` +
	`main{min-height:100%;box-sizing:border-box;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:2em;padding:2em}` +
	`h1{margin:0;font:600 20px/1 monospace;letter-spacing:.25em;color:#8f86ad}` +
	`.tiles{display:flex;flex-wrap:wrap;justify-content:center;gap:12px;max-width:46em}` +
	`a.tile{display:flex;flex-direction:column;gap:.35em;width:12em;padding:1em;box-sizing:border-box;border:1px solid #2a2342;border-radius:8px;background:#100d18;color:#cdd2da;text-decoration:none}` +
	`a.tile:hover{border-color:#5a4a8a;background:#1b1626}` +
	`a.tile b{font:600 14px system-ui,sans-serif}` +
	`a.tile span{font:11px monospace;opacity:.55;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}` +
	`p{margin:0;font-size:12px;opacity:.45}`
