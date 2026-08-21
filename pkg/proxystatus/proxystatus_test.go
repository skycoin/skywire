package proxystatus

import (
	"bufio"
	"net/http"
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
			{Index: 0, TpType: "stcpr", RemotePK: "0311223344556677889900aabbccddeeff00112233445566778899aabbccddeeff", SentBytes: 2048, RecvBytes: 1024, LatencyMS: 42, Alive: true},
			{Index: 1, TpType: "sudph", SentBytes: 512, Standby: true, Alive: true},
		},
		Logs:   []string{"line one", "line two"},
		Events: nil,
	}
	page := string(Render(snap))
	for _, want := range []string{
		"<!doctype html>", "proxy status", "skysocks-client", "per-leg mux",
		"stcpr", "recent log", "line two", "route control", "read-only preview",
		"http-equiv=\"refresh\"", "standby", "status.dmsg", "status.skynet",
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
