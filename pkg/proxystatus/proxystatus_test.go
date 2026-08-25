package proxystatus

import (
	"bufio"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	cases := []struct {
		host    string
		want    Surface
		matched bool
	}{
		{"status.dmsg", SurfaceDmsg, true},
		{"status.skynet", SurfaceSkynet, true},
		{"status.skysocks", SurfaceSkysocks, true},
		{"STATUS.SkySocks", SurfaceSkysocks, true}, // case-insensitive
		{"status.skysocks:80", SurfaceSkysocks, true},
		{"status.skysocks:8080", SurfaceSkysocks, true},
		{"status", "", false},            // bare label, no surface
		{"x.status.skysocks", "", false}, // must be exactly two labels
		{"status.unknown", "", false},    // unknown surface
		{"pk.skynet", "", false},         // ordinary resolver host
		{"home.dmsg", "", false},         // the other reserved host
	}
	for _, c := range cases {
		got, ok := Match(c.host)
		if ok != c.matched || got != c.want {
			t.Errorf("Match(%q) = (%q,%v), want (%q,%v)", c.host, got, ok, c.want, c.matched)
		}
	}
}

func TestHost(t *testing.T) {
	if h := Host(SurfaceSkysocks); h != "status.skysocks" {
		t.Errorf("Host = %q", h)
	}
}

func TestRenderSections(t *testing.T) {
	snap := Snapshot{
		Surface:    SurfaceSkysocks,
		App:        "skysocks-client",
		Running:    true,
		MuxEnabled: true,
		Legs: []Leg{
			{Index: 0, TpType: "stcpr", RemotePK: "0311223344556677889900aabbccddeeff00112233445566778899aabbccddeeff", SentBytes: 2048, RecvBytes: 1024, LatencyMS: 42, RouteLatencyMS: 42, Direct: true, Alive: true,
				Hops: []Hop{{TpID: "tp0aaaaaaa-0000-0000-0000-000000000000", From: "02aa11223344556677889900aabbccddeeff00112233445566778899aabbccddee", To: "0311223344556677889900aabbccddeeff00112233445566778899aabbccddeeff", TpType: "stcpr", LatencyMS: 42}}},
			{Index: 1, TpType: "sudph", RemotePK: "03bb223344556677889900aabbccddeeff00112233445566778899aabbccddeeff", SentBytes: 512, RecvBytes: 256, LatencyMS: 30, RouteLatencyMS: 480, Direct: false, Standby: true, Alive: true,
				Hops: []Hop{
					{TpID: "tp1bbbbbbb-1111-1111-1111-111111111111", From: "02aa11223344556677889900aabbccddeeff00112233445566778899aabbccddee", To: "03bb223344556677889900aabbccddeeff00112233445566778899aabbccddeeff", TpType: "sudph", LatencyMS: 30},
					{TpID: "tp2ccccccc-2222-2222-2222-222222222222", From: "03bb223344556677889900aabbccddeeff00112233445566778899aabbccddeeff", To: "03cc223344556677889900aabbccddeeff00112233445566778899aabbccddeeff", TpType: "stcpr", LatencyMS: 450},
				}},
		},
		Logs:    []string{"line two", "[2025-06-01T00:00:00.0000Z] INFO [skysocks-client]: app is running app_name=skysocks-client"},
		Events:  []string{"[2025-06-01T00:00:01.0000Z] WARN [router]: leg demoted", "[2025-06-01T00:00:02.0000Z] ERROR [dmsgC]: handshake failed close_err=broken pipe"},
		Streams: []Stream{{ID: 7, Target: "example.com:80", AgeMS: 65000}},
	}
	page := string(Render(snap))
	for _, want := range []string{
		"<!doctype html>", "proxy status", "skysocks-client", "per-leg mux",
		"stcpr", "route, transport", "line two", "route control", "read-only preview",
		// live push (skysocks): the WebSocket script + swap target replace the old
		// meta refresh, which must be gone for this surface. The WS URL is derived
		// from the page origin (never a hardcoded http://), and sendCmd is the
		// browser→server control seam.
		"<main id=\"live\">", "new WebSocket", "/ws",
		`location.origin.replace(/^http/,"ws")`, "sendCmd",
		"standby", "status.dmsg", "status.skynet",
		// route observability: the left per-route summary carries the end-to-end
		// route rtt; "route-rtt" is also named in the tree legend. The multihop
		// standby leg's route rtt (480) renders compact ("480ms").
		"route-rtt", "480ms",
		// ONE shared bilateral route tree (pkg/proxystatus.RouteTree) rendered by
		// pkg/bitree into a monospace <pre>: root = this visor (source PK), each
		// active route a horizontal line crossing a central vertical spine — the
		// left summary block LEFT of the spine, the hop chain RIGHT of it, and
		// aligned trailing hop columns. The exact model+shape `proxy tree` prints.
		// The source PK is anchored at the spine column with a "│" descender into
		// it; routes join the spine via ┼/┴ junctions; a multihop route's extra
		// hops hang off the RIGHT with └── connectors.
		`class="tree"`, `class="bitree"`, "──┼──", "──┴──", "└──", "│",
		// Tree legend, now BELOW the tree, with the state WORDS themselves colored
		// (source accent / active green / standby amber) — no swatch dot. Dead legs
		// are pruned so there is no dead entry.
		`class="tlegend"`, `class="lgnd src"`,
		`class="lgnd ok"`, `class="lgnd standby"`,
		"this visor",
		// Label-header row (TreeHeader) rendered inside the tree <pre> as a template
		// that lines up with the columns beneath: labels in place of the PKs/values.
		`class="thead"`, "peer-pk", "tp-id", "tp-rtt", "R[n]",
		// Header up/down throughput meters (moved to the top, between the brand and
		// the surface name), driven by the inline script from the hidden cumulative
		// byte counters.
		`class="rates"`, `class="rmeter up"`, `class="rmeter down"`,
		// FULL (never truncated) PKs: the source (root) and the multihop exit.
		"03cc223344556677889900aabbccddeeff00112233445566778899aabbccddeeff",
		"02aa11223344556677889900aabbccddeeff00112233445566778899aabbccddee",
		// per-hop transport columns: bracketed type + full tpid (click-to-copy) + rtt.
		"[stcpr]", "[sudph]", "450ms", "30ms",
		`class="ftid copy"`, "tp2ccccccc-2222-2222-2222-222222222222",
		// left per-route summary tinted by STATE (ok=active green / standby=amber),
		// with a color-blind-safe state dot (● active / ○ standby) — no state word.
		`class="lsum ok"`, `class="lsum standby"`,
		`class="tstate ok"`, `class="tstate standby"`, "●", "○", "R[0]", "R[1]",
		// UX pass: selection-guard + identical-fragment skip in the live script,
		// click-to-copy affordance (execCommand fallback for the HTTP context),
		// the WebSocket live indicator, route-group summary, and staged control tags.
		"getSelection", "selectionchange", "execCommand",
		`class="fpk copy"`, "data-copy=", "click to copy",
		`id="wsstat"`, "reconnecting", "rsync(this)", `class="soon"`,
		// Hidden cumulative byte counters live inside the live region so each push
		// refreshes them; the visible meters are in the static header.
		`data-bytes="up" data-val=`, "hidden",
		// Scroll preservation across the live-swap: apply() captures the window offset
		// (and the log <pre>'s inner offset) before el.innerHTML=h and restores it, so
		// scrolling the page horizontally doesn't snap back on the next ~1s push.
		"window.scrollX", "window.scrollY", "window.scrollTo(", "pre.log",
		// The log pane is a resizable black terminal: black background, user-draggable
		// height (resize:vertical), its own scroll.
		"background:#000", "resize:vertical",
		// The route tree scrolls horizontally in its OWN overflow container (not the
		// page body) when a long PK runs wide.
		"overflow-x:auto",
		// Embedded mononoki monospace font so the tree's box-drawing glyphs align:
		// an @font-face woff2 data-URI in the shell + the family on the tree/PK cells.
		"@font-face", "'Mononoki'", "data:font/woff2;base64,",
		// Tree grid CSS so the box-drawing glyphs actually connect: a tight
		// line-height:1 (vertical │ touch between rows), letter-spacing:0 (── run
		// continuous), and a real box-drawing monospace fallback after Mononoki.
		"line-height:1;letter-spacing:0", "'DejaVu Sans Mono'",
		// The open-streams <details> keeps its open/closed state across the ~1s
		// WebSocket innerHTML swap (captured before, restored after — like the scroll
		// guard) so it doesn't snap shut while the operator reads it.
		"details.streams", "ds.open=dopen",
		// item 3: ONE combined route+transport+log stream, colored PER TOKEN to match
		// `proxy start --verbose` (pkg/logging printColored): the [timestamp] grey
		// (ll-ts), the LEVEL word its level color (INFO green / WARN amber / ERROR red),
		// the "[prefix]:" cyan (ll-prefix), the message default, and each field KEY
		// tinted to the level color (value default). ERROR uses a real red (--err), not
		// the pink --warn. The event + log lines merge in timestamp order.
		"route, transport &amp; log", `class="ll-info"`, `class="ll-warn"`, `class="ll-error"`,
		`class="ll-ts"`, `class="ll-prefix"`, "[router]:", "[dmsgC]:",
		// field key tinted to the level color, its value left default foreground.
		`<span class="ll-info">app_name</span>=skysocks-client`,
		"app is running", "leg demoted",
		// item 5: live up/down rate meters differenced from the cumulative byte totals
		// by the inline script over the WebSocket pushes.
		`data-bytes="up"`, `data-bytes="down"`, `data-rate="up"`, `data-rate="down"`, `class="rate"`, "fmtRate",
		// item 4: the "N open stream(s)" count expands into per-stream rows (id /
		// target / age) in an expandable section.
		`class="streams"`, "open streams", "example.com:80",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	// PKs must never be truncated: no "<hex>…<hex>" shortPK pattern on the page.
	// (Bare "…" is fine in UI affordances like the disabled "mux mode…" button.)
	if regexp.MustCompile(`[0-9a-f]…[0-9a-f]`).MatchString(page) {
		t.Error("status page must not truncate public keys (found hex…hex)")
	}
	// The current surface should not be linked back to itself in the footer.
	if strings.Contains(page, `href="http://status.skysocks/"`) {
		t.Error("footer should not link the current surface to itself")
	}
	// The skysocks surface is kept live by the WebSocket, so the old meta refresh
	// must be gone (it would fight the in-place swap with a full-page reload).
	if strings.Contains(page, `http-equiv="refresh"`) {
		t.Error("skysocks status page must not use meta refresh (WebSocket live-push replaces it)")
	}
	// The per-node word pills and per-leg state/hop-count word tags are gone: the
	// root "this visor" and leaf "exit" pills are replaced by the source/dest PK
	// color accents; the "active"/"standby" state word and the "N hops" count by the
	// state color + tree depth. Guard that their markup is absent (the words survive
	// only in the header legend + hint prose, matched above).
	for _, gone := range []string{
		`class="tlabel src">this visor`, `class="tlabel dst">exit`,
		`class="rtag`, `class="badge`,
	} {
		if strings.Contains(page, gone) {
			t.Errorf("status page must not render the removed %q markup", gone)
		}
	}
	// The layout pass removed the status pill row, the leg-census summary line, and
	// the per-stream metering disclaimer sentence; guard their markup/text is gone.
	for _, gone := range []string{
		`class="pills"`, `class="pill"`, `class="rgsummary"`,
		"not metered at the tunnel",
	} {
		if strings.Contains(page, gone) {
			t.Errorf("status page must no longer contain %q", gone)
		}
	}
}

// TestRenderFragmentIsLiveRegionOnly verifies RenderFragment emits the live
// content (pills/mux/log) WITHOUT the page shell, so it can be pushed as a
// WebSocket TEXT frame and swapped into <main id="live">. It must be identical to
// the shell's live region — same source of truth.
func TestRenderFragmentIsLiveRegionOnly(t *testing.T) {
	snap := Snapshot{
		Surface: SurfaceSkysocks, App: "skysocks-client", Running: true,
		Logs: []string{"frag line"},
	}
	frag := string(RenderFragment(snap))
	for _, want := range []string{"per-leg mux", "route, transport", "frag line"} {
		if !strings.Contains(frag, want) {
			t.Errorf("fragment missing %q", want)
		}
	}
	// The ~38 KB embedded font must ride in the page shell only, never in the live
	// fragment the WebSocket restreams every ~1s.
	for _, unwanted := range []string{"<!doctype", "<html", "<head", "<style", "<main", "new WebSocket", "route control", "<footer", "@font-face", "data:font/woff2"} {
		if strings.Contains(frag, unwanted) {
			t.Errorf("fragment must not contain shell/static markup %q", unwanted)
		}
	}
	// The fragment must be exactly the inner HTML of the shell's <main id="live">.
	page := string(Render(snap))
	const open, closeTag = `<main id="live">`, `</main>`
	i := strings.Index(page, open)
	j := strings.Index(page, closeTag)
	if i < 0 || j < 0 || j < i {
		t.Fatal("shell is missing the <main id=\"live\"> live region")
	}
	if inner := page[i+len(open) : j]; inner != frag {
		t.Errorf("fragment differs from shell live region:\n frag=%q\ninner=%q", frag, inner)
	}
}

// TestRenderDmsgKeepsMetaRefresh guards that non-skysocks surfaces (no WebSocket
// endpoint) keep the meta-refresh fallback and do NOT get the skysocks WebSocket
// script.
func TestRenderDmsgKeepsMetaRefresh(t *testing.T) {
	page := string(Render(Snapshot{Surface: SurfaceDmsg, App: "dmsgweb"}))
	if !strings.Contains(page, `http-equiv="refresh"`) {
		t.Error("dmsg status page should keep the meta-refresh fallback")
	}
	if strings.Contains(page, "new WebSocket") {
		t.Error("dmsg status page must not emit the skysocks WebSocket script")
	}
}

func TestRenderEscapes(t *testing.T) {
	snap := Snapshot{Surface: SurfaceDmsg, App: "dmsgweb", Logs: []string{"<script>evil()</script>"}}
	page := string(Render(snap))
	if strings.Contains(page, "<script>evil()") {
		t.Error("log line was not HTML-escaped")
	}
}

func TestRenderEmptyMux(t *testing.T) {
	snap := Snapshot{Surface: SurfaceDmsg, App: "dmsgweb"}
	page := string(Render(snap))
	if !strings.Contains(page, "No active route group") {
		t.Error("empty-mux note missing")
	}
}

func TestServeConn(t *testing.T) {
	c := ServeConn([]byte("<html>ok</html>"))
	if _, err := c.Write([]byte("GET / HTTP/1.1\r\nHost: status.skysocks\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
}

// --- WCAG contrast (the dark + light legibility fix) ---------------------------

func hexLum(h string) float64 {
	h = strings.TrimPrefix(h, "#")
	c := func(i int) float64 {
		v, err := strconv.ParseInt(h[i:i+2], 16, 0)
		if err != nil {
			return 0
		}
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	r, g, b := c(0), c(2), c(4)
	return 0.2126*r + 0.7152*g + 0.0722*b
}

func contrast(fg, bg string) float64 {
	l1, l2 := hexLum(fg)+0.05, hexLum(bg)+0.05
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return l1 / l2
}

// TestContrastAA locks the legibility fix: every text token the status page uses
// must clear WCAG AA body-text contrast (>=4.5:1) against the surface it sits on,
// in BOTH the dark default and the light prefers-color-scheme block.
func TestContrastAA(t *testing.T) {
	const (
		darkBG, darkCard, darkMuted, darkFG = "#0b0d17", "#131629", "#a2a8cc", "#c7cbe6"
		lightBG, lightMuted, lightFG        = "#f6f7fb", "#4a4f63", "#1c1e26"
		lightOK, lightWarn, lightStandby    = "#0a7a4c", "#c02a48", "#7a5c00"
	)
	pairs := []struct {
		name   string
		fg, bg string
	}{
		{"dark muted on bg", darkMuted, darkBG},
		{"dark muted on card", darkMuted, darkCard},
		{"dark fg on bg", darkFG, darkBG},
		{"light muted on bg", lightMuted, lightBG},
		{"light fg on bg", lightFG, lightBG},
		{"light ok on bg", lightOK, lightBG},
		{"light warn on bg", lightWarn, lightBG},
		{"light standby on bg", lightStandby, lightBG},
	}
	for _, p := range pairs {
		if r := contrast(p.fg, p.bg); r < 4.5 {
			t.Errorf("%s: contrast %.2f:1 < 4.5:1", p.name, r)
		}
	}
	// The token values asserted above must actually be the ones the stylesheet
	// ships, or the test guards nothing.
	for _, want := range []string{darkMuted, lightMuted, lightOK, lightWarn, lightStandby} {
		if !strings.Contains(css, want) {
			t.Errorf("css missing asserted token %s", want)
		}
	}
	// The old low-contrast greys must be gone.
	for _, gone := range []string{"#7a80a8", "--muted:#666"} {
		if strings.Contains(css, gone) {
			t.Errorf("css still contains low-contrast token %q", gone)
		}
	}
}
