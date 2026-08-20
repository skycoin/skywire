package presethook

import (
	"context"
	"reflect"
	"testing"

	"github.com/skycoin/skywire/pkg/router"
)

func TestBeforeDial_RotatingBWShape(t *testing.T) {
	h := New("rotating-bw", nil)
	adj, err := h.BeforeDial(context.Background(), router.DialInfo{AppName: "skysocks-client"})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	want := router.DialAdjustment{
		MuxRoutes:               5,
		MinHops:                 2,
		RotationIntervalSeconds: 90,
		AvoidDirect:             true,
		Distribution:            router.DistributionConfig{Mode: router.DistributionRoundRobin},
	}
	if !reflect.DeepEqual(adj, want) {
		t.Errorf("rotating-bw BeforeDial:\n got %+v\nwant %+v", adj, want)
	}
}

func TestBeforeDial_AppMuxOtherIsNoop(t *testing.T) {
	h := New("app-mux", nil)
	adj, err := h.BeforeDial(context.Background(), router.DialInfo{AppName: "unknown"})
	if err != nil {
		t.Fatalf("BeforeDial: %v", err)
	}
	if !reflect.DeepEqual(adj, router.DialAdjustment{}) {
		t.Errorf("app-mux for a non-target app must be a no-op adjustment; got %+v", adj)
	}
}

// staticProvider supplies fixed geo/kind for named hops.
type staticProvider struct {
	geo  map[string]string
	kind map[string]string
}

func (p staticProvider) Geo(pk string) string {
	if g, ok := p.geo[pk]; ok {
		return g
	}
	return "??"
}
func (p staticProvider) Kind(pk string) string { return p.kind[pk] }

func TestSelectRoute_GeoAvoidPicksCleanRoute(t *testing.T) {
	prov := staticProvider{geo: map[string]string{"a": "US", "b": "DE"}}
	h := New("geo-avoid", prov)
	fwd := []router.CandidateInfo{
		{Hops: []string{"a"}, EstLatencyMs: 10}, // US — blocked
		{Hops: []string{"b"}, EstLatencyMs: 50}, // DE — clean
	}
	sel, err := h.SelectRoute(context.Background(),
		router.DialInfo{AppName: "skysocks-client", CLIOverrides: map[string]string{"avoid_geo": "US"}},
		fwd, nil)
	if err != nil {
		t.Fatalf("SelectRoute: %v", err)
	}
	if sel.Chosen != 1 {
		t.Fatalf("geo-avoid should choose the clean DE candidate (index 1); got %d", sel.Chosen)
	}
}

func TestOnTick_RotatingBWParksFragile(t *testing.T) {
	h := New("rotating-bw", nil)
	legs := []router.LegInfo{
		{Index: 0, TransportID: "t0", Kind: "stcpr", Alive: true},
		{Index: 1, TransportID: "t1", Kind: "webrtc", Alive: true},
	}
	act := h.OnTick(router.DialInfo{AppName: "skysocks-client"}, legs)
	want := router.RotationAction{DemoteToStandby: []int{1}}
	if !reflect.DeepEqual(act, want) {
		t.Errorf("OnTick rotating-bw: got %+v want %+v", act, want)
	}
}
