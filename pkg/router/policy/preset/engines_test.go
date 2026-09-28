package preset

import "testing"

// TestEnginesIsolateRouteGroups: two route groups of one app sharing a
// transport must not share controller state. Before, one Engine served every
// group, so group B's first tick read group A's byte counter for the shared
// transport as its own baseline.
func TestEnginesIsolateRouteGroups(t *testing.T) {
	var s Engines
	a := s.For(GroupKey("02aa", 3, 49283))
	b := s.For(GroupKey("02aa", 3, 49284))
	if a == b {
		t.Fatal("two route groups got the same Engine")
	}
	if s.For(GroupKey("02aa", 3, 49283)) != a {
		t.Fatal("a route group's Engine changed between ticks")
	}
	shared := []LegInfo{{Index: 0, TransportID: "tp-shared", Alive: true, RecvBytes: 50_000_000, LatencyMs: 100}}
	a.OnTick("adaptive", shared)
	if _, seen := b.adaptPrevRecv["tp-shared"]; seen {
		t.Fatal("group B holds group A's byte counter for the shared transport")
	}
	sharedA, sharedB := s.For(""), s.For("")
	if sharedA != sharedB || sharedA == a {
		t.Fatal("the empty key should be one shared Engine distinct from per-group ones")
	}
	if GroupKey("02aa", 3, 0) != "" {
		t.Fatal("a zero local port must map to the shared key")
	}
}

// TestEnginesDropIdleGroups: a group that stops ticking is forgotten.
func TestEnginesDropIdleGroups(t *testing.T) {
	var s Engines
	s.For("gone")
	for i := 0; i < engineIdleCalls+512; i++ {
		s.For("live")
	}
	if _, ok := s.byGroup["gone"]; ok {
		t.Fatal("an idle route group's Engine was never dropped")
	}
	if _, ok := s.byGroup["live"]; !ok {
		t.Fatal("a ticking route group's Engine was dropped")
	}
}
