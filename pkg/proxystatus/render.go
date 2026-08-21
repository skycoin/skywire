// Package proxystatus pkg/proxystatus/render.go c4-app-web
package proxystatus

import (
	"fmt"
	"html"
	"strings"
)

// refreshSeconds is how often the status page re-fetches itself. The page is a
// point-in-time Snapshot rendered server-side (no JS/websocket), so a meta
// refresh is how it stays live — frequent enough to watch a route warm, slow
// enough not to thrash the proxy.
const refreshSeconds = 4

// maxLogLines caps how many recent log lines the page renders, newest at the
// bottom (terminal-tail order). The Provider may return fewer.
const maxLogLines = 200

// Render returns the full, self-contained HTML status page for snap. All
// interpolated values are HTML-escaped; the page loads no external resource
// (matching the proxies' strict no-network serving context).
func Render(snap Snapshot) []byte {
	var b strings.Builder
	surface := html.EscapeString(string(snap.Surface))
	b.WriteString("<!doctype html><html lang=en><head><meta charset=utf-8>")
	b.WriteString(`<meta name=viewport content="width=device-width, initial-scale=1">`)
	fmt.Fprintf(&b, `<meta http-equiv="refresh" content="%d">`, refreshSeconds)
	fmt.Fprintf(&b, "<title>%s proxy status · skywire</title><style>%s</style></head><body>", surface, css)

	// Header + brand.
	b.WriteString(`<header><div class="brand"><b>skywire</b> proxy status</div>`)
	fmt.Fprintf(&b, `<div class="surface">%s</div></header>`, surface)

	// Status pills.
	b.WriteString(`<div class="pills">`)
	writePill(&b, "surface", surface, "")
	writePill(&b, "app", html.EscapeString(snap.App), "")
	if snap.Running {
		writePill(&b, "state", "running", "ok")
	} else {
		writePill(&b, "state", "stopped", "warn")
	}
	if len(snap.Legs) > 0 {
		mux := "off"
		if snap.MuxEnabled {
			mux = "on"
		}
		writePill(&b, "mux", mux, "")
		writePill(&b, "legs", fmt.Sprintf("%d", len(snap.Legs)), "")
	}
	b.WriteString(`</div>`)

	if snap.Note != "" {
		fmt.Fprintf(&b, `<p class="note">%s</p>`, html.EscapeString(snap.Note))
	}

	writeMuxSection(&b, snap)
	writeEventsSection(&b, snap)
	writeLogsSection(&b, snap)
	writeControlSeam(&b, snap)
	writeFooter(&b, snap)

	b.WriteString("</body></html>")
	return []byte(b.String())
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
	var maxSent uint64 = 1
	for _, l := range snap.Legs {
		if l.SentBytes > maxSent {
			maxSent = l.SentBytes
		}
	}
	b.WriteString(`<table class="mux"><thead><tr>` +
		`<th>leg</th><th>transport</th><th>peer</th><th>sent</th><th>bandwidth (sent share)</th>` +
		`<th>recv</th><th>rtt</th><th>rtx</th><th>state</th></tr></thead><tbody>`)
	for _, l := range snap.Legs {
		// share is 0..100 (maxSent is the per-leg max, >=1) — kept as uint64 and
		// printed directly so there's no uint64->int narrowing to overflow-check.
		share := l.SentBytes * 100 / maxSent
		state, scls := legState(l)
		fmt.Fprintf(b, `<tr><td>R%d</td><td>%s</td><td class="pk">%s</td><td>%s</td>`,
			l.Index, html.EscapeString(orDash(l.TpType)), html.EscapeString(shortPK(l.RemotePK)), humanBytes(l.SentBytes))
		fmt.Fprintf(b, `<td class="barcell"><span class="bar %s" style="width:%d%%"></span></td>`, scls, share)
		fmt.Fprintf(b, `<td>%s</td><td>%.0f ms</td><td>%d</td><td class="%s">%s</td></tr>`,
			humanBytes(l.RecvBytes), l.LatencyMS, l.Retransmits, scls, state)
	}
	b.WriteString(`</tbody></table>`)
	b.WriteString(`<p class="hint">Bar width is each leg's sent bytes relative to the busiest leg. ` +
		`For a live terminal chart use <code>skywire cli proxy mux plot</code>.</p></section>`)
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

// writeControlSeam renders the (disabled) route-control preview. This is the
// documented extension point: a future write-capable page enables these
// controls and calls a mutating Provider method. It is intentionally inert now
// so the MVP stays read-only.
func writeControlSeam(b *strings.Builder, snap Snapshot) {
	b.WriteString(`<section class="seam"><h2>route control <small>read-only preview</small></h2>`)
	b.WriteString(`<p>Selecting routes and relays from here is planned. Today these are inert; ` +
		`the MVP status page is read-only.</p><div class="controls">`)
	switch snap.Surface {
	case SurfaceSkynet, SurfaceSkysocks:
		b.WriteString(`<button disabled>add leg</button><button disabled>drop leg</button>` +
			`<button disabled>mux mode…</button><button disabled>rebuild route</button>`)
	case SurfaceDmsg:
		b.WriteString(`<button disabled>pick dmsg relay…</button><button disabled>reconnect</button>`)
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

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// shortPK renders a public key as its first+last 4 hex chars, or "direct"/"-"
// for an empty peer.
func shortPK(pk string) string {
	pk = strings.TrimSpace(pk)
	if pk == "" {
		return "-"
	}
	if len(pk) <= 12 {
		return pk
	}
	return pk[:6] + "…" + pk[len(pk)-4:]
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
	`.pills{display:flex;flex-wrap:wrap;gap:.4rem;margin:.9rem 0}` +
	`.pill{background:var(--card);border:1px solid var(--line);border-radius:999px;padding:.15rem .6rem;font-size:12px}` +
	`.pill i{color:var(--muted);font-style:normal;margin-right:.25rem;text-transform:uppercase;font-size:10px;letter-spacing:.4px}` +
	`.pill.ok{border-color:var(--ok);color:var(--ok)}.pill.warn{border-color:var(--warn);color:var(--warn)}` +
	`h2{font-size:.95rem;margin:1.6rem 0 .5rem;color:#e7e9ff;font-weight:600}h2 small{color:var(--muted);font-weight:400;font-size:11px;margin-left:.4rem}` +
	`.note{color:var(--standby)}.empty,.hint{color:var(--muted);font-size:12px}` +
	`table.mux{width:100%;border-collapse:collapse;font-size:12px}` +
	`table.mux th{text-align:left;color:var(--muted);font-weight:500;border-bottom:1px solid var(--line);padding:.3rem .4rem;text-transform:uppercase;font-size:10px;letter-spacing:.3px}` +
	`table.mux td{padding:.3rem .4rem;border-bottom:1px solid rgba(43,49,99,.5);white-space:nowrap}` +
	`td.pk{font-family:ui-monospace,SFMono-Regular,monospace;color:var(--muted)}` +
	`td.barcell{width:36%}.bar{display:block;height:.7rem;border-radius:3px;background:var(--accent);min-width:2px}` +
	`.bar.standby{background:var(--standby)}.bar.warn{background:var(--warn)}` +
	`td.ok{color:var(--ok)}td.warn{color:var(--warn)}td.standby{color:var(--standby)}` +
	`pre.log{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:.7rem;font:11.5px/1.5 ui-monospace,SFMono-Regular,monospace;` +
	`white-space:pre-wrap;word-break:break-word;max-height:26rem;overflow:auto;color:var(--fg)}` +
	`ul.events{margin:.3rem 0;padding-left:1.1rem;font-size:12px}ul.events li{margin:.1rem 0}` +
	`.seam{opacity:.9}.controls{display:flex;flex-wrap:wrap;gap:.4rem;margin-top:.4rem}` +
	`.controls button{font:inherit;font-size:12px;padding:.2rem .6rem;border:1px dashed var(--line);border-radius:6px;background:transparent;color:var(--muted);cursor:not-allowed}` +
	`code{color:var(--accent);font-size:11.5px}` +
	`footer{margin-top:2rem;padding-top:.7rem;border-top:1px solid var(--line);color:var(--muted);font-size:12px}` +
	`footer a{color:var(--accent);text-decoration:none}footer a:hover{text-decoration:underline}` +
	`@media(prefers-color-scheme:light){:root{--bg:#f6f7fb;--fg:#1c1e26;--muted:#4a4f63;--card:#fff;--line:#d3d6e4;--accent:#4149d6;--accent2:#7b3fd0;--ok:#0a7a4c;--warn:#c02a48;--standby:#7a5c00}` +
	`h2,.surface{color:#1c1e26}}`
