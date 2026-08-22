package proxystatus

import (
	"bufio"
	"math"
	"net/http"
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
			{Index: 0, TpType: "stcpr", RemotePK: "0311223344556677889900aabbccddeeff00112233445566778899aabbccddeeff", SentBytes: 2048, RecvBytes: 1024, LatencyMS: 42, RouteLatencyMS: 42, Direct: true, Alive: true},
			{Index: 1, TpType: "sudph", SentBytes: 512, RecvBytes: 256, LatencyMS: 30, RouteLatencyMS: 480, Direct: false, Standby: true, Alive: true},
		},
		Logs:   []string{"line one", "line two"},
		Events: nil,
	}
	page := string(Render(snap))
	for _, want := range []string{
		"<!doctype html>", "proxy status", "skysocks-client", "per-leg mux",
		"stcpr", "recent log", "line two", "route control", "read-only preview",
		"http-equiv=\"refresh\"", "standby", "status.dmsg", "status.skynet",
		// route observability: route-latency column, direct/multihop badge, recv bar
		"route rtt", "recv share", "direct", "multihop", "480 ms", "bar recv",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}
	// The current surface should not be linked back to itself in the footer.
	if strings.Contains(page, `href="http://status.skysocks/"`) {
		t.Error("footer should not link the current surface to itself")
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
