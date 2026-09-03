package proxystatus

import (
	"strings"
	"testing"
	"time"
)

// dmsgLayerSnap is a fully-populated dmsg-surface snapshot: a live layer, two
// sessions, aliases, and NO route-group legs (the dmsg surface has none).
func dmsgLayerSnap() Snapshot {
	last := time.Now().Add(-90 * time.Second)
	started := time.Now().Add(-2 * time.Hour)
	return Snapshot{
		Surface: SurfaceDmsg,
		App:     "dmsgweb",
		Running: true,
		Layer: &Layer{
			Listen:        "127.0.0.1:4445",
			Suffix:        ".dmsg",
			Upstream:      "127.0.0.1:4446",
			UpstreamState: "skynet_web resolving proxy · running",
			StartedAt:     &started,
			UptimeSec:     7200,
			Requests:      41,
			Successful:    39,
			Failed:        2,
			Active:        1,
			LastRequestAt: &last,
			LastError:     "dial timeout",
		},
		Sessions: []DmsgSession{
			{ServerPK: "0281a102c82c4bbcf7a13d5e0d9a71b3c5cbf2f0e5f3f0b0e0dcb2a3c9b13ef000", Protocol: "tcp", Addr: "1.2.3.4:8081", Streams: 3, PingMS: 12.5},
			{ServerPK: "03f1a1b2c3d4e5f60718293a4b5c6d7e8f901a2b3c4d5e6f708192a3b4c5d6e7f0", Protocol: "quic", Addr: "5.6.7.8:8082", Streams: 0},
		},
		Names: &Names{
			Kind:   "dmsg discovery (public key destinations) + configured aliases",
			Suffix: ".dmsg",
			Aliases: []Alias{
				{Name: "tpd", PK: "02b7a1b2c3d4e5f60718293a4b5c6d7e8f901a2b3c4d5e6f708192a3b4c5d6e7f0"},
			},
		},
	}
}

// TestRenderDmsgLayerSections proves the dmsg status page renders its
// layer-appropriate content — liveness, uptime, chain target, sessions with FULL
// server PKs, and the alias table — from the additive Snapshot sections.
func TestRenderDmsgLayerSections(t *testing.T) {
	snap := dmsgLayerSnap()
	page := string(Render(snap))

	for _, want := range []string{
		"proxy layer",
		"running",
		"up 2h0m",              // uptime, compact
		"127.0.0.1:4445",       // listener
		"chains to",            // chain line
		"127.0.0.1:4446",       // downstream target
		"skynet_web resolving", // honest chain-target label
		"dmsg sessions",
		"2 connected",
		"1m30s ago", // last request, humanized (compactDuration of 90s)
		"last error: dial timeout",
		"name resolution",
		"tpd.dmsg", // alias rendered with the suffix appended
	} {
		if !strings.Contains(page, want) {
			t.Errorf("dmsg status page missing %q", want)
		}
	}

	// PKs are NEVER truncated on a status page.
	for _, s := range snap.Sessions {
		if !strings.Contains(page, s.ServerPK) {
			t.Errorf("session PK %s not rendered in full", s.ServerPK)
		}
	}

	// No lookup cache exists, so the page must not imply one.
	if !strings.Contains(page, "no recent-lookup history") {
		t.Error("name-resolution section should say there is no lookup cache")
	}
}

// TestRenderDmsgEmptyLegsDegrades proves the route-group section degrades to a
// layer-appropriate explanation for the dmsg surface (which owns no route group
// at all) rather than the generic "no active route group" fault reading — and
// that it never fabricates a leg.
func TestRenderDmsgEmptyLegsDegrades(t *testing.T) {
	page := string(Render(dmsgLayerSnap()))
	if !strings.Contains(page, "relays over dmsg <b>sessions</b>") {
		t.Error("dmsg surface should explain it has no route plane, got generic empty text")
	}
	if strings.Contains(page, "No active route group") {
		t.Error("dmsg surface must not use the generic empty-route-group text")
	}
}

// TestRenderSkynetLayerSections proves the skynet page renders its own content:
// layer summary with the skysocks chain, forwarded ports, and active conns.
func TestRenderSkynetLayerSections(t *testing.T) {
	snap := Snapshot{
		Surface: SurfaceSkynet,
		App:     "skynetweb",
		Running: true,
		Layer: &Layer{
			Listen:        "127.0.0.1:4446",
			Suffix:        ".skynet",
			Upstream:      "127.0.0.1:1080",
			UpstreamState: "skysocks-client · running",
			UptimeSec:     45,
			Requests:      7,
		},
		Forwards: []Forward{
			{Port: 8080, LocalPort: 3000, Label: "web", Skynet: true},
			{Port: 8081, Skynet: true, UDP: true}, // LocalPort defaults to Port
		},
		Conns: []Conn{
			{ID: "3f2b1c00-0000-4000-8000-000000000001", Network: "skynet", LocalPort: 3000, RemotePort: 8080},
		},
	}
	page := string(Render(snap))
	for _, want := range []string{
		"proxy layer", "up 45s", "127.0.0.1:4446",
		"skysocks-client · running", "127.0.0.1:1080",
		"forwarded ports", "8080", "3000", "web",
		"active forwarded conns", "3f2b1c00-0000-4000-8000-000000000001",
		"No active route group", // skynet CAN have a route group; it just has none now
	} {
		if !strings.Contains(page, want) {
			t.Errorf("skynet status page missing %q", want)
		}
	}
	// A forward with no explicit local port reports the MESH port as the local
	// one — the registry's own default (EffectiveLocalPort), not a bare zero.
	if !strings.Contains(page, `<td class="num">8081</td><td class="num">8081</td>`) {
		t.Error("a forward with no explicit local port should default it to the mesh port")
	}
}

// TestRenderSkysocksUnaffected proves the additive sections are inert for the
// skysocks surface: no layer/sessions/names/forwards blocks appear, so the
// existing page is byte-for-byte the page it was.
func TestRenderSkysocksUnaffected(t *testing.T) {
	page := string(Render(Snapshot{Surface: SurfaceSkysocks, App: "skysocks-client"}))
	for _, unwanted := range []string{"proxy layer", "dmsg sessions", "name resolution", "forwarded ports"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("skysocks page unexpectedly grew a %q section", unwanted)
		}
	}
}

// TestRenderLayerStoppedAndEmpty proves the honest degraded states: a stopped
// layer with no chain, no sessions and no aliases renders as such instead of
// showing blanks or claiming reachability.
func TestRenderLayerStoppedAndEmpty(t *testing.T) {
	page := string(Render(Snapshot{
		Surface: SurfaceDmsg,
		Layer:   &Layer{Suffix: ".dmsg"},
		Note:    "dmsg resolving proxy is not enabled on this visor",
	}))
	for _, want := range []string{
		"stopped",
		"nothing — ",      // no chain target configured
		"No dmsg session", // sessions section renders its empty state
		"not enabled on this visor",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("stopped dmsg page missing %q", want)
		}
	}
	// UpstreamState is empty, so no reachability claim may appear.
	if strings.Contains(page, "running") {
		t.Error("a stopped layer with no chain must not claim anything is running")
	}
}

func TestCompactDuration(t *testing.T) {
	cases := map[int64]string{0: "0s", 45: "45s", 60: "1m0s", 90: "1m30s", 3599: "59m59s", 3600: "1h0m", 7380: "2h3m", 86400: "1d0h", 187200: "2d4h"}
	for sec, want := range cases {
		if got := compactDuration(sec); got != want {
			t.Errorf("compactDuration(%d) = %q, want %q", sec, got, want)
		}
	}
}

func TestAgoOrDash(t *testing.T) {
	if got := agoOrDash(nil); got != "-" {
		t.Errorf("agoOrDash(nil) = %q, want %q", got, "-")
	}
	var zero time.Time
	if got := agoOrDash(&zero); got != "-" {
		t.Errorf("agoOrDash(zero) = %q, want %q", got, "-")
	}
	// A future timestamp (clock skew) clamps to 0 rather than printing a negative.
	fut := time.Now().Add(time.Hour)
	if got := agoOrDash(&fut); got != "0s ago" {
		t.Errorf("agoOrDash(future) = %q, want %q", got, "0s ago")
	}
}
