// Package proxystatus pkg/proxystatus/render.go c4-app-web
package proxystatus

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/bitree"
)

// refreshSeconds is the fallback full-page reload cadence for surfaces that have
// no live-push endpoint (status.dmsg, status.skynet). status.skysocks is kept in
// sync by a WebSocket stream instead (see liveScript / RenderFragment) and omits
// the meta refresh entirely.
const refreshSeconds = 4

// maxLogLines caps how many recent log lines the page renders, newest at the
// bottom (terminal-tail order). The Provider may return fewer.
const maxLogLines = 200

// Render returns the full, self-contained HTML status page for snap. All
// interpolated values are HTML-escaped; the page loads no external resource
// (matching the proxies' strict no-network serving context).
//
// For the skysocks surface the live region (pills, per-leg mux, events, log) is
// wrapped in <main id="live"> and kept current by an inline WebSocket script that
// swaps just that element on each server push — the server-rendered markup is the
// initial paint, so the page still works with JS/WebSocket unavailable. Other
// surfaces have no WebSocket endpoint and fall back to a meta refresh, as before.
func Render(snap Snapshot) []byte {
	var b strings.Builder
	surface := html.EscapeString(string(snap.Surface))
	live := snap.Surface == SurfaceSkysocks
	b.WriteString("<!doctype html><html lang=en><head><meta charset=utf-8>")
	b.WriteString(`<meta name=viewport content="width=device-width, initial-scale=1">`)
	if !live {
		fmt.Fprintf(&b, `<meta http-equiv="refresh" content="%d">`, refreshSeconds)
	}
	// fontFace (the ~38 KB embedded mononoki woff2) rides in the page shell only, so
	// the tree's box-drawing glyphs align in a true fixed-width font; it is NOT part
	// of css/RenderFragment, which the WebSocket restreams every ~1s.
	fmt.Fprintf(&b, "<title>%s proxy status · skywire</title><style>%s%s</style></head><body>", surface, fontFace, css)

	// Header + brand. The live indicator sits OUTSIDE <main id="live"> (in the
	// static header) on purpose: the WebSocket swap replaces the live region's
	// innerHTML, so an indicator inside it would be wiped on every push. liveScript
	// drives it (connecting → live → reconnecting); it renders as a neutral dot for
	// no-JS / non-skysocks surfaces.
	b.WriteString(`<header><div class="brand"><b>skywire</b> proxy status</div>`)
	fmt.Fprintf(&b, `<div class="surface">%s</div>`, surface)
	if live {
		b.WriteString(`<span id="wsstat" class="wsstat wait" title="live update stream"><i class="dot"></i>connecting</span>`)
	}
	b.WriteString(`</header>`)

	// Live region: server-rendered here for the initial paint, then (skysocks)
	// swapped in place by the SSE script. RenderFragment renders the identical
	// inner markup so there is one source of truth.
	b.WriteString(`<main id="live">`)
	writeLiveRegion(&b, snap)
	b.WriteString(`</main>`)

	writeControlSeam(&b, snap)
	writeFooter(&b, snap)

	if live {
		b.WriteString(liveScript)
	}

	b.WriteString("</body></html>")
	return []byte(b.String())
}

// RenderFragment renders ONLY the live region — the inner HTML of
// <main id="live"> (pills, per-leg mux, events, recent log) — with no
// <html>/<head>/<style> shell. It is the single source of truth for the live
// markup: Render composes the page shell around it, and the skysocks-client
// WebSocket handler pushes it verbatim as one TEXT frame so the browser swaps the
// region in place (el.innerHTML = frame) without a full-page reload. Newlines in
// the fragment (the <pre> log block) ride through the TEXT frame as-is, so no
// escaping is needed.
func RenderFragment(snap Snapshot) []byte {
	var b strings.Builder
	writeLiveRegion(&b, snap)
	return []byte(b.String())
}

// liveScript opens a WebSocket to <origin>/ws and replaces the live region's
// innerHTML on each server push — a seamless live update in place of the old
// jarring full-page meta refresh, and a duplex channel so the page can send
// control commands back (window.sendCmd). The URL is derived from the page's OWN
// origin — location.origin.replace(/^http/,"ws") + "/ws" — never a hardcoded
// http://status.skysocks/… absolute: an absolute http:// subrequest is force-
// upgraded to https by the browser's HTTPS-Only Mode and then fails on the HTTP-
// only .skysocks host, whereas the relative form inherits the page's actual
// (non-upgraded) ws:// scheme and host.
//
// Progressive enhancement: the server-rendered initial paint stands alone, so the
// page still works with JS or WebSocket unavailable. A healthy message cancels any
// pending fallback. On close/error it reconnects after a short backoff (2s);
// only if reconnects never recover does the slow (15s) full reload fire as a last
// resort. window.sendCmd(obj) sends a JSON control frame (e.g. {cmd:"resync"}) and
// is the seam the route-control buttons use. Emitted only for the skysocks surface
// (the only one with a /ws handler).
//
// Selection guard (the copy-without-fighting-the-repaint fix): before swapping the
// live region we check window.getSelection(); while the user has a non-collapsed
// selection we stash the newest fragment in `pend` and return, so the ~1s push
// cadence can't destroy a highlight mid-copy. The deferred fragment is applied the
// moment the selection collapses (selectionchange). apply() also skips the DOM
// write entirely when the incoming fragment is byte-identical to what's shown, so a
// steady surface stops repainting at all — no needless innerHTML churn.
//
// Scroll guard: replacing the live region's innerHTML resets scroll, so before the
// swap apply() captures the window offset (scrollX/scrollY) plus any still-scrollable
// sub-container's offset (the recent-log <pre>'s scrollTop/scrollLeft) and restores
// them right after. With the route tree now page-level (no inner overflow box), the
// tree's horizontal scroll IS the window offset, so scrolling the page to read a long
// PK stays put across the ~1s live pushes instead of snapping back.
//
// Copy affordance: a delegated click handler copies any [data-copy] element's full
// value (untruncated PKs, transport ids) to the clipboard, using the async
// Clipboard API when available and falling back to execCommand('copy') so it works
// over plain HTTP (status.skysocks is not a secure context). It flashes the element
// green briefly. The live indicator (#wsstat) is driven here too.
const liveScript = `<script>(function(){var t,ws,pend=null,last=null,pv=null;` +
	`function stat(s,c){var el=document.getElementById("wsstat");if(el){el.textContent=s;el.className="wsstat "+c;el.insertAdjacentHTML("afterbegin",'<i class="dot"></i>');}}` +
	`function slow(){if(!t){t=setTimeout(function(){location.reload();},15000);}}` +
	`function url(){return location.origin.replace(/^http/,"ws")+"/ws";}` +
	`function selecting(){var s=window.getSelection();return !!(s&&!s.isCollapsed&&String(s));}` +
	// Live up/down rate meters: difference the cumulative data-val byte totals of the
	// two rgsummary stats between pushes and divide by elapsed wall time. Counter
	// resets (route rebuild) clamp to 0. Seeded from the initial render so the first
	// push measures over the real interval.
	`function fmtRate(b){b=b||0;if(b>=1048576){return (b/1048576).toFixed(1)+"M";}if(b>=1024){return (b/1024).toFixed(1)+"K";}return Math.round(b)+"B";}` +
	`function meters(el){var now=Date.now(),cur={},ss=el.querySelectorAll(".stat[data-bytes]"),i;` +
	`for(i=0;i<ss.length;i++){cur[ss[i].getAttribute("data-bytes")]=parseFloat(ss[i].getAttribute("data-val"))||0;}` +
	`if(pv&&now>pv.t){var dt=(now-pv.t)/1000,rs=el.querySelectorAll(".rate[data-rate]"),k;` +
	`for(k=0;k<rs.length;k++){var key=rs[k].getAttribute("data-rate"),d=(cur[key]||0)-(pv[key]||0);if(d<0){d=0;}rs[k].textContent="· "+fmtRate(d/dt)+"/s";}}` +
	`pv={up:cur.up||0,down:cur.down||0,t:now};}` +
	// Pin the log pane to the newest line unless the user has scrolled up to read
	// back; restore any horizontal offset and the window offset as before.
	`function apply(h){if(h===last){return;}var el=document.getElementById("live");if(!el){return;}` +
	`var sx=window.scrollX,sy=window.scrollY,lg=el.querySelector("pre.log");` +
	`var lt=lg?lg.scrollTop:0,ll=lg?lg.scrollLeft:0,pin=lg?(lg.scrollTop+lg.clientHeight>=lg.scrollHeight-4):true;` +
	// Preserve the open/closed state of the open-streams <details> across the
	// innerHTML swap (same idea as the scroll/selection guards): the WS restreams
	// the whole live region every ~1s, which would otherwise snap the dropdown shut.
	`var dop=el.querySelector("details.streams"),dopen=dop?dop.open:false;` +
	`el.innerHTML=h;last=h;` +
	`var ds=el.querySelector("details.streams");if(ds){ds.open=dopen;}` +
	`var lg2=el.querySelector("pre.log");if(lg2){lg2.scrollTop=pin?lg2.scrollHeight:lt;lg2.scrollLeft=ll;}meters(el);window.scrollTo(sx,sy);}` +
	`function push(h){if(selecting()){pend=h;return;}apply(h);}` +
	`document.addEventListener("selectionchange",function(){if(pend!==null&&!selecting()){var h=pend;pend=null;apply(h);}});` +
	`function connect(){stat("connecting","wait");try{ws=new WebSocket(url());}catch(e){slow();return;}` +
	`ws.onopen=function(){stat("live","ok");};` +
	`ws.onmessage=function(e){if(t){clearTimeout(t);t=null;}stat("live","ok");push(e.data);};` +
	`ws.onclose=function(){ws=null;stat("reconnecting","warn");slow();setTimeout(connect,2000);};` +
	`ws.onerror=function(){try{ws.close();}catch(e){}};}` +
	`window.sendCmd=function(o){try{if(ws&&ws.readyState===1){ws.send(JSON.stringify(o));return true;}}catch(e){}return false;};` +
	`function flash(el,cls){if(!el){return;}el.classList.add(cls);setTimeout(function(){el.classList.remove(cls);},800);}` +
	`function fb(txt){try{var a=document.createElement("textarea");a.value=txt;a.setAttribute("readonly","");a.style.position="fixed";a.style.opacity="0";document.body.appendChild(a);a.select();document.execCommand("copy");document.body.removeChild(a);return true;}catch(e){return false;}}` +
	`function copy(txt,el){function ok(){flash(el,"copied");}if(navigator.clipboard&&navigator.clipboard.writeText&&window.isSecureContext){navigator.clipboard.writeText(txt).then(ok,function(){if(fb(txt)){ok();}});}else if(fb(txt)){ok();}}` +
	`document.addEventListener("click",function(e){var el=e.target.closest?e.target.closest("[data-copy]"):null;if(el){e.preventDefault();copy(el.getAttribute("data-copy")||el.textContent,el);}});` +
	`window.rsync=function(btn){var ok=sendCmd({cmd:"resync"});flash(btn,ok?"flash":"deny");return ok;};` +
	// Seed the rate baseline from the server-rendered paint and pin the log pane to
	// its newest line so the tail is visible before the first live push arrives.
	`var el0=document.getElementById("live");if(el0){meters(el0);var lg0=el0.querySelector("pre.log");if(lg0){lg0.scrollTop=lg0.scrollHeight;}}` +
	`document.body.classList.add("js");connect();})();</script>`

// writeLiveRegion writes the dynamic status content (pills, per-leg mux, events,
// recent log) shared by Render (page shell) and RenderFragment (SSE push).
func writeLiveRegion(b *strings.Builder, snap Snapshot) {
	surface := html.EscapeString(string(snap.Surface))

	// Status pills.
	b.WriteString(`<div class="pills">`)
	writePill(b, "surface", surface, "")
	writePill(b, "app", html.EscapeString(snap.App), "")
	if snap.Running {
		writePill(b, "state", "running", "ok")
	} else {
		writePill(b, "state", "stopped", "warn")
	}
	if len(snap.Legs) > 0 {
		mux := "off"
		if snap.MuxEnabled {
			mux = "on"
		}
		writePill(b, "mux", mux, "")
		writePill(b, "legs", fmt.Sprintf("%d", len(snap.Legs)), "")
	}
	b.WriteString(`</div>`)

	if snap.Note != "" {
		fmt.Fprintf(b, `<p class="note">%s</p>`, html.EscapeString(snap.Note))
	}

	writeStreamsSection(b, snap)
	writeMuxSection(b, snap)
	writeLogSection(b, snap)
}

func writePill(b *strings.Builder, k, v, cls string) {
	c := "pill"
	if cls != "" {
		c += " " + cls
	}
	fmt.Fprintf(b, `<span class="%s"><i>%s</i> %s</span>`, c, html.EscapeString(k), v)
}

// writeMuxSection renders the route group as ONE unified route tree rooted at
// the local visor — the flat mux table and the separate "full routes" hop
// chains folded into a single view inspired by `skywire cli visor ping tree`
// (one root, branches = the peers this visor reaches). Every leg is a path
// local → … → destination; all paths are prefix-merged into the one tree, so a
// shared first hop collapses into a single branch that diverges deeper. Each
// branch/edge carries the transport used for that hop (id + type + rtt) and each
// leg's end-to-end mux telemetry (hop count, gate state, route-rtt, rtx,
// sent/recv share bars) hangs off the destination leaf for that leg. A static,
// no-JS analog of the `cli proxy mux plot` panel.
func writeMuxSection(b *strings.Builder, snap Snapshot) {
	b.WriteString(`<section><h2>route group · per-leg mux</h2>`)
	if len(snap.Legs) == 0 {
		b.WriteString(`<p class="empty">No active route group for this surface right now. ` +
			`A leg appears here once a route to a destination is warm.</p></section>`)
		return
	}
	var totSent, totRecv uint64
	var nActive, nStandby, nClosed int
	for _, l := range snap.Legs {
		totSent += l.SentBytes
		totRecv += l.RecvBytes
		switch {
		case !l.Alive:
			nClosed++
		case l.Standby:
			nStandby++
		default:
			nActive++
		}
	}
	// Scannable route-group summary: leg census + aggregate throughput, so the
	// operator gets the shape of the group before reading the route tree. The
	// cumulative sent/recv totals carry machine-readable data-bytes attributes so
	// the live script can difference successive ~1s WebSocket pushes into the live
	// up/down RATE meters (bytes/sec) beside them — no server-side rate needed and
	// no per-leg speed field on the wire.
	b.WriteString(`<div class="rgsummary">`)
	writeStat(b, "legs", fmt.Sprintf("%d", len(snap.Legs)), "")
	writeStat(b, "active", fmt.Sprintf("%d", nActive), "ok")
	if nStandby > 0 {
		writeStat(b, "standby", fmt.Sprintf("%d", nStandby), "standby")
	}
	if nClosed > 0 {
		writeStat(b, "closed", fmt.Sprintf("%d", nClosed), "warn")
	}
	fmt.Fprintf(b, `<span class="stat" data-bytes="up" data-val="%d"><i>up</i> %s <b class="rate" data-rate="up">· —/s</b></span>`, totSent, humanBytes(totSent))
	fmt.Fprintf(b, `<span class="stat" data-bytes="down" data-val="%d"><i>down</i> %s <b class="rate" data-rate="down">· —/s</b></span>`, totRecv, humanBytes(totRecv))
	b.WriteString(`</div>`)

	// The route tree itself: ONE shared bilateral model (pkg/proxystatus.RouteTree)
	// rendered by pkg/bitree — the exact model + geometry `skywire cli proxy tree`
	// prints, so the page and the terminal never drift. Root = this visor; each
	// active route is a right branch (its hop chain, dead legs pruned) with a left
	// summary (R[n], state glyph, route rtt, bandwidth). htmlStyleCell decorates
	// cells (colored state dot, click-to-copy PKs/tpids) without disturbing the
	// column alignment (layout is computed from the plain text).
	b.WriteString(`<div class="tree">`)
	writeTreeLegend(b)
	b.WriteString(`<pre class="bitree">`)
	b.WriteString(bitree.Render(RouteTree(snap), bitree.Options{StyleCell: htmlStyleCell}))
	b.WriteString(`</pre>`)
	b.WriteString(`</div>`)
	b.WriteString(`<p class="hint">The same tree prints from <code>skywire cli proxy tree</code>; for a live chart use ` +
		`<code>skywire cli proxy mux plot</code>.</p></section>`)
}

// writeTreeLegend prints a compact, monospace header/legend above the route tree
// — mirroring `skywire cli tp tree`'s swatch+column header. It names the color
// coding (source PK accent, active/standby state dot colors) with swatches, then
// the left-summary and per-hop column hints, so the tree is self-describing
// without a per-node word label. Dead legs are pruned, so there is no dead
// swatch.
func writeTreeLegend(b *strings.Builder) {
	b.WriteString(`<div class="tlegend">` +
		`<span class="lgnd src">source · this visor</span>` +
		`<span class="lgnd ok">active ●</span>` +
		`<span class="lgnd standby">standby ○</span>` +
		`<span class="lgnd cols">left: R[n] · state · route-rtt · bw ↑↓ &nbsp; hop: peer · [type] · tpid · tp-rtt</span>` +
		`</div>`)
}

// htmlStyleCell is the page's bitree StyleCell: it decorates each cell with the
// page's existing color/interaction classes WITHOUT changing display width (the
// spans it injects are zero-width in the monospace <pre>, and bitree computes
// layout from the plain text before this runs). PKs and transport ids become
// click-to-copy; the state dot in the left summary is colored; transport
// columns are muted.
func htmlStyleCell(text string, kind bitree.CellKind) string {
	switch kind {
	case bitree.CellRoot:
		return `<span class="src">` + copyablePK(text) + `</span>`
	case bitree.CellLabel:
		return copyablePK(text)
	case bitree.CellColumn:
		return htmlTreeColumn(text)
	case bitree.CellLeft:
		return htmlLegSummary(text)
	default:
		return html.EscapeString(text)
	}
}

// htmlTreeColumn styles one trailing hop column. The three columns per hop are,
// in order, [tp-type], tpid, transport-rtt — so a bracketed or ms-suffixed cell
// is a muted label and anything else is the (click-to-copy) transport id.
func htmlTreeColumn(text string) string {
	switch {
	case text == "" || text == "-":
		return html.EscapeString(text)
	case strings.HasPrefix(text, "["), strings.HasSuffix(text, "ms"):
		return `<span class="thop">` + html.EscapeString(text) + `</span>`
	default:
		return copyableID(text, "transport id")
	}
}

// htmlLegSummary colors the left per-route summary: the whole block is tinted by
// state (ok=active green / standby=amber) and the state dot is emphasized. The
// leading right-justify padding rides inside the span (harmless in a <pre>).
// State is read from the glyph the adapter chose, so no state word is needed.
func htmlLegSummary(text string) string {
	if strings.TrimSpace(text) == "" {
		return text // continuation / empty left row
	}
	cls, glyph := "ok", GlyphActive
	if strings.Contains(text, GlyphStandby) {
		cls, glyph = "standby", GlyphStandby
	}
	esc := html.EscapeString(text) // the glyphs/arrows are not HTML-special
	dot := `<span class="tstate ` + cls + `">` + glyph + `</span>`
	esc = strings.Replace(esc, glyph, dot, 1)
	return `<span class="lsum ` + cls + `">` + esc + `</span>`
}

// legRank orders legs for the tree: the direct active leg (carrying app traffic)
// first, then active multihop, then warm standby, then dead legs.
func legRank(l Leg) int {
	switch {
	case !l.Alive:
		return 3
	case l.Standby:
		return 2
	case l.Direct:
		return 0
	default:
		return 1
	}
}

// writeStreamsSection expands the "N open stream(s)" count into per-stream rows
// when the surface tracks them (skysocks-client records id + CONNECT target +
// age locally). It is a native <details> so it stays collapsed by default and
// costs nothing to skip. Per-stream BYTES are deliberately not shown: the yamux
// layer carrying the streams does not meter them per stream — the byte counters
// that exist are the route-group sent/recv totals in the mux section above.
func writeStreamsSection(b *strings.Builder, snap Snapshot) {
	if len(snap.Streams) == 0 {
		return
	}
	fmt.Fprintf(b, `<details class="streams"><summary>open streams <b>%d</b></summary>`, len(snap.Streams))
	b.WriteString(`<table class="strm"><thead><tr><th>id</th><th>target</th><th>age</th></tr></thead><tbody>`)
	for _, s := range snap.Streams {
		fmt.Fprintf(b, `<tr><td>%d</td><td>%s</td><td>%s</td></tr>`,
			s.ID, html.EscapeString(orDash(s.Target)), html.EscapeString(compactAge(s.AgeMS)))
	}
	b.WriteString(`</tbody></table>`)
	b.WriteString(`<p class="hint">Per-stream byte counts are not metered at the tunnel (yamux) layer; ` +
		`the route-group <b>sent/recv</b> totals above are the byte counters that exist.</p>`)
	b.WriteString(`</details>`)
}

// levelClass maps an UPPERCASE log level token to the CSS class that colors it
// the way `skywire cli proxy start --verbose` colors the level in a terminal:
// INFO green, WARN amber, ERROR/FATAL/PANIC red, DEBUG blue, TRACE grey. The
// second return is false for an unrecognized token.
func levelClass(tok string) (string, bool) {
	switch strings.ToUpper(tok) {
	case "INFO":
		return "ll-info", true
	case "WARN", "WARNING":
		return "ll-warn", true
	case "ERRO", "ERROR", "FATA", "FATAL", "PANI", "PANIC":
		return "ll-error", true
	case "DEBU", "DEBUG":
		return "ll-debug", true
	case "TRAC", "TRACE":
		return "ll-trace", true
	}
	return "", false
}

// firstToken returns the first whitespace-delimited token of s.
func firstToken(s string) string {
	s = strings.TrimLeft(s, " \t")
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// writeLogLine emits one formatted log line as per-token colored spans matching
// the terminal logger (pkg/logging printColored). The line shape is
//
//	[timestamp] LEVEL [prefix]: message key=value key=value …
//
// and each token is colored: the [timestamp] grey, the LEVEL word its level
// color, the "[prefix]:" token cyan, the message default foreground, and every
// trailing field's KEY tinted to the level color (its =value left default). A
// line that doesn't begin with a recognizable "[ts] LEVEL" shape is emitted as
// plain default-foreground text (unchanged from the pre-token behavior).
func writeLogLine(b *strings.Builder, line string) {
	esc := html.EscapeString
	rest := line

	// [timestamp]
	var ts string
	if strings.HasPrefix(rest, "[") {
		if i := strings.IndexByte(rest, ']'); i >= 0 {
			ts = rest[:i+1]
			rest = rest[i+1:]
		}
	}
	// leading spaces + LEVEL
	sp := leadingSpaces(rest)
	afterSp := rest[len(sp):]
	lvlTok := firstToken(afterSp)
	cls, ok := levelClass(lvlTok)
	if ts == "" || !ok {
		b.WriteString(esc(line)) // unrecognized shape: plain default foreground
		return
	}
	b.WriteString(`<span class="ll-ts">`)
	b.WriteString(esc(ts))
	b.WriteString(`</span>`)
	b.WriteString(esc(sp))
	b.WriteString(`<span class="`)
	b.WriteString(cls)
	b.WriteString(`">`)
	b.WriteString(esc(lvlTok))
	b.WriteString(`</span>`)
	rest = afterSp[len(lvlTok):]

	// optional " [prefix]:" — the module token, brackets and trailing colon.
	sp2 := leadingSpaces(rest)
	body := rest[len(sp2):]
	if strings.HasPrefix(body, "[") {
		if i := strings.IndexByte(body, ']'); i >= 0 && i+1 < len(body) && body[i+1] == ':' {
			pfx := body[:i+2] // "[module]:"
			b.WriteString(esc(sp2))
			b.WriteString(`<span class="ll-prefix">`)
			b.WriteString(esc(pfx))
			b.WriteString(`</span>`)
			rest = body[i+2:]
			writeLogMsgFields(b, rest, cls)
			return
		}
	}
	// no valid prefix token: emit the remainder as message + fields.
	writeLogMsgFields(b, rest, cls)
}

// writeLogMsgFields emits the message-and-fields tail of a log line: the message
// (up to the first " key=" field) as default foreground, then each trailing
// logfmt field with its KEY tinted to cls and its =value left default. Field
// values may contain spaces (unquoted), so a field runs until the next " key="
// boundary. Spacing is preserved verbatim.
func writeLogMsgFields(b *strings.Builder, s, cls string) {
	esc := html.EscapeString
	starts := fieldStarts(s)
	if len(starts) == 0 {
		b.WriteString(esc(s))
		return
	}
	b.WriteString(esc(s[:starts[0]])) // message
	for fi, st := range starts {
		end := len(s)
		if fi+1 < len(starts) {
			end = starts[fi+1]
		}
		seg := s[st:end] // " key=value…"
		lead := leadingSpaces(seg)
		kv := seg[len(lead):]
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			b.WriteString(esc(seg))
			continue
		}
		b.WriteString(esc(lead))
		b.WriteString(`<span class="`)
		b.WriteString(cls)
		b.WriteString(`">`)
		b.WriteString(esc(kv[:eq]))
		b.WriteString(`</span>`)
		b.WriteString(esc(kv[eq:])) // "=value"
	}
}

// fieldStarts returns the byte offsets in s at which a trailing logfmt field
// begins — the first byte of the run of space(s) that precedes an identifier
// immediately followed by '='. Everything before the first offset is the
// message; each field's value extends to the next offset (so unquoted values
// with spaces stay whole).
func fieldStarts(s string) []int {
	var out []int
	i := 0
	for i < len(s) {
		if s[i] != ' ' {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == ' ' {
			j++
		}
		k := j
		for k < len(s) && isIdentByte(s[k]) {
			k++
		}
		if k > j && k < len(s) && s[k] == '=' {
			out = append(out, i)
			i = k + 1
			continue
		}
		i = j
	}
	return out
}

// isIdentByte reports whether c may appear in a logfmt field key.
func isIdentByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '_' || c == '.'
}

// leadingSpaces returns the leading run of ASCII spaces in s.
func leadingSpaces(s string) string {
	i := 0
	for i < len(s) && s[i] == ' ' {
		i++
	}
	return s[:i]
}

// writeLogSection renders the single, combined route+transport+log stream — the
// same content `proxy start --verbose` prints — as one terminal-colored,
// scrollable window (fixed max-height, newest at the bottom). The Snapshot's
// route/transport lifecycle Events and the process Logs are two views of the one
// per-app log ring; they are merged in timestamp order into a single tail here so
// the operator reads one chronological stream instead of two disjoint lists. The
// live WebSocket restreams this fragment, and the inline script pins the pane to
// the bottom so the newest line stays visible.
func writeLogSection(b *strings.Builder, snap Snapshot) {
	b.WriteString(`<section><h2>route, transport &amp; log</h2>`)
	lines := combinedLogLines(snap.Events, snap.Logs, maxLogLines)
	if len(lines) == 0 {
		b.WriteString(`<p class="empty">No route, transport, or log events for this process yet.</p></section>`)
		return
	}
	b.WriteString(`<pre class="log">`)
	for _, ln := range lines {
		ln = strings.TrimRight(ln, "\r\n")
		writeLogLine(b, ln)
		b.WriteByte('\n')
	}
	b.WriteString(`</pre></section>`)
}

// combinedLogLines merges the route/transport lifecycle events and the process
// log lines — both formatted "[ts] LEVEL [module]: msg", both oldest-first — into
// one chronological tail of at most limit lines. Each input is already sorted, so
// a stable two-pointer merge on the parsed leading timestamp suffices; a line
// whose timestamp can't be parsed inherits the last seen time so it keeps its
// relative position instead of jumping.
func combinedLogLines(events, logs []string, limit int) []string {
	out := make([]string, 0, len(events)+len(logs))
	var last time.Time
	tOf := func(s string) time.Time {
		if t, ok := parseLogTime(s); ok {
			last = t
			return t
		}
		return last
	}
	i, j := 0, 0
	for i < len(events) && j < len(logs) {
		if !tOf(events[i]).After(tOf(logs[j])) {
			out = append(out, events[i])
			i++
		} else {
			out = append(out, logs[j])
			j++
		}
	}
	out = append(out, events[i:]...)
	out = append(out, logs[j:]...)
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// logTimeLayout is the timestamp format the log ring's formatter emits (see
// pkg/logging recordFormatter): "[2006-01-02T15:04:05.0000Z07:00]".
const logTimeLayout = "2006-01-02T15:04:05.0000Z07:00"

// parseLogTime extracts the leading "[timestamp]" from a formatted log line.
func parseLogTime(line string) (time.Time, bool) {
	if len(line) == 0 || line[0] != '[' {
		return time.Time{}, false
	}
	i := strings.IndexByte(line, ']')
	if i < 0 {
		return time.Time{}, false
	}
	t, err := time.Parse(logTimeLayout, line[1:i])
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// compactAge formats a stream age (milliseconds) as a terse "12s" / "3m" / "1h".
func compactAge(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}

// writeControlSeam renders the route-control panel. For skysocks the "resync"
// button is live — it proves the WebSocket send path by calling window.sendCmd
// (defined in liveScript), which the server answers with an immediate fragment
// push. The mux-op buttons (add/drop leg, mux mode, rebuild) stay disabled: they
// mutate the route group and need an app→visor mux-control RPC that does not exist
// yet (see the TODO in pkg/skysocks/client.go handleStatusControl). Non-skysocks
// surfaces have no WebSocket and keep the fully-inert preview.
func writeControlSeam(b *strings.Builder, snap Snapshot) {
	b.WriteString(`<section class="seam"><h2>route control <small>read-only preview</small></h2>`)
	switch snap.Surface {
	case SurfaceSkysocks:
		b.WriteString(`<p>The live view streams over a WebSocket; <b>resync</b> forces an ` +
			`immediate push. Route mutation is a <b>read-only preview</b> — add/drop leg, mux mode ` +
			`and rebuild are staged for the upcoming mux-control RPC and stay disabled until it lands.</p><div class="controls">`)
		// Live: sends {cmd:"resync"} over the WebSocket (rsync flashes the button on
		// success — see liveScript / handleStatusControl).
		b.WriteString(`<button type="button" class="live" onclick="rsync(this)">resync</button>`)
		// TODO(mux-control): enable once the app→visor mux-control RPC exists. The
		// "soon" tag marks them as staged-not-broken.
		for _, label := range []string{"add leg", "drop leg", "mux mode…", "rebuild route"} {
			fmt.Fprintf(b, `<button disabled title="staged for the mux-control RPC (not yet available)">%s<span class="soon">soon</span></button>`,
				html.EscapeString(label))
		}
	case SurfaceSkynet:
		b.WriteString(`<p>Selecting routes and relays from here is planned. Today these are inert; ` +
			`the MVP status page is read-only.</p><div class="controls">`)
		b.WriteString(`<button disabled>add leg</button><button disabled>drop leg</button>` +
			`<button disabled>mux mode…</button><button disabled>rebuild route</button>`)
	case SurfaceDmsg:
		b.WriteString(`<p>Selecting routes and relays from here is planned. Today these are inert; ` +
			`the MVP status page is read-only.</p><div class="controls">`)
		b.WriteString(`<button disabled>pick dmsg relay…</button><button disabled>reconnect</button>`)
	default:
		b.WriteString(`<p>Selecting routes and relays from here is planned. Today these are inert; ` +
			`the MVP status page is read-only.</p><div class="controls">`)
	}
	b.WriteString(`</div></section>`)
}

func writeFooter(b *strings.Builder, snap Snapshot) {
	b.WriteString(`<footer>other surfaces: `)
	first := true
	for _, s := range []Surface{SurfaceDmsg, SurfaceSkynet, SurfaceSkysocks} {
		if s == snap.Surface {
			continue
		}
		if !first {
			b.WriteString(" · ")
		}
		first = false
		host := Host(s)
		fmt.Fprintf(b, `<a href="http://%s/">%s</a>`, host, html.EscapeString(host))
	}
	b.WriteString(`</footer>`)
}

// --- small helpers -----------------------------------------------------------

// writeStat renders one chip in the route-group summary: an uppercase label and
// its value, optionally tinted (ok/standby/warn) so leg census reads at a glance.
func writeStat(b *strings.Builder, k, v, cls string) {
	c := "stat"
	if cls != "" {
		c += " " + cls
	}
	fmt.Fprintf(b, `<span class="%s"><i>%s</i> %s</span>`, c, html.EscapeString(k), html.EscapeString(v))
}

// copyablePK renders a FULL (never truncated) public key as a click-to-copy
// monospace cell. The [data-copy] attribute carries the exact value the delegated
// copy handler writes to the clipboard; an empty key renders as an inert dash with
// no copy affordance. Progressive enhancement: with JS off the key is still plain
// selectable text (word-break:break-all).
func copyablePK(pk string) string {
	pk = strings.TrimSpace(pk)
	if pk == "" {
		return `<code class="fpk">-</code>`
	}
	esc := html.EscapeString(pk)
	return `<code class="fpk copy" data-copy="` + esc + `" title="click to copy">` + esc + `</code>`
}

// copyableID renders a click-to-copy monospace token (e.g. a transport id) the
// same way copyablePK does, with a caller-supplied hover title. It sits inside
// the edge's .thop span, so the source/exit PK tint (a direct-child selector)
// does not reach it. Empty renders nothing.
func copyableID(id, what string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	esc := html.EscapeString(id)
	return `<code class="ftid copy" data-copy="` + esc + `" title="click to copy ` + html.EscapeString(what) + `">` + esc + `</code>`
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// humanBytes formats a byte count with a binary unit suffix.
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// css: the dark default is tuned so every text token clears WCAG AA (≥4.5:1 for
// body text) against --bg/--card — notably --muted (#a2a8cc ≈ 8:1 on --bg),
// which every low-emphasis label (pills, table headers, .hint/.empty, footer,
// log tail) uses. The light block re-darkens --muted AND the status colors
// (--ok/--warn/--standby), whose dark-mode brights are illegible on a light
// background, so the same ≥4.5:1 floor holds in both schemes. The accent
// gradient and overall identity are unchanged.
const css = `:root{--bg:#0b0d17;--fg:#c7cbe6;--muted:#a2a8cc;--accent:#7c83ff;--accent2:#a06bff;--ok:#4ad9a4;--warn:#ff6b8a;--err:#ff5c5c;--cyan:#3fd0d8;--standby:#e0b64a;--card:#131629;--line:#2b3163}` +
	`*{box-sizing:border-box}html,body{margin:0;background:var(--bg);color:var(--fg);font:13.5px/1.55 system-ui,-apple-system,Segoe UI,Roboto,sans-serif}` +
	`body{max-width:60rem;margin:0 auto;padding:1.2rem 1rem 3rem}` +
	`header{display:flex;align-items:baseline;gap:.8rem;flex-wrap:wrap;border-bottom:1px solid var(--line);padding-bottom:.7rem}` +
	`.brand b{font-weight:600;letter-spacing:.4px;background:linear-gradient(90deg,var(--accent),var(--accent2));-webkit-background-clip:text;background-clip:text;color:transparent}` +
	`.brand{font-size:1rem}.surface{margin-left:auto;font:600 1.1rem ui-monospace,SFMono-Regular,monospace;color:#e7e9ff}` +
	`.wsstat{display:inline-flex;align-items:center;gap:.35rem;margin-left:.7rem;font-size:11px;text-transform:uppercase;letter-spacing:.4px;color:var(--muted)}` +
	`.wsstat .dot{width:.5rem;height:.5rem;border-radius:50%;background:var(--muted);flex:none}` +
	`.wsstat.ok{color:var(--ok)}.wsstat.ok .dot{background:var(--ok);box-shadow:0 0 6px var(--ok);animation:pulse 2s infinite}` +
	`.wsstat.warn{color:var(--warn)}.wsstat.warn .dot{background:var(--warn)}` +
	`.wsstat.wait{color:var(--standby)}.wsstat.wait .dot{background:var(--standby)}` +
	`@keyframes pulse{0%,100%{opacity:1}50%{opacity:.35}}` +
	`.pills{display:flex;flex-wrap:wrap;gap:.4rem;margin:.9rem 0}` +
	`.pill{background:var(--card);border:1px solid var(--line);border-radius:999px;padding:.15rem .6rem;font-size:12px}` +
	`.pill i{color:var(--muted);font-style:normal;margin-right:.25rem;text-transform:uppercase;font-size:10px;letter-spacing:.4px}` +
	`.pill.ok{border-color:var(--ok);color:var(--ok)}.pill.warn{border-color:var(--warn);color:var(--warn)}` +
	`h2{font-size:.95rem;margin:1.6rem 0 .5rem;color:#e7e9ff;font-weight:600}h2 small{color:var(--muted);font-weight:400;font-size:11px;margin-left:.4rem}` +
	`.note{color:var(--standby)}.empty,.hint{color:var(--muted);font-size:12px}` +
	`.rgsummary{display:flex;flex-wrap:wrap;gap:.4rem;margin:.2rem 0 .7rem}` +
	`.stat{background:var(--card);border:1px solid var(--line);border-radius:6px;padding:.15rem .55rem;font-size:12px}` +
	`.stat i{color:var(--muted);font-style:normal;margin-right:.3rem;text-transform:uppercase;font-size:10px;letter-spacing:.4px}` +
	`.stat.ok{color:var(--ok)}.stat.warn{color:var(--warn)}.stat.standby{color:var(--standby)}` +
	`.bar{display:block;height:.6rem;border-radius:3px;background:var(--accent);min-width:2px}` +
	`.bar.ok{background:var(--ok)}.bar.standby{background:var(--standby)}.bar.warn{background:var(--warn)}` +
	`.bar.recv{background:var(--recv,#3b9)}` +
	`.fpk{font-family:'Mononoki',ui-monospace,monospace;font-size:11px;word-break:break-all}` +
	`.ftid{font-family:'Mononoki',ui-monospace,monospace;font-size:10.5px;color:var(--muted)}` +
	`.copy{border-radius:3px;transition:background .15s,color .15s}` +
	`body.js .copy{cursor:pointer}body.js .copy:hover{background:rgba(124,131,255,.14)}` +
	`.copy.copied{background:var(--ok);color:#04120c}.copy.copied::after{content:" ✓ copied";font-size:10px;letter-spacing:.3px}` +
	// One unified route tree (root = local visor; branches = first-hop peers).
	// No inner overflow-x box: the tree lays out at page width and the PAGE (body)
	// scrolls horizontally if the long full PKs run wide, so a scroll position is a
	// window offset the live-swap can restore — not a nested scroll region that
	// resets on every push.
	`.tree{font-family:'Mononoki',ui-monospace,SFMono-Regular,monospace;font-size:11.5px;line-height:1.6;padding:.3rem 0;border:1px solid var(--line);border-radius:7px;background:var(--card);padding:.55rem .65rem}` +
	// The route tree is one <pre class="bitree"> of monospace text laid out by
	// pkg/bitree; every cell decorated by htmlStyleCell must keep the SAME font
	// metrics or the aligned columns skew, so all inner code/span inherit the
	// pre's font and never break-word. Alignment is spaces from bitree; the spans
	// add color/interactivity only.
	// The route tree is drawn with box-drawing glyphs (─│┼┴├┘└…) that MUST tile
	// edge-to-edge to read as connected lines. The embedded 'Mononoki' subset does
	// not carry the U+2500 box-drawing block (nor the arrows/dots), so those glyphs
	// fall back per-character to another font — and a fallback glyph sized to ITS
	// own em never fills a Mononoki cell, leaving gaps. The cure is a SINGLE font
	// for the whole tree: a system-monospace stack (every common one tiles box
	// drawing perfectly), so text AND glyphs share one set of metrics and connect.
	// line-height:1 + letter-spacing:0 keep the vertical │ runs and horizontal ──
	// runs continuous.
	`pre.bitree{font-family:'DejaVu Sans Mono','Liberation Mono','Noto Sans Mono',ui-monospace,'Cascadia Mono','Segoe UI Mono',Menlo,Consolas,'Courier New',monospace;font-size:11.5px;line-height:1;letter-spacing:0;margin:0;white-space:pre;color:var(--fg)}` +
	`pre.bitree code,pre.bitree span{font:inherit;color:inherit;line-height:1;letter-spacing:0;white-space:pre;word-break:normal;background:none;border:0;padding:0}` +
	`pre.bitree code.fpk{color:var(--fg)}pre.bitree .src code.fpk{color:var(--accent);font-weight:600}` +
	`pre.bitree code.ftid{color:var(--muted)}pre.bitree .tcol{color:var(--muted)}` +
	`pre.bitree .lsum.ok{color:var(--ok)}pre.bitree .lsum.standby{color:var(--standby)}` +
	// Tree header/legend (mirrors `tp tree`'s swatch+column header): color swatches
	// name the source/dest PK accents and the active/standby/dead state colors, then
	// the column hints for what each tree line carries. Self-describes the tree so no
	// per-node word pill is needed on the root ("this visor") or leaves ("exit").
	`.tlegend{display:flex;flex-wrap:wrap;gap:.15rem .9rem;font-size:9.5px;text-transform:uppercase;letter-spacing:.3px;color:var(--muted);padding-bottom:.4rem;margin-bottom:.4rem;border-bottom:1px solid var(--line)}` +
	`.lgnd{display:inline-flex;align-items:center;white-space:nowrap}` +
	`.lgnd::before{content:"\2b24";margin-right:.3rem;font-size:8px;line-height:1}` +
	`.lgnd.src::before{color:var(--accent)}.lgnd.dst::before{color:var(--accent2)}` +
	`.lgnd.ok::before{color:var(--ok)}.lgnd.standby::before{color:var(--standby)}.lgnd.warn::before{color:var(--warn)}` +
	`.lgnd.cols::before{content:none}.lgnd.cols{color:var(--muted);opacity:.85;text-transform:none;letter-spacing:0}` +
	// State dot in the left per-route summary (replaces any active/standby word):
	// color-blind-safe by shape (● active / ○ standby) as well as hue.
	`pre.bitree .tstate{font-weight:700}` +
	`pre.bitree .tstate.ok{color:var(--ok)}pre.bitree .tstate.standby{color:var(--standby)}` +
	// Recent log: keep the vertical max-height scroll, but drop the horizontal
	// inner scroll — lines wrap (pre-wrap + break-word) so any width is page-level.
	`pre.log{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:.7rem;font:11.5px/1.5 'Mononoki',ui-monospace,SFMono-Regular,monospace;` +
	`white-space:pre-wrap;word-break:break-word;max-height:26rem;overflow-y:auto;color:var(--fg)}` +
	// Per-token log coloring, matching `proxy start --verbose` (pkg/logging
	// printColored): the [timestamp] is grey, the LEVEL word takes its level color
	// (INFO green, WARN amber, ERROR/FATAL/PANIC red — a real red, not the pink
	// --warn — DEBUG blue, TRACE grey), the "[prefix]:" token is cyan, the message
	// is default, and each field key is tinted to the level color (value default).
	`pre.log .ll-info{color:var(--ok)}pre.log .ll-warn{color:var(--standby)}pre.log .ll-error{color:var(--err)}` +
	`pre.log .ll-debug{color:var(--accent)}pre.log .ll-trace{color:var(--muted)}` +
	`pre.log .ll-ts{color:var(--muted)}pre.log .ll-prefix{color:var(--cyan)}` +
	// Per-stream detail (expandable) behind the "N open stream(s)" count.
	`details.streams{margin:.5rem 0}details.streams summary{cursor:pointer;font-size:12px;color:var(--muted)}` +
	`details.streams summary b{color:var(--fg);margin-left:.2rem}` +
	`table.strm{border-collapse:collapse;font-size:11.5px;margin:.4rem 0;font-family:'Mononoki',ui-monospace,SFMono-Regular,monospace}` +
	`table.strm th,table.strm td{text-align:left;padding:.1rem 1rem .1rem 0;color:var(--fg);white-space:nowrap}` +
	`table.strm th{color:var(--muted);text-transform:uppercase;font-size:9.5px;letter-spacing:.4px}` +
	// Live up/down rate meters computed by the inline script from successive pushes.
	`.stat .rate{margin-left:.2rem;color:var(--accent);font-weight:600;font-size:11px}` +
	`.seam{opacity:.9}.controls{display:flex;flex-wrap:wrap;gap:.4rem;margin-top:.4rem}` +
	`.controls button{font:inherit;font-size:12px;padding:.2rem .6rem;border:1px dashed var(--line);border-radius:6px;background:transparent;color:var(--muted);cursor:not-allowed}` +
	`.controls button{position:relative}` +
	`.controls button .soon{margin-left:.35rem;font-size:8.5px;text-transform:uppercase;letter-spacing:.4px;border:1px solid currentColor;border-radius:999px;padding:0 .25rem;opacity:.75;vertical-align:middle}` +
	`.controls button.live{border:1px solid var(--accent);color:var(--accent);cursor:pointer}` +
	`.controls button.live:hover{background:rgba(124,131,255,.12)}` +
	`.controls button.live.flash{background:var(--ok);border-color:var(--ok);color:#04120c}` +
	`.controls button.live.deny{background:var(--warn);border-color:var(--warn);color:#fff}` +
	`code{color:var(--accent);font-size:11.5px}` +
	`footer{margin-top:2rem;padding-top:.7rem;border-top:1px solid var(--line);color:var(--muted);font-size:12px}` +
	`footer a{color:var(--accent);text-decoration:none}footer a:hover{text-decoration:underline}` +
	`@media(prefers-color-scheme:light){:root{--bg:#f6f7fb;--fg:#1c1e26;--muted:#4a4f63;--card:#fff;--line:#d3d6e4;--accent:#4149d6;--accent2:#7b3fd0;--ok:#0a7a4c;--warn:#c02a48;--err:#c8102e;--cyan:#0a6c74;--standby:#7a5c00}` +
	`h2,.surface{color:#1c1e26}}`
