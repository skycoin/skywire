package proxystatus

import (
	"strings"
	"testing"
)

func TestRenderSkywireConsumers(t *testing.T) {
	self := "02" + strings.Repeat("a", 64)
	exit := "03" + strings.Repeat("b", 64)
	leg := Leg{TransportID: "tp-1", TpType: "stcpr", RemotePK: exit, Alive: true, SentBytes: 2048, RecvBytes: 4096,
		Hops: []Hop{{From: self, To: exit, TpID: "tp-1", TpType: "stcpr"}}, Direct: true}
	snap := Snapshot{Surface: SurfaceSkywire, SelfPK: self, Running: true, Consumers: []Consumer{
		{Name: "skysocks-client", Running: true, Tunnels: []Tunnel{{ExitPK: exit, Legs: []Leg{leg}}}},
		{Name: "skysocks (inbound)", Inbound: true, Tunnels: []Tunnel{{ExitPK: exit, Legs: []Leg{leg}}}},
	}}
	page := string(Render(snap))
	for _, want := range []string{"route status", "route users <small>2</small>", "skysocks-client", "skysocks (inbound)", "accepted", exit} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "No active route group") {
		t.Fatal("the skywire page fell through to the single-surface mux section")
	}
	if n := strings.Count(page, `<pre class="bitree">`); n != 2 {
		t.Fatalf("want one route tree per consumer, got %d", n)
	}
}

func TestMatchSkywire(t *testing.T) {
	if s, ok := Match("status.skywire"); !ok || s != SurfaceSkywire {
		t.Fatalf("status.skywire: got %q %v", s, ok)
	}
}

func TestRenderSkywireDomainRoutes(t *testing.T) {
	snap := Snapshot{Surface: SurfaceSkywire, DomainRoutes: []DomainRoute{
		{Suffix: "example.com", Upstream: "127.0.0.1:1085", Via: "skysocks-client-2 · running"},
		{Suffix: "*", Upstream: "127.0.0.1:1080", Via: "skysocks-client · running"},
	}}
	out := string(Render(snap))
	for _, want := range []string{"domain routes", "example.com", "127.0.0.1:1085", "skysocks-client-2 · running", "everything else"} {
		if !strings.Contains(out, want) {
			t.Fatalf("page lacks %q", want)
		}
	}
	if strings.Contains(string(Render(Snapshot{Surface: SurfaceSkywire})), "domain routes") {
		t.Fatal("domain routes section drawn with no rules")
	}
}
