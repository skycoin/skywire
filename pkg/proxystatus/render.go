// Package proxystatus pkg/proxystatus/render.go c4-app-web
package proxystatus

import (
	"fmt"
	"html"
	"strings"
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
	fmt.Fprintf(&b, "<title>%s proxy status · skywire</title><style>%s</style></head><body>", surface, css)

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
	`function apply(h){if(h===last){return;}var el=document.getElementById("live");if(el){el.innerHTML=h;last=h;}}` +
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

// writeMuxSection renders the per-leg mux view: one row per leg with a
// bandwidth bar sized to its share of the busiest leg (a static, no-JS analog of
// the `cli proxy mux plot` panel), plus RTT, retransmits and gate state.
func writeMuxSection(b *strings.Builder, snap Snapshot) {
	b.WriteString(`<section><h2>route group · per-leg mux</h2>`)
	if len(snap.Legs) == 0 {
		b.WriteString(`<p class="empty">No active route group for this surface right now. ` +
			`A leg appears here once a route to a destination is warm.</p></section>`)
		return
	}
	var maxSent, maxRecv, totSent, totRecv uint64 = 1, 1, 0, 0
	var nActive, nStandby, nClosed int
	for _, l := range snap.Legs {
		if l.SentBytes > maxSent {
			maxSent = l.SentBytes
		}
		if l.RecvBytes > maxRecv {
			maxRecv = l.RecvBytes
		}
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
	// operator gets the shape of the group before reading the per-leg table.
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
	b.WriteString(`<table class="mux"><thead><tr>` +
		`<th>leg</th><th>route</th><th>transport</th><th>peer</th>` +
		`<th>sent</th><th>bandwidth (sent share)</th>` +
		`<th>recv</th><th>bandwidth (recv share)</th>` +
		`<th>tp rtt</th><th>route rtt</th><th>rtx</th><th>state</th></tr></thead><tbody>`)
	for _, l := range snap.Legs {
		// shares are 0..100 (maxSent/maxRecv are per-leg maxes, >=1) — kept as
		// uint64 and printed directly so there's no uint64->int narrowing.
		sentShare := l.SentBytes * 100 / maxSent
		recvShare := l.RecvBytes * 100 / maxRecv
		state, scls := legState(l)
		// route: direct (1-hop) vs multihop (relayed). RouteLatencyMS is the
		// TRUE end-to-end latency; LatencyMS is only the first-hop transport.
		routeLabel, routeCls := "multihop", "route-relay"
		if l.Direct {
			routeLabel, routeCls = "direct", "route-direct"
		}
		fmt.Fprintf(b, `<tr><td>R%d</td><td><span class="rtag %s">%s</span></td><td>%s</td><td class="pk">%s</td>`,
			l.Index, routeCls, routeLabel, html.EscapeString(orDash(l.TpType)), copyablePK(l.RemotePK))
		fmt.Fprintf(b, `<td>%s</td><td class="barcell"><span class="bar %s" style="width:%d%%"></span></td>`,
			humanBytes(l.SentBytes), scls, sentShare)
		fmt.Fprintf(b, `<td>%s</td><td class="barcell"><span class="bar recv %s" style="width:%d%%"></span></td>`,
			humanBytes(l.RecvBytes), scls, recvShare)
		fmt.Fprintf(b, `<td>%.0f ms</td><td>%s</td><td>%d</td><td><span class="badge %s">%s</span></td></tr>`,
			l.LatencyMS, routeRTT(l.RouteLatencyMS), l.Retransmits, scls, state)
	}
	b.WriteString(`</tbody></table>`)
	writeMuxRoutes(b, snap.Legs)
	b.WriteString(`<p class="hint">Bars are each leg's sent / recv bytes relative to the busiest leg. ` +
		`<b>tp rtt</b> is the first-hop transport; <b>route rtt</b> is the true end-to-end route latency (all hops). ` +
		`Click any public key to copy it. ` +
		`For a live terminal chart use <code>skywire cli proxy mux plot</code>.</p></section>`)
}

// writeMuxRoutes renders each leg's FULL forward route as a hop chain:
// origin PK ─[tptype rtt]→ hop PK ─[…]→ destination PK. Public keys are
// shown in full (never truncated); per-hop latency is shown where known.
func writeMuxRoutes(b *strings.Builder, legs []Leg) {
	any := false
	for _, l := range legs {
		if len(l.Hops) > 0 {
			any = true
			break
		}
	}
	if !any {
		return
	}
	b.WriteString(`<h3>full routes</h3><div class="routes">`)
	for _, l := range legs {
		if len(l.Hops) == 0 {
			continue
		}
		fmt.Fprintf(b, `<div class="route"><span class="rlabel">R%d</span> %s`,
			l.Index, copyablePK(l.Hops[0].From))
		for _, h := range l.Hops {
			lat := ""
			if h.LatencyMS > 0 {
				lat = fmt.Sprintf(" %.0fms", h.LatencyMS)
			}
			fmt.Fprintf(b, ` <span class="hop">─[%s%s]→</span> %s`,
				html.EscapeString(orDash(h.TpType)), html.EscapeString(lat), copyablePK(h.To))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
}

func legState(l Leg) (label, cls string) {
	switch {
	case !l.Alive:
		return "closed", "warn"
	case l.Standby:
		return "standby", "standby"
	default:
		return "active", "ok"
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
		b.WriteString(`<ul class="events">`)
		for _, e := range snap.Events {
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

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// routeRTT formats a leg's end-to-end route latency. Zero means no
// liveness pong has landed yet, shown as an em dash rather than "0 ms"
// so an unmeasured route isn't misread as instant.
func routeRTT(ms float64) string {
	if ms <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f ms", ms)
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
	`.badge{display:inline-block;padding:.02rem .4rem;border-radius:999px;font-size:10.5px;font-weight:600;text-transform:uppercase;letter-spacing:.3px;border:1px solid currentColor}` +
	`.badge.ok{color:var(--ok)}.badge.warn{color:var(--warn)}.badge.standby{color:var(--standby)}` +
	`table.mux{width:100%;border-collapse:collapse;font-size:12px}` +
	`table.mux th{text-align:left;color:var(--muted);font-weight:500;border-bottom:1px solid var(--line);padding:.3rem .4rem;text-transform:uppercase;font-size:10px;letter-spacing:.3px}` +
	`table.mux td{padding:.3rem .4rem;border-bottom:1px solid rgba(43,49,99,.5);white-space:nowrap}` +
	`td.pk{font-family:ui-monospace,SFMono-Regular,monospace;color:var(--muted)}` +
	`td.barcell{width:36%}.bar{display:block;height:.7rem;border-radius:3px;background:var(--accent);min-width:2px}` +
	`.bar.standby{background:var(--standby)}.bar.warn{background:var(--warn)}` +
	`.bar.recv{background:var(--recv,#3b9)}` +
	`.rtag{display:inline-block;padding:0 .4rem;border-radius:3px;font-size:11px;font-weight:600}` +
	`.rtag.route-direct{background:rgba(60,180,120,.18);color:#2a8}` +
	`.rtag.route-relay{background:rgba(120,140,200,.18);color:#68a}` +
	`.fpk{font-family:ui-monospace,monospace;font-size:11px;word-break:break-all}` +
	`.copy{border-radius:3px;transition:background .15s,color .15s}` +
	`body.js .copy{cursor:pointer}body.js .copy:hover{background:rgba(124,131,255,.14)}` +
	`.copy.copied{background:var(--ok);color:#04120c}.copy.copied::after{content:" ✓ copied";font-size:10px;letter-spacing:.3px}` +
	`.routes{display:flex;flex-direction:column;gap:.5rem;margin:.4rem 0}` +
	`.route{font-size:12px;line-height:1.7;padding:.4rem .6rem;border:1px solid var(--border,#3334);border-radius:5px;overflow-wrap:anywhere}` +
	`.route .rlabel{font-weight:700;margin-right:.3rem}` +
	`.route .hop{color:var(--muted,#8a94a6);white-space:nowrap;font-family:ui-monospace,monospace}` +
	`td.ok{color:var(--ok)}td.warn{color:var(--warn)}td.standby{color:var(--standby)}` +
	`pre.log{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:.7rem;font:11.5px/1.5 ui-monospace,SFMono-Regular,monospace;` +
	`white-space:pre-wrap;word-break:break-word;max-height:26rem;overflow:auto;color:var(--fg)}` +
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
