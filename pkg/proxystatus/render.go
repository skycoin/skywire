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
	// Live up/down throughput meters ride in the header, between the brand and the
	// surface name. They are driven by the inline script, which differences the
	// cumulative byte counters (kept hidden inside the live region, refreshed on
	// every ~1s WebSocket push) and writes the rate here. The header is static
	// (outside the swapped live region), so the script targets these spans
	// document-wide rather than within the live region.
	if live {
		b.WriteString(`<div class="rates" title="live throughput (up / down)">` +
			`<span class="rmeter up"><i>↑</i> <b class="rate" data-rate="up">—/s</b></span>` +
			`<span class="rmeter down"><i>↓</i> <b class="rate" data-rate="down">—/s</b></span></div>`)
	}
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

	// The GPU route-graph view sits between the live region and the control seam.
	// It is STATIC (outside <main id="live">) so its WebGL context/layout survive
	// the live swaps; the live region only restreams the #rgdata JSON it feeds on.
	if live {
		writeGraphSection(&b)
	}

	writeControlSeam(&b, snap)
	writeFooter(&b, snap)

	if live {
		b.WriteString(liveScript)
		b.WriteString(graphScript)
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
	`if(pv&&now>pv.t){var dt=(now-pv.t)/1000,rs=document.querySelectorAll(".rate[data-rate]"),k;` +
	`for(k=0;k<rs.length;k++){var key=rs[k].getAttribute("data-rate"),d=(cur[key]||0)-(pv[key]||0);if(d<0){d=0;}rs[k].textContent=fmtRate(d/dt)+"/s";}}` +
	`pv={up:cur.up||0,down:cur.down||0,t:now};}` +
	// Pin the log pane to the newest line unless the user has scrolled up to read
	// back; restore any horizontal offset and the window offset as before.
	`function apply(h){if(h===last){return;}var el=document.getElementById("live");if(!el){return;}` +
	`var sx=window.scrollX,sy=window.scrollY,lg=el.querySelector("pre.log");` +
	`var tr=el.querySelector(".tree"),trl=tr?tr.scrollLeft:0;` +
	`var lt=lg?lg.scrollTop:0,ll=lg?lg.scrollLeft:0,pin=lg?(lg.scrollTop+lg.clientHeight>=lg.scrollHeight-4):true;` +
	// Preserve the open/closed state of the open-streams <details> across the
	// innerHTML swap (same idea as the scroll/selection guards): the WS restreams
	// the whole live region every ~1s, which would otherwise snap the dropdown shut.
	`var dop=el.querySelector("details.streams"),dopen=dop?dop.open:false;` +
	`el.innerHTML=h;last=h;` +
	`var ds=el.querySelector("details.streams");if(ds){ds.open=dopen;}` +
	`var lg2=el.querySelector("pre.log");if(lg2){lg2.scrollTop=pin?lg2.scrollHeight:lt;lg2.scrollLeft=ll;}` +
	`var tr2=el.querySelector(".tree");if(tr2){tr2.scrollLeft=trl;}meters(el);window.scrollTo(sx,sy);}` +
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

// graphScript drives the GPU route-graph view. It reuses the network
// visualizer's engine WITHOUT any new wasm: it loads the one wasm-visor blob in
// its "netview" role (globalThis.__SKYWIRE_WASM_ROLE__="netview" before go.run,
// exactly as pkg/tpviz/ui/src/cosmos-go-graph.ts does), which publishes the
// generic cosmos-go graph API on globalThis.tpvizGL (pkg/tpviz/wasmgl.Register),
// then drives tpvizGL.init/setData with the route subgraph the page emits in the
// #rgdata JSON. /main.wasm and /wasm_exec.js are served same-origin by the
// skysocks status handler (pkg/skysocks/client.go) out of pkg/wasmhv/wasmbin.
//
// Lazy: the ~3 MB blob is fetched only the first time the graph view is opened,
// so a user who stays on the tree pays nothing. Live: a MutationObserver on the
// live region re-reads #rgdata on each ~1s push and refreshes the hover-tooltip
// data every time, but re-feeds the force layout (setData) ONLY when the
// topology signature changes — so watching bytes tick doesn't relayout the
// graph. The canvas is static (outside the swapped region) so the settled layout
// and WebGL context persist. setRouteView flips a <body> class (static, survives
// swaps) that CSS uses to show/hide the tree vs graph and to light the toggle.
const graphScript = `<script>(function(){` +
	`var booted=false,booting=false,nodes=[],sig=null,io=null;` +
	`function gl(){return window.tpvizGL;}` +
	`function data(){var el=document.getElementById("rgdata");if(!el){return null;}try{return JSON.parse(el.textContent||"null");}catch(e){return null;}}` +
	`function tip(){return document.getElementById("rgtip");}` +
	`function showTip(i,x,y){var n=nodes[i],t=tip();if(!n||!t){return;}t.textContent=n.tip||"";t.style.display="block";t.style.left=(x+14)+"px";t.style.top=(y+14)+"px";}` +
	`function hideTip(){var t=tip();if(t){t.style.display="none";}}` +
	`function onEvent(kind,index){var a=arguments;if(kind==="over"){showTip(index,a[2],a[3]);}else if(kind==="out"||kind==="bgclick"){hideTip();}}` +
	// payload maps the route subgraph JSON to cosmos-go's typed-array setData
	// shape. It carries the DETERMINISTIC seed positions per node (x,y in cosmos
	// space, from routegraph.go seedRouteLayout) as the positions Float32Array —
	// exactly the way tpviz's grouped layout feeds fixed positions — so the graph
	// opens as a root-left → exit-right fan instead of a blob at the origin; the
	// force sim (grouped:false) then only refines it.
	`function payload(d){var n=d.nodes.length,pc=new Array(n),ps=new Float32Array(n),pp=new Float32Array(n*2),idx={},i;` +
	`for(i=0;i<n;i++){var nd=d.nodes[i];idx[nd.id]=i;pc[i]=nd.color;ps[i]=nd.size;pp[i*2]=nd.x||0;pp[i*2+1]=nd.y||0;}` +
	`var L=d.links.length,lk=new Float32Array(L*2),lw=new Float32Array(L),lc=new Array(L),w=0,j;` +
	`for(j=0;j<L;j++){var e=d.links[j],s=idx[e.source],t=idx[e.target];if(s===undefined||t===undefined){continue;}lk[w*2]=s;lk[w*2+1]=t;lc[w]=e.color;lw[w]=e.width;w++;}` +
	`return {positions:pp,pointColors:pc,pointSizes:ps,links:lk.subarray(0,w*2),linkColors:lc.slice(0,w),linkWidths:lw.subarray(0,w),grouped:false,boundaries:[]};}` +
	// On a topology change (or forced first paint) re-feed the layout from the seed
	// positions and, after letting the sim settle briefly, frame it with fit(). A
	// metric-only push (same sig) refreshes tooltips but never relayouts.
	`function apply(force){var d=data();if(!d){return;}nodes=d.nodes||[];var g=gl();if(!g){return;}if(force||d.sig!==sig){sig=d.sig;g.setData(payload(d));if(g.fit){setTimeout(function(){var gg=gl();if(gg){gg.fit();}},1200);}}}` +
	`function observe(){if(io){return;}var live=document.getElementById("live");if(!live){return;}io=new MutationObserver(function(){apply(false);});io.observe(live,{childList:true,subtree:true});}` +
	`function fail(){var c=document.getElementById("rgcanvas");if(c){c.innerHTML='<div class="rgfail">The GPU graph view failed to load (main.wasm). The tree view shows the same routes.</div>';}}` +
	`function ready(){var g=gl();if(g&&g.ready){booted=true;booting=false;if(!g.init("rgcanvas",onEvent)){setTimeout(function(){g.init("rgcanvas",onEvent);apply(true);observe();},80);}else{apply(true);observe();}return true;}return false;}` +
	`function boot(){if(booted||booting){return;}booting=true;window.__SKYWIRE_WASM_ROLE__="netview";` +
	`var s=document.createElement("script");s.src="/wasm_exec.js";s.onerror=function(){booting=false;fail();};` +
	`s.onload=function(){var G=window.Go;if(!G){booting=false;fail();return;}var go=new G();` +
	`WebAssembly.instantiateStreaming(fetch("/main.wasm"),go.importObject).then(function(res){go.run(res.instance);` +
	`var tries=0;(function wait(){if(ready()){return;}if(++tries>150){booting=false;fail();return;}setTimeout(wait,20);})();` +
	`}).catch(function(){booting=false;fail();});};document.head.appendChild(s);}` +
	// Per-section collapse. The log, route tree and route graph each get an
	// independent show/hide toggle; all default to VISIBLE. State rides on a
	// <body> class (sec-hide-<name>) — a STATIC element that survives the ~1s live
	// innerHTML swaps, the same trick the streams-<details> open-state and the
	// static graph canvas use — plus localStorage, so a collapsed section stays
	// collapsed across pushes and page loads. The toggle buttons themselves are
	// re-rendered each push but read their label/state from the body class in CSS.
	`function hidden(name){try{return localStorage.getItem("sh_"+name)==="1";}catch(e){return false;}}` +
	`window.secToggle=function(name){var on=document.body.classList.toggle("sec-hide-"+name);` +
	`try{localStorage.setItem("sh_"+name,on?"1":"0");}catch(e){}` +
	// Revealing the graph the first time boots the wasm lazily; a later reveal just
	// re-fits (the canvas kept its WebGL context and settled layout while hidden).
	`if(name==="graph"&&!on){if(!booted){boot();}else{var g=gl();if(g){setTimeout(function(){g.fit();},60);}}}};` +
	// On-canvas zoom / fit controls, wired to the SAME cosmos-go API the network
	// visualizer's +/−/fit buttons use (tpvizGL.zoomBy / fit; zoom-in 1.3,
	// zoom-out 1/1.3 — see pkg/tpviz/ui/src/events.ts). Wheel-zoom, drag-pan,
	// double-click-zoom and touch pan/pinch are handled natively by the cosmos-go
	// canvas itself, so no extra wiring is needed for those.
	`window.rgZoom=function(f){var g=gl();if(g&&g.zoomBy){g.zoomBy(f);}};` +
	`window.rgFit=function(){var g=gl();if(g&&g.fit){g.fit();}};` +
	`["log","tree","graph"].forEach(function(name){if(hidden(name)){document.body.classList.add("sec-hide-"+name);}});` +
	// Boot the GPU graph now unless the user has it collapsed (then defer to reveal),
	// since the graph is visible by default alongside the tree and log.
	`if(!document.body.classList.contains("sec-hide-graph")){boot();}` +
	`})();</script>`

// writeLiveRegion writes the dynamic status content (pills, per-leg mux, events,
// recent log) shared by Render (page shell) and RenderFragment (SSE push).
func writeLiveRegion(b *strings.Builder, snap Snapshot) {
	if snap.Note != "" {
		fmt.Fprintf(b, `<p class="note">%s</p>`, html.EscapeString(snap.Note))
	}

	writeStreamsSection(b, snap)
	writeRangeSplitSection(b, snap)
	writeMuxSection(b, snap)
	writeLogSection(b, snap)
}

// writeRangeSplitSection renders the transparent HTTP range-split summary when
// the surface reports one. It makes "is range-split firing" a visible line: a
// live pill (ACTIVE with the in-flight split count, or IDLE) plus the cumulative
// shape (splits · chunks · bytes, streams-per-split × chunk-size). Omitted
// entirely when the surface does not range-split.
func writeRangeSplitSection(b *strings.Builder, snap Snapshot) {
	rs := snap.RangeSplit
	if rs == nil || !rs.Enabled {
		return
	}
	state, cls := "idle", "rs-idle"
	if rs.ActiveSplits > 0 {
		state, cls = fmt.Sprintf("active ×%d", rs.ActiveSplits), "rs-active"
	}
	fmt.Fprintf(b, `<p class="rsplit"><b>range-split</b> `+
		`<span class="pill %s">%s</span> `+
		`<span class="rsagg">%d splits · %d chunks · %s · %d streams/split × %s</span></p>`,
		cls, html.EscapeString(state),
		rs.TotalSplits, rs.TotalChunks, html.EscapeString(compactBytes(rs.TotalBytes)),
		rs.StreamsPerSplit, html.EscapeString(compactBytes(uint64(rs.ChunkSize)))) //nolint:gosec // chunkSize>0
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
	if len(snap.Legs) == 0 {
		b.WriteString(`<section><h2>route group · per-leg mux</h2>`)
		b.WriteString(`<p class="empty">No active route group for this surface right now. ` +
			`A leg appears here once a route to a destination is warm.</p></section>`)
		return
	}
	fmt.Fprintf(b, `<section><h2>route group · per-leg mux%s</h2>`, sectionToggle(snap, "tree"))
	var totSent, totRecv uint64
	for _, l := range snap.Legs {
		totSent += l.SentBytes
		totRecv += l.RecvBytes
	}
	// Cumulative sent/recv byte counters, kept HIDDEN inside the live region so
	// each ~1s WebSocket push refreshes them; the inline script differences them
	// into the live up/down RATE meters shown in the header. No visible leg-census
	// summary line here — the route tree below carries per-route state/rtt/bw.
	fmt.Fprintf(b, `<span class="stat" data-bytes="up" data-val="%d" hidden></span>`, totSent)
	fmt.Fprintf(b, `<span class="stat" data-bytes="down" data-val="%d" hidden></span>`, totRecv)

	// The ASCII bilateral route tree is wrapped in .muxtree so its own SHOW/HIDE
	// toggle (in the h2 above) can collapse it independently — it is one of three
	// always-visible-by-default sections (tree, GPU graph, log) shown stacked on
	// the page, not a mutually-exclusive view. The GPU graph lives in its own
	// static section (writeGraphSection) whose WebGL context must survive the ~1s
	// live-region swaps, fed the route subgraph from the #rgdata JSON emitted below
	// (restreamed with the live region).
	b.WriteString(`<div class="muxtree">`)

	// The route tree itself: ONE shared bilateral model (pkg/proxystatus.RouteTree)
	// rendered by pkg/bitree — the exact model + geometry `skywire cli proxy tree`
	// prints, so the page and the terminal never drift. Root = this visor (right-
	// anchored over the spine); each active route is a right branch (its hop chain,
	// dead legs pruned) with a left summary (R[n], state glyph, route rtt,
	// bandwidth) padded into fixed columns. A label-header row (TreeHeader) rides
	// above the tree as a template/legend that lines up with the columns beneath.
	// htmlStyleCell decorates cells (colored state dot, click-to-copy PKs/tpids)
	// without disturbing column alignment (layout is computed from the plain text).
	// The tree is NOT boxed: it flows with the surrounding text at page margins and
	// scrolls horizontally in its OWN overflow container only if a long PK runs wide.
	hLeft, hLabel, hCols := TreeHeader()
	// Per-hop color classes, keyed by PK: the exit red, each intermediate hop
	// LEVEL its own hue (see hopClassMap). Captured in the StyleCell closure so the
	// PK label cells can be wrapped without disturbing bitree's plain-text layout.
	hopClasses := hopClassMap(snap)
	// When more than one stream is present, name the two multiplexing LAYERS above
	// the tree so the stream-boundary nodes and per-stream leg bands below are
	// self-explanatory.
	if len(snap.Tunnels) > 1 {
		writeLayerLegend(b)
	}
	b.WriteString(`<div class="tree">`)
	b.WriteString(`<pre class="bitree">`)
	b.WriteString(bitree.Render(RouteTree(snap), bitree.Options{
		StyleCell: func(text string, kind bitree.CellKind) string {
			return htmlStyleCell(text, kind, hopClasses)
		},
		HeaderLeft:  hLeft,
		HeaderLabel: hLabel,
		HeaderCols:  hCols,
	}))
	b.WriteString(`</pre>`)
	b.WriteString(`</div>`)
	// Legend BELOW the tree: the state words are themselves colored (source accent,
	// active green, standby amber) — no separate swatch dot.
	writeTreeLegend(b)
	b.WriteString(`<p class="hint">The same tree prints from <code>skywire cli proxy tree</code>; for a live chart use ` +
		`<code>skywire cli proxy mux plot</code>.</p>`)
	b.WriteString(`</div>`) // .muxtree
	// The route subgraph the GPU graph view renders, as inert JSON inside the live
	// region so each ~1s WebSocket push restreams it; the static driver
	// (graphScript) re-reads it and re-feeds cosmos-go only when the topology
	// signature changes (routegraph.go topoSig), so live metric updates don't jerk
	// the force layout. Skysocks only (the only surface that serves the wasm blob).
	if snap.Surface == SurfaceSkysocks {
		fmt.Fprintf(b, `<script type="application/json" id="rgdata">%s</script>`, RouteGraphJSON(snap))
	}
	b.WriteString(`</section>`)
}

// sectionToggle returns the inline SHOW/HIDE button that rides in a section's h2
// (skysocks only — the only surface with the graphScript that defines secToggle).
// Clicking it flips a sec-hide-<name> class on <body> — a STATIC element that
// survives the live-region innerHTML swaps — so the collapsed/expanded choice
// sticks across the ~1s pushes even though the button is re-rendered each push;
// its "hide"/"show" label is derived from the body class in CSS. secToggle
// (graphScript) also persists the choice in localStorage. All sections default to
// VISIBLE (no body class). Returns "" for non-skysocks surfaces, which have no
// graphScript and no live swaps.
func sectionToggle(snap Snapshot, name string) string {
	if snap.Surface != SurfaceSkysocks {
		return ""
	}
	return `<button type="button" class="sectoggle" data-sec="` + name + `" ` +
		`onclick="secToggle('` + name + `')" aria-label="show or hide this section"></button>`
}

// writeGraphSection emits the STATIC route-graph section (skysocks only): the
// cosmos-go canvas container, a color legend, and the fixed hover tooltip. It
// lives OUTSIDE <main id="live"> on purpose — the WebGL context and the settled
// force layout must persist across the live region's ~1s innerHTML swaps, so the
// container is never re-created; only the #rgdata JSON inside the live region
// updates, and the driver re-feeds the engine in place. Visible by DEFAULT,
// stacked below the route tree; its own SHOW/HIDE toggle collapses the .rgbody
// (canvas + legend + hint) via a body class, leaving the heading and the WebGL
// context in place so a later reveal just re-fits.
func writeGraphSection(b *strings.Builder) {
	b.WriteString(`<section id="rgraphsec" aria-label="route graph">`)
	b.WriteString(`<h2>route graph<button type="button" class="sectoggle" data-sec="graph" ` +
		`onclick="secToggle('graph')" aria-label="show or hide this section"></button></h2>`)
	b.WriteString(`<div class="rgbody">`)
	// The cosmos-go engine appends its <canvas> INTO #rgcanvas (it never clears the
	// container), so these overlay controls coexist with it. Same controls the
	// network visualizer offers: zoom in / out / fit-all. Wheel-zoom and drag-pan
	// are native to the canvas and need no wiring.
	b.WriteString(`<div id="rgcanvas" class="rgcanvas">` +
		`<div class="rgctl" role="group" aria-label="graph controls">` +
		`<button type="button" onclick="rgZoom(1.3)" title="zoom in" aria-label="zoom in">+</button>` +
		`<button type="button" onclick="rgZoom(1/1.3)" title="zoom out" aria-label="zoom out">−</button>` +
		`<button type="button" onclick="rgFit()" title="fit to view" aria-label="fit to view">⤢</button>` +
		`</div></div>`)
	// Legend: the same color language as the tree (source/exit/hop by depth,
	// per-stream edge accents) plus active/standby edge styling. The swatch words
	// carry their own color so the coding is self-documenting.
	b.WriteString(`<div class="rglegend">` +
		`<span class="rgl src">● source</span>` +
		`<span class="rgl exit">● exit</span>` +
		`<span class="rgl hop1">● hop</span>` +
		`<span class="rgl s0">— stream 0</span>` +
		`<span class="rgl s1">— stream 1</span>` +
		`<span class="rgl standby">— standby (dim)</span>` +
		`</div>`)
	b.WriteString(`<p class="hint">GPU force-directed graph of this proxy's routes — the same engine (cosmos-go) ` +
		`as the network visualizer, driving only THIS visor, its hops and the exit. It opens seeded ` +
		`root-left → exit-right with the streams fanned, then the force sim refines it. Hover a node for its ` +
		`transports and per-leg detail; drag to pan, scroll to zoom. Needs WebGL; the tree above shows the ` +
		`same routes without it.</p>`)
	b.WriteString(`</div>`) // .rgbody
	b.WriteString(`</section>`)
	b.WriteString(`<div id="rgtip" class="rgtip" role="tooltip"></div>`)
}

// writeTreeLegend prints a compact legend BELOW the route tree. The words
// themselves are colored in their tree colors — "source · this visor" in the
// source accent, the hop-DEPTH colors ("hop 1/2/3" per level) and "exit" (red)
// matching the PK coloring, and "active"/"standby" in their state colors —
// instead of a separate swatch dot beside each word. The column hints live in
// the label-header row above the tree (TreeHeader), so the legend only names the
// color coding. Dead legs are pruned, so there is no dead entry.
// writeLayerLegend names the TWO route-multiplexing layers the tree shows, so
// the stream-boundary header nodes (▚) and the per-stream leg accent bands (▏sN)
// read as two distinct kinds of multiplexing. Rendered only when more than one
// stream is present (single-stream trees carry no stream chrome to explain).
func writeLayerLegend(b *strings.Builder) {
	b.WriteString(`<div class="llayers">` +
		`<span class="llayer stream-l"><b>` + StreamHeaderGlyph + ` stream</b> · independent route groups (--tunnels)</span>` +
		`<span class="llayer leg-l"><b>` + StreamBandGlyph + `sN leg</b> · packet-striping mux within a stream</span>` +
		`</div>`)
}

func writeTreeLegend(b *strings.Builder) {
	b.WriteString(`<div class="tlegend">` +
		`<span class="lgnd src">source · this visor</span>` +
		`<span class="lgnd hop-l1">hop 1</span>` +
		`<span class="lgnd hop-l2">hop 2</span>` +
		`<span class="lgnd hop-l3">hop 3</span>` +
		`<span class="lgnd hop-exit">exit</span>` +
		`<span class="lgnd ok">active ●</span>` +
		`<span class="lgnd standby">standby ○</span>` +
		`</div>`)
}

// htmlStyleCell is the page's bitree StyleCell: it decorates each cell with the
// page's existing color/interaction classes WITHOUT changing display width (the
// spans it injects are zero-width in the monospace <pre>, and bitree computes
// layout from the plain text before this runs). PKs and transport ids become
// click-to-copy; the state dot in the left summary is colored; transport
// columns are muted.
func htmlStyleCell(text string, kind bitree.CellKind, hopClasses map[string]string) string {
	switch kind {
	case bitree.CellRoot:
		return `<span class="src">` + copyablePK(text) + `</span>`
	case bitree.CellLabel:
		// A stream-boundary header (the STREAM layer): a different KIND of node than a
		// hop PK — give it the stream's accent + badge styling rather than PK coloring.
		if strings.HasPrefix(strings.TrimSpace(text), StreamHeaderGlyph) {
			return `<span class="streamhdr ` + streamAccentClass(streamIdxOf(text, "stream ")) + `">` +
				html.EscapeString(text) + `</span>`
		}
		// A hop PK: color it by its role/depth (exit red, intermediates by level).
		// The class wraps copyablePK so the click-to-copy PK is unchanged; the span
		// is zero-width text, so bitree's monospace column layout is preserved. The
		// pk-color CSS (pre.bitree .hop-… code.fpk) mirrors the .src accent selector.
		if cls := hopClasses[strings.TrimSpace(text)]; cls != "" {
			return `<span class="` + cls + `">` + copyablePK(text) + `</span>`
		}
		return copyablePK(text)
	case bitree.CellColumn:
		return htmlTreeColumn(text)
	case bitree.CellLeft:
		return htmlLegSummary(text)
	case bitree.CellHeaderLeft, bitree.CellHeaderLabel, bitree.CellHeaderColumn:
		// The label-header row: template labels standing in for PKs/values, styled
		// as a muted legend so it reads as a column header, not live data.
		return `<span class="thead">` + html.EscapeString(text) + `</span>`
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
	// Peel off an optional leading per-STREAM accent band ("▏s0 "): color it with
	// that stream's accent (the STREAM layer), OUTSIDE the state-tinted .lsum span,
	// so the stream accent and the leg's active/standby state coding compose instead
	// of one overriding the other. The leading padding spaces (bitree's right-
	// justify) stay ahead of the band. When absent (single-stream view) nothing is
	// peeled and the summary is styled exactly as before.
	bandHTML := ""
	if i := strings.Index(text, StreamBandGlyph); i >= 0 {
		rest := text[i:]
		if sp := strings.IndexByte(rest, ' '); sp > 0 {
			tag := rest[:sp] // "▏s0"
			bandHTML = html.EscapeString(text[:i]) +
				`<span class="stream ` + streamAccentClass(streamIdxOf(tag, StreamBandGlyph+"s")) + `">` +
				html.EscapeString(tag) + `</span> `
			text = rest[sp+1:] // remainder after the tag + its space
		}
	}
	cls, glyph := "ok", GlyphActive
	if strings.Contains(text, GlyphStandby) {
		cls, glyph = "standby", GlyphStandby
	}
	esc := html.EscapeString(text) // the glyphs/arrows are not HTML-special
	dot := `<span class="tstate ` + cls + `">` + glyph + `</span>`
	esc = strings.Replace(esc, glyph, dot, 1)
	return bandHTML + `<span class="lsum ` + cls + `">` + esc + `</span>`
}

// streamIdxOf extracts the stream index that follows prefix in s (e.g. "stream "
// in a header label, or "▏s" in a leg band) — the run of digits right after the
// prefix. Returns 0 when no index is found, so an unparsable label still gets a
// valid (first) accent rather than no class.
func streamIdxOf(s, prefix string) int {
	i := strings.Index(s, prefix)
	if i < 0 {
		return 0
	}
	j := i + len(prefix)
	k := j
	for k < len(s) && s[k] >= '0' && s[k] <= '9' {
		k++
	}
	if k == j {
		return 0
	}
	n := 0
	for _, c := range s[j:k] {
		n = n*10 + int(c-'0')
	}
	return n
}

// streamAccentClass maps a stream index to its stable CSS accent class,
// cycling the small palette (--stream0…) so the accent is stable per index
// across the ~1s live re-renders.
func streamAccentClass(idx int) string {
	return fmt.Sprintf("s%d", ((idx%streamAccentCount)+streamAccentCount)%streamAccentCount)
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
// age, plus this stream's own up/down byte totals and smoothed transfer rate).
// It is a native <details> so it stays collapsed by default and costs nothing to
// skip. Each row shows the stream's ↑/↓ bytes and ↑/↓ rate metered by the splice
// loop carrying it — true per-stream totals, distinct from the route-group-wide
// sent/recv totals in the mux section above. The latency column carries the
// ROUTE-GROUP latency (the stream's session RTT to the exit), labeled as such —
// yamux exposes no per-stream round-trip, so it is not faked per stream. The
// summary line carries the aggregate ↑/↓ across all open streams.
func writeStreamsSection(b *strings.Builder, snap Snapshot) {
	if len(snap.Streams) == 0 {
		return
	}
	var aggSent, aggRecv uint64
	for _, s := range snap.Streams {
		aggSent += s.SentBytes
		aggRecv += s.RecvBytes
	}
	fmt.Fprintf(b, `<details class="streams"><summary>open streams <b>%d</b>`+
		`<span class="sagg"><i class="up">↑</i> %s <i class="down">↓</i> %s</span></summary>`,
		len(snap.Streams), html.EscapeString(compactBytes(aggSent)), html.EscapeString(compactBytes(aggRecv)))
	b.WriteString(`<table class="strm"><thead><tr>` +
		`<th>id</th><th>target</th><th>age</th>` +
		`<th class="num">↑ bytes</th><th class="num">↓ bytes</th>` +
		`<th class="num">↑ rate</th><th class="num">↓ rate</th>` +
		`<th class="num" title="route-group latency (session RTT to the exit) — not per-stream">rg rtt</th>` +
		`</tr></thead><tbody>`)
	for _, s := range snap.Streams {
		fmt.Fprintf(b, `<tr><td>%d</td><td>%s</td><td>%s</td>`+
			`<td class="num up">%s</td><td class="num down">%s</td>`+
			`<td class="num up">%s</td><td class="num down">%s</td>`+
			`<td class="num rtt">%s</td></tr>`,
			s.ID, html.EscapeString(orDash(s.Target)), html.EscapeString(compactAge(s.AgeMS)),
			html.EscapeString(compactBytes(s.SentBytes)), html.EscapeString(compactBytes(s.RecvBytes)),
			html.EscapeString(compactRate(s.SentRateBps)), html.EscapeString(compactRate(s.RecvRateBps)),
			html.EscapeString(routeRTTCompact(s.LatencyMS)))
	}
	b.WriteString(`</tbody></table>`)
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
	fmt.Fprintf(b, `<section><h2>route, transport &amp; log%s</h2>`, sectionToggle(snap, "log"))
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

// css: the dark default is tuned so every text token clears WCAG AA (≥4.5:1 for
// body text) against --bg/--card — notably --muted (#a2a8cc ≈ 8:1 on --bg),
// which every low-emphasis label (pills, table headers, .hint/.empty, footer,
// log tail) uses. The light block re-darkens --muted AND the status colors
// (--ok/--warn/--standby), whose dark-mode brights are illegible on a light
// background, so the same ≥4.5:1 floor holds in both schemes. The accent
// gradient and overall identity are unchanged.
const css = `:root{--bg:#0b0d17;--fg:#c7cbe6;--muted:#a2a8cc;--accent:#7c83ff;--accent2:#a06bff;--ok:#4ad9a4;--warn:#ff6b8a;--err:#ff5c5c;--cyan:#3fd0d8;--standby:#e0b64a;--card:#131629;--line:#2b3163;` +
	// Route-tree hop colors: the EXIT (any leg's destination) red, and each
	// intermediate hop LEVEL its own hue (level 1..4, then the classes cycle). All
	// chosen for legibility on the tree's dark ground; the light block re-darkens
	// them below so they clear the same contrast floor on a light background.
	`--hop-exit:#ff5c5c;--hop1:#4fc3f7;--hop2:#c48cff;--hop3:#ffb74d;--hop4:#4dd0a0;` +
	// Per-STREAM accents (the stream/tunnel layer): a small palette cycled by stream
	// index (stream N → N % 5), stable per index across live re-renders. Chosen to
	// stay clear of the state/exit hues (green/amber/red) so the stream accent never
	// reads as active/standby or exit, and legible on the dark ground; the light
	// block re-darkens them below to hold the same contrast floor.
	`--stream0:#7c9cff;--stream1:#ff9e64;--stream2:#e879c9;--stream3:#4dd0e1;--stream4:#c3a6ff}` +
	`*{box-sizing:border-box}html,body{margin:0;background:var(--bg);color:var(--fg);font:13.5px/1.55 system-ui,-apple-system,Segoe UI,Roboto,sans-serif}` +
	`body{max-width:60rem;margin:0 auto;padding:1.2rem 1rem 3rem}` +
	`header{display:flex;align-items:baseline;gap:.8rem;flex-wrap:wrap;border-bottom:1px solid var(--line);padding-bottom:.7rem}` +
	`.brand b{font-weight:600;letter-spacing:.4px;background:linear-gradient(90deg,var(--accent),var(--accent2));-webkit-background-clip:text;background-clip:text;color:transparent}` +
	`.brand{font-size:1rem}.surface{margin-left:.2rem;font:600 1.1rem ui-monospace,SFMono-Regular,monospace;color:#e7e9ff}` +
	// Live up/down throughput meters in the header, between the brand and the
	// surface name. Pushed to the right edge (margin-left:auto) so the brand sits
	// left and the meters+surface group sits right.
	`.rates{margin-left:auto;display:inline-flex;gap:.7rem;align-items:baseline}` +
	`.rmeter{font-size:12px;color:var(--muted)}.rmeter i{font-style:normal;margin-right:.15rem}` +
	`.rmeter.up i{color:var(--ok)}.rmeter.down i{color:var(--cyan)}` +
	`.rate{color:var(--accent);font-weight:600;font-size:12px;font-family:ui-monospace,SFMono-Regular,monospace}` +
	`.wsstat{display:inline-flex;align-items:center;gap:.35rem;margin-left:.7rem;font-size:11px;text-transform:uppercase;letter-spacing:.4px;color:var(--muted)}` +
	`.wsstat .dot{width:.5rem;height:.5rem;border-radius:50%;background:var(--muted);flex:none}` +
	`.wsstat.ok{color:var(--ok)}.wsstat.ok .dot{background:var(--ok);box-shadow:0 0 6px var(--ok);animation:pulse 2s infinite}` +
	`.wsstat.warn{color:var(--warn)}.wsstat.warn .dot{background:var(--warn)}` +
	`.wsstat.wait{color:var(--standby)}.wsstat.wait .dot{background:var(--standby)}` +
	`@keyframes pulse{0%,100%{opacity:1}50%{opacity:.35}}` +
	`h2{font-size:.95rem;margin:1.6rem 0 .5rem;color:#e7e9ff;font-weight:600}h2 small{color:var(--muted);font-weight:400;font-size:11px;margin-left:.4rem}` +
	`.note{color:var(--standby)}.empty,.hint{color:var(--muted);font-size:12px}` +
	`.bar{display:block;height:.6rem;border-radius:3px;background:var(--accent);min-width:2px}` +
	`.bar.ok{background:var(--ok)}.bar.standby{background:var(--standby)}.bar.warn{background:var(--warn)}` +
	`.bar.recv{background:var(--recv,#3b9)}` +
	`.fpk{font-family:'Mononoki',ui-monospace,monospace;font-size:11px;word-break:break-all}` +
	`.ftid{font-family:'Mononoki',ui-monospace,monospace;font-size:10.5px;color:var(--muted)}` +
	`.copy{border-radius:3px;transition:background .15s,color .15s}` +
	`body.js .copy{cursor:pointer}body.js .copy:hover{background:rgba(124,131,255,.14)}` +
	`.copy.copied{background:var(--ok);color:#04120c}.copy.copied::after{content:" ✓ copied";font-size:10px;letter-spacing:.3px}` +
	// One unified route tree (root = local visor; branches = first-hop peers). The
	// tree is NOT boxed: it flows with the surrounding prose at the same page
	// margins, centered on the page like the text above and below it. It scrolls
	// horizontally only if a long full PK runs wider than the page, and that scroll
	// is confined to the tree's OWN overflow container (never the page body); the
	// live-swap captures and restores that container's scrollLeft.
	// The tree full-bleeds to the viewport width (breaking out of the 60rem body)
	// so a wide full-PK route set shows whole without a "little" scroll box; the
	// centered body constrains everything else. overflow-x stays only as a safety
	// for a set wider than the whole viewport.
	// text-align:center centers the inline-block <pre> within the full-bleed
	// container so a normal-width tree sits in the middle of the page; the pre
	// keeps its own text-align:left so the box-drawing branches still line up.
	`.tree{margin:.7rem 0;width:100vw;margin-left:calc(50% - 50vw);padding:0 1rem;overflow-x:auto;overflow-y:hidden;text-align:center}` +
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
	// font-variant-numeric:tabular-nums renders every digit at equal advance width
	// (belt-and-suspenders with the fixed-width Go padding of the mutable rtt/byte
	// fields) so live value updates never shift the aligned tree columns even if a
	// fallback font's digits are proportional.
	`pre.bitree{display:inline-block;text-align:left;font-family:'DejaVu Sans Mono','Liberation Mono','Noto Sans Mono',ui-monospace,'Cascadia Mono','Segoe UI Mono',Menlo,Consolas,'Courier New',monospace;font-variant-numeric:tabular-nums;font-size:11.5px;line-height:1;letter-spacing:0;margin:0 auto;white-space:pre;color:var(--fg)}` +
	`pre.bitree code,pre.bitree span{font:inherit;color:inherit;line-height:1;letter-spacing:0;white-space:pre;word-break:normal;background:none;border:0;padding:0}` +
	`pre.bitree code.fpk{color:var(--fg)}pre.bitree .src code.fpk{color:var(--accent);font-weight:600}` +
	// Hop coloring, mirroring the .src accent selector: the exit PK red, each
	// intermediate hop LEVEL its own hue. The span wraps copyablePK, so this
	// direct-child selector tints the PK code without reaching the trailing
	// transport columns (those sit in their own .thop/.ftid spans).
	`pre.bitree .hop-exit code.fpk{color:var(--hop-exit);font-weight:600}` +
	`pre.bitree .hop-l1 code.fpk{color:var(--hop1)}pre.bitree .hop-l2 code.fpk{color:var(--hop2)}` +
	`pre.bitree .hop-l3 code.fpk{color:var(--hop3)}pre.bitree .hop-l4 code.fpk{color:var(--hop4)}` +
	`pre.bitree code.ftid{color:var(--muted)}pre.bitree .tcol{color:var(--muted)}` +
	`pre.bitree .lsum.ok{color:var(--ok)}pre.bitree .lsum.standby{color:var(--standby)}` +
	// STREAM layer coloring. The stream-boundary HEADER node reads as a badge — bold,
	// its stream accent, a faint accent underline — clearly a different KIND of node
	// than a leg/hop row. Each leg's leading "▏sN" band carries the same per-stream
	// accent, tying the leg to its stream WITHOUT touching the state (●/○) or hop-PK
	// coloring beside it, so the two layers' signals compose.
	`pre.bitree .streamhdr{font-weight:700;letter-spacing:.3px;border-bottom:1px solid currentColor}` +
	`pre.bitree .stream{font-weight:700}` +
	`pre.bitree .streamhdr.s0,pre.bitree .stream.s0{color:var(--stream0)}` +
	`pre.bitree .streamhdr.s1,pre.bitree .stream.s1{color:var(--stream1)}` +
	`pre.bitree .streamhdr.s2,pre.bitree .stream.s2{color:var(--stream2)}` +
	`pre.bitree .streamhdr.s3,pre.bitree .stream.s3{color:var(--stream3)}` +
	`pre.bitree .streamhdr.s4,pre.bitree .stream.s4{color:var(--stream4)}` +
	// Two-layer legend above the tree: names the stream (route-group) layer and the
	// leg (packet-striping) layer so the ▚ header nodes and ▏sN leg bands are
	// self-explanatory. The markers themselves carry the layer's accent.
	`.llayers{display:flex;flex-wrap:wrap;gap:.3rem 1.4rem;font-size:11px;margin:.5rem 0 .2rem}` +
	`.llayer{color:var(--muted)}.llayer b{font-family:ui-monospace,SFMono-Regular,monospace}` +
	`.llayer.stream-l b{color:var(--stream0)}.llayer.leg-l b{color:var(--stream1)}` +
	// Tree legend, BELOW the tree: the WORDS themselves are colored in their state
	// colors (source accent, active green, standby amber) — no separate swatch dot
	// beside each word. The column hints now live in the label-header row above the
	// tree (.thead), so the legend only names the color coding.
	`.tlegend{display:flex;flex-wrap:wrap;gap:.15rem 1.1rem;font-size:10px;text-transform:uppercase;letter-spacing:.3px;margin-top:.4rem}` +
	`.lgnd{display:inline-flex;align-items:center;white-space:nowrap;font-weight:600}` +
	`.lgnd.src{color:var(--accent)}.lgnd.ok{color:var(--ok)}.lgnd.standby{color:var(--standby)}` +
	// Hop-color legend entries: the WORD itself carries the tree's hop hue so the
	// coloring is self-documenting (exit red, hop 1/2/3 by level).
	`.lgnd.hop-exit{color:var(--hop-exit)}.lgnd.hop-l1{color:var(--hop1)}` +
	`.lgnd.hop-l2{color:var(--hop2)}.lgnd.hop-l3{color:var(--hop3)}` +
	// The label-header row rendered inside the tree <pre>: template labels in place
	// of PKs/values, muted so it reads as a column legend rather than live data.
	`pre.bitree .thead{color:var(--muted);opacity:.9}` +
	// State dot in the left per-route summary (replaces any active/standby word):
	// color-blind-safe by shape (● active / ○ standby) as well as hue.
	`pre.bitree .tstate{font-weight:700}` +
	`pre.bitree .tstate.ok{color:var(--ok)}pre.bitree .tstate.standby{color:var(--standby)}` +
	// Recent log: a black terminal pane. The user can drag it taller/shorter
	// (resize:vertical) — a generous default height, a sensible floor, and its own
	// vertical scroll. Lines wrap (pre-wrap + break-word) so width stays page-level.
	// The background is black in BOTH page themes (it reads as a terminal), so the
	// token colors below are pinned to fixed bright values rather than the theme
	// tokens (whose light-mode variants would be illegible on black).
	`pre.log{background:#000;border:1px solid var(--line);border-radius:8px;padding:.7rem;font:11.5px/1.5 'Mononoki',ui-monospace,SFMono-Regular,monospace;` +
	`white-space:pre-wrap;word-break:break-word;height:32rem;min-height:10rem;max-height:80vh;overflow:auto;resize:vertical;color:#c7cbe6}` +
	// Per-token log coloring, matching `proxy start --verbose` (pkg/logging
	// printColored) on the black pane: the [timestamp] grey, the LEVEL word its
	// level color (INFO green, WARN amber, ERROR/FATAL/PANIC a real red, DEBUG blue,
	// TRACE grey), the "[prefix]:" token cyan, the message default, each field key
	// tinted to the level color. Fixed hexes so the terminal look holds in both themes.
	`pre.log .ll-info{color:#4ad9a4}pre.log .ll-warn{color:#e0b64a}pre.log .ll-error{color:#ff5c5c}` +
	`pre.log .ll-debug{color:#7c83ff}pre.log .ll-trace{color:#a2a8cc}` +
	`pre.log .ll-ts{color:#a2a8cc}pre.log .ll-prefix{color:#3fd0d8}` +
	// Per-stream detail (expandable) behind the "N open stream(s)" count.
	`details.streams{margin:.5rem 0}details.streams summary{cursor:pointer;font-size:12px;color:var(--muted)}` +
	`details.streams summary b{color:var(--fg);margin-left:.2rem}` +
	// Aggregate ↑/↓ across all open streams, shown in the summary line beside the
	// count. The arrows carry the up (green) / down (cyan) accents used elsewhere.
	`details.streams summary .sagg{margin-left:.6rem;font-family:'Mononoki',ui-monospace,SFMono-Regular,monospace;font-size:11px}` +
	`details.streams summary .sagg i{font-style:normal;margin:0 .1rem 0 .5rem}` +
	`details.streams summary .sagg i.up{color:var(--ok)}details.streams summary .sagg i.down{color:var(--cyan)}` +
	`table.strm{border-collapse:collapse;font-size:11.5px;margin:.4rem 0;font-family:'Mononoki',ui-monospace,SFMono-Regular,monospace}` +
	`table.strm th,table.strm td{text-align:left;padding:.1rem 1rem .1rem 0;color:var(--fg);white-space:nowrap}` +
	// Numeric columns (bytes / rate / rtt) are right-aligned and tabular so the
	// figures line up as they update live; the ↑ columns take the up accent, ↓ the
	// down/cyan accent, and the route-group rtt stays muted (it is not per-stream).
	`table.strm td.num,table.strm th.num{text-align:right;font-variant-numeric:tabular-nums}` +
	`table.strm td.up{color:var(--ok)}table.strm td.down{color:var(--cyan)}table.strm td.rtt{color:var(--muted)}` +
	`table.strm th{color:var(--muted);text-transform:uppercase;font-size:9.5px;letter-spacing:.4px}` +
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
	// Per-section SHOW/HIDE toggle button that rides in each section's h2. Its
	// "hide"/"show" label comes from the <body> collapse class (a static hook that
	// survives the ~1s live swaps) via ::after, so it reflects state without any
	// per-render bookkeeping.
	`.sectoggle{margin-left:.55rem;font:inherit;font-size:10px;text-transform:uppercase;letter-spacing:.4px;vertical-align:middle;padding:.05rem .5rem;border:1px solid var(--line);border-radius:6px;background:transparent;color:var(--muted);cursor:pointer}` +
	`.sectoggle:hover{border-color:var(--accent);color:var(--fg)}` +
	`.sectoggle::after{content:"hide"}` +
	`body.sec-hide-tree .sectoggle[data-sec="tree"]::after,body.sec-hide-log .sectoggle[data-sec="log"]::after,body.sec-hide-graph .sectoggle[data-sec="graph"]::after{content:"show"}` +
	// The route tree, GPU graph and log are all shown stacked and VISIBLE by
	// default; each collapses independently via its body class, hiding only its
	// body (never its heading, and — for the tree — never the inert #rgdata JSON,
	// which sits outside .muxtree so it keeps streaming to the still-live graph).
	// The canvas is a fixed, user-resizable dark GL surface (cosmos paints its own
	// dark background).
	`#rgraphsec{margin-top:.2rem}` +
	`body.sec-hide-tree .muxtree{display:none}` +
	`body.sec-hide-log pre.log{display:none}` +
	`body.sec-hide-graph .rgbody{display:none}` +
	`.rgcanvas{position:relative;width:100%;height:30rem;min-height:18rem;border:1px solid var(--line);border-radius:8px;background:#0b1020;overflow:hidden;resize:vertical}` +
	// On-canvas zoom/fit controls, overlaid top-right above the GL canvas (the
	// engine's canvas sits at the default z, these ride above it). Wheel-zoom and
	// drag-pan are native to the canvas; these mirror the network visualizer's
	// +/−/fit buttons. The dark ground is fixed (the GL canvas is always dark) so
	// the control colors are pinned rather than theme tokens.
	`.rgctl{position:absolute;top:.45rem;right:.45rem;z-index:6;display:flex;flex-direction:column;gap:.3rem}` +
	`.rgctl button{width:1.8rem;height:1.8rem;padding:0;font:600 15px/1 ui-monospace,SFMono-Regular,monospace;display:flex;align-items:center;justify-content:center;border:1px solid #2b3163;border-radius:6px;background:rgba(11,16,32,.82);color:#c7cbe6;cursor:pointer}` +
	`.rgctl button:hover{border-color:#7c83ff;color:#7c83ff;background:rgba(11,16,32,.96)}` +
	`.rgfail{padding:1rem;color:var(--warn);font-size:12px}` +
	// Node hover tooltip: a fixed, pointer-transparent monospace panel positioned
	// at the cursor by the driver; pre-wrap keeps the multi-line leg detail + full
	// (untruncated) PK readable.
	`.rgtip{display:none;position:fixed;z-index:50;max-width:36rem;white-space:pre-wrap;word-break:break-all;pointer-events:none;background:rgba(6,8,20,.96);color:#c7cbe6;border:1px solid var(--line);border-radius:6px;padding:.4rem .55rem;font:11px/1.45 ui-monospace,SFMono-Regular,monospace;box-shadow:0 4px 18px rgba(0,0,0,.5)}` +
	// Graph legend: the swatch words carry their own tree/stream colors.
	`.rglegend{display:flex;flex-wrap:wrap;gap:.15rem 1.1rem;font-size:10px;text-transform:uppercase;letter-spacing:.3px;margin-top:.45rem}` +
	`.rgl{font-weight:600;white-space:nowrap}` +
	`.rgl.src{color:var(--accent)}.rgl.exit{color:var(--hop-exit)}.rgl.hop1{color:var(--hop1)}` +
	`.rgl.s0{color:var(--stream0)}.rgl.s1{color:var(--stream1)}.rgl.standby{color:var(--standby)}` +
	`@media(prefers-color-scheme:light){:root{--bg:#f6f7fb;--fg:#1c1e26;--muted:#4a4f63;--card:#fff;--line:#d3d6e4;--accent:#4149d6;--accent2:#7b3fd0;--ok:#0a7a4c;--warn:#c02a48;--err:#c8102e;--cyan:#0a6c74;--standby:#7a5c00;` +
	// Re-darken the hop hues for a light background so exit + each hop level stay
	// legible (the dark-mode brights wash out on white).
	`--hop-exit:#c8102e;--hop1:#0a6fb0;--hop2:#6a3fd0;--hop3:#a85a00;--hop4:#0a7a4c;` +
	// Re-darken the stream accents for a light background (the dark-mode pastels
	// wash out on white) so each stream stays legible at the same contrast floor.
	`--stream0:#3a5bd0;--stream1:#b85c1a;--stream2:#b03592;--stream3:#0a7c88;--stream4:#6a3fd0}` +
	`h2,.surface{color:#1c1e26}}`
