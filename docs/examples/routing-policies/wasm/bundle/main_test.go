package main

import "testing"

// These tests prove each conditional preset HONORS ITS CONSTRAINT — a
// deterministic property of the decide output over synthetic candidates, so it
// needs no live mesh. Run natively: `go test .` in this dir (its own module).

func ctxWith(overrides map[string]string) routingContextWire {
	return routingContextWire{App: "skysocks-client", CLIOverrides: overrides}
}

func TestGeoAvoid_NeverTransitsBlockedCountry(t *testing.T) {
	cands := []candidateWire{
		{Hops: []string{"a"}, HopsGeo: []string{"US"}, EstLatencyMs: 10},            // blocked, fastest
		{Hops: []string{"b"}, HopsGeo: []string{"DE"}, EstLatencyMs: 50},            // clean
		{Hops: []string{"c", "d"}, HopsGeo: []string{"FR", "US"}, EstLatencyMs: 20}, // blocked via 2nd hop
	}
	spec := decideGeoAvoid(ctxWith(map[string]string{"avoid_geo": "US"}), cands)
	if spec.Chosen == nil {
		t.Fatal("expected a chosen candidate, got none")
	}
	for _, g := range spec.Chosen.HopsGeo {
		if g == "US" {
			t.Fatalf("geo-avoid chose a route transiting a blocked country: %v", spec.Chosen.HopsGeo)
		}
	}
	if spec.Chosen.HopsGeo[0] != "DE" {
		t.Fatalf("expected the clean DE route, got %v", spec.Chosen.HopsGeo)
	}
}

func TestGeoAvoid_NoCleanRoute_DefersRatherThanViolates(t *testing.T) {
	cands := []candidateWire{{Hops: []string{"a"}, HopsGeo: []string{"US"}, EstLatencyMs: 10}}
	spec := decideGeoAvoid(ctxWith(map[string]string{"avoid_geo": "US"}), cands)
	if spec.Chosen != nil {
		t.Fatalf("with no clean candidate, geo-avoid must defer (nil Chosen), got %v", spec.Chosen.HopsGeo)
	}
}

func TestGeoAvoid_NoOverride_IsNoOp(t *testing.T) {
	cands := []candidateWire{{Hops: []string{"a"}, HopsGeo: []string{"US"}}}
	if spec := decideGeoAvoid(ctxWith(nil), cands); spec.Chosen != nil {
		t.Fatal("with no avoid_geo configured, geo-avoid must not constrain")
	}
}

func TestTransportDiverse_MaximizesDistinctKinds(t *testing.T) {
	cands := []candidateWire{
		{Hops: []string{"a", "b"}, TransportKinds: []string{"stcpr", "stcpr"}, EstLatencyMs: 10}, // 1 distinct
		{Hops: []string{"c", "d"}, TransportKinds: []string{"stcpr", "sudph"}, EstLatencyMs: 30}, // 2 distinct
		{Hops: []string{"e", "f"}, TransportKinds: []string{"sudph", "sudph"}, EstLatencyMs: 5},  // 1 distinct
	}
	spec := decideTransportDiverse(ctxWith(nil), cands)
	if spec.Chosen == nil {
		t.Fatal("expected a chosen candidate")
	}
	if distinctCount(spec.Chosen.TransportKinds) != 2 {
		t.Fatalf("transport-diverse must pick the most-diverse route; got kinds %v", spec.Chosen.TransportKinds)
	}
}

func TestTrustTiered_PrefersFullyTrusted(t *testing.T) {
	cands := []candidateWire{
		{Hops: []string{"trustA", "evil"}, EstLatencyMs: 5},    // partial
		{Hops: []string{"trustA", "trustB"}, EstLatencyMs: 40}, // fully trusted (slower)
	}
	spec := decideTrustTiered(ctxWith(map[string]string{"trusted_pks": "trustA,trustB"}), cands)
	if spec.Chosen == nil {
		t.Fatal("expected a chosen candidate")
	}
	if len(spec.Chosen.Hops) != 2 || spec.Chosen.Hops[1] != "trustB" {
		t.Fatalf("trust-tiered must prefer the fully-trusted route even if slower; got %v", spec.Chosen.Hops)
	}
}

func TestTrustTiered_FallsBackToMostTrusted(t *testing.T) {
	cands := []candidateWire{
		{Hops: []string{"evil", "evil2"}, EstLatencyMs: 5},  // 0 trusted
		{Hops: []string{"trustA", "evil"}, EstLatencyMs: 9}, // 1 trusted (most)
	}
	spec := decideTrustTiered(ctxWith(map[string]string{"trusted_pks": "trustA,trustB"}), cands)
	if spec.Chosen == nil || spec.Chosen.Hops[0] != "trustA" {
		t.Fatalf("trust-tiered must fall back to the most-trusted route; got %v", spec.Chosen)
	}
}

func TestTimeOfDay_SwitchesShapeByHour(t *testing.T) {
	const h = int64(3600) * 1_000_000_000 // one hour in unix-nanos
	// business hours (default 9-17): lean single route.
	biz := decideTimeOfDay(routingContextWire{NowUnixNano: 11 * h})
	if biz.Mux != 1 {
		t.Fatalf("business-hours shape must be lean (mux 1); got mux=%d", biz.Mux)
	}
	// off-hours: wide privacy mux with rotation.
	off := decideTimeOfDay(routingContextWire{NowUnixNano: 3 * h})
	if off.Mux != 4 || off.RotationIntervalSeconds == 0 || off.Distribution != "round-robin" {
		t.Fatalf("off-hours shape must be a wide rotating privacy mux; got %+v", off)
	}
	// configurable window: 22-6 (wraps midnight) — hour 23 is inside.
	night := decideTimeOfDay(routingContextWire{NowUnixNano: 23 * h, CLIOverrides: map[string]string{"business_hours": "22-6"}})
	if night.Mux != 1 {
		t.Fatalf("hour 23 within a 22-6 window must be lean; got mux=%d", night.Mux)
	}
}
