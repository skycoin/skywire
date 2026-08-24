// Package proxystatus pkg/proxystatus/render.go c4-app-web
package proxystatus

import (
	"fmt"
	"html"
	"strings"

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

// maxEventLines caps how many recent route/transport event lines the page
// renders (oldest first, matching the Provider's ordering). The Provider may
// return fewer.
const maxEventLines = 200

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
const liveScript = `<script>(function(){var t,ws,pend=null,last=null;` +
	`function stat(s,c){var el=document.getElementById("wsstat");if(el){el.textContent=s;el.className="wsstat "+c;el.insertAdjacentHTML("afterbegin",'<i class="dot"></i>');}}` +
	`function slow(){if(!t){t=setTimeout(function(){location.reload();},15000);}}` +
	`function url(){return location.origin.replace(/^http/,"ws")+"/ws";}` +
	`function selecting(){var s=window.getSelection();return !!(s&&!s.isCollapsed&&String(s));}` +
	`function apply(h){if(h===last){return;}var el=document.getElementById("live");if(!el){return;}` +
	`var sx=window.scrollX,sy=window.scrollY,lg=el.querySelector("pre.log"),lt=lg?lg.scrollTop:0,ll=lg?lg.scrollLeft:0;` +
	`el.innerHTML=h;last=h;` +
	`var lg2=el.querySelector("pre.log");if(lg2){lg2.scrollTop=lt;lg2.scrollLeft=ll;}window.scrollTo(sx,sy);}` +
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

	writeMuxSection(b, snap)
	writeEventsSection(b, snap)
	writeLogsSection(b, snap)
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
	// operator gets the shape of the group before reading the route tree.
	b.WriteString(`<div class="rgsummary">`)
	writeStat(b, "legs", fmt.Sprintf("%d", len(snap.Legs)), "")
	writeStat(b, "active", fmt.Sprintf("%d", nActive), "ok")
	if nStandby > 0 {
		writeStat(b, "standby", fmt.Sprintf("%d", nStandby), "standby")
	}
	if nClosed > 0 {
		writeStat(b, "closed", fmt.Sprintf("%d", nClosed), "warn")
	}
	writeStat(b, "sent", humanBytes(totSent), "")
	writeStat(b, "recv", humanBytes(totRecv), "")
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
	b.WriteString(`<p class="hint">One route tree rooted at this visor (source accent); each active route is a ` +
		`branch — its hop chain to the exit, each hop carrying the transport (type · id · rtt) — with a left ` +
		`summary block (R[n], a <b>color-coded</b> state dot — active green, standby amber — the route rtt and ` +
		`its bandwidth X↑ Y↓). Dead legs are pruned. ` +
		`<b>tp rtt</b> (a hop's column) is that transport; <b>route rtt</b> (the left summary) is the true ` +
		`end-to-end route latency across all hops. Click any public key (or transport id) to copy it. ` +
		`The same tree prints from <code>skywire cli proxy tree</code>; for a live chart use ` +
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

func writeEventsSection(b *strings.Builder, snap Snapshot) {
	b.WriteString(`<section><h2>route &amp; transport events</h2>`)
	if len(snap.Events) == 0 {
		// Scaffold: event capture is wired but may be empty on an idle surface,
		// and the richer per-surface event stream (leg promote/demote, transport
		// drop) is an extension point — see the control seam below.
		b.WriteString(`<p class="empty">No route or transport events captured for this surface yet.</p>`)
	} else {
		events := snap.Events
		if len(events) > maxEventLines {
			events = events[len(events)-maxEventLines:]
		}
		b.WriteString(`<ul class="events">`)
		for _, e := range events {
			fmt.Fprintf(b, `<li>%s</li>`, html.EscapeString(e))
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</section>`)
}

func writeLogsSection(b *strings.Builder, snap Snapshot) {
	b.WriteString(`<section><h2>recent log</h2>`)
	lines := snap.Logs
	if len(lines) > maxLogLines {
		lines = lines[len(lines)-maxLogLines:]
	}
	if len(lines) == 0 {
		b.WriteString(`<p class="empty">No recent log lines for this process.</p></section>`)
		return
	}
	b.WriteString(`<pre class="log">`)
	for _, ln := range lines {
		b.WriteString(html.EscapeString(strings.TrimRight(ln, "\r\n")))
		b.WriteByte('\n')
	}
	b.WriteString(`</pre></section>`)
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
const css = `:root{--bg:#0b0d17;--fg:#c7cbe6;--muted:#a2a8cc;--accent:#7c83ff;--accent2:#a06bff;--ok:#4ad9a4;--warn:#ff6b8a;--standby:#e0b64a;--card:#131629;--line:#2b3163}` +
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
	`pre.bitree{font-family:'Mononoki',ui-monospace,SFMono-Regular,monospace;font-size:11.5px;line-height:1.55;margin:0;white-space:pre;color:var(--fg)}` +
	`pre.bitree code,pre.bitree span{font:inherit;color:inherit;white-space:pre;word-break:normal;background:none;border:0;padding:0}` +
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
	`ul.events{margin:.3rem 0;padding-left:1.1rem;font-size:12px}ul.events li{margin:.1rem 0}` +
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
	`@media(prefers-color-scheme:light){:root{--bg:#f6f7fb;--fg:#1c1e26;--muted:#4a4f63;--card:#fff;--line:#d3d6e4;--accent:#4149d6;--accent2:#7b3fd0;--ok:#0a7a4c;--warn:#c02a48;--standby:#7a5c00}` +
	`h2,.surface{color:#1c1e26}}`
