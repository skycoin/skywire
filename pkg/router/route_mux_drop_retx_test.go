package router

import (
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/routing"
)

type dropHook struct{ drop []int }

func (h dropHook) OnTick(_ DialInfo, _ []LegInfo) RotationAction {
	return RotationAction{DropLegs: h.drop}
}

// TestMuxDropFlushesInFlightSeqs: a policy drop of an ACTIVE leg strands its
// in-flight, unACKed sequences exactly as a demote does, and the receiver's
// no-skip reorder holds that gap open. If the sequences age out of the bounded
// retx buffer before a SACK round-trip asks for them the gap can never be
// filled and the stream wedges. So the drop must resend them onto a surviving
// leg at once — and only them; sequences riding the surviving leg heal
// through the normal SACK path.
func TestMuxDropFlushesInFlightSeqs(t *testing.T) {
	rg, conns := createCapturingMuxRouteGroup(t)
	leg0ID := rg.tps[0].Entry.ID
	leg1ID := rg.tps[1].Entry.ID

	const nPkts = 16
	want := make([]uint32, 0, nPkts)
	for i := 0; i < nPkts; i++ {
		_, seq, err := rg.mux.wrapPayload(routing.RouteID(2), []byte{byte(i), 0xAA}, leg1ID)
		if err != nil {
			t.Fatalf("wrapPayload: %v", err)
		}
		want = append(want, seq)
	}
	survivor := make(map[uint32]bool)
	for i := 0; i < 4; i++ {
		_, seq, err := rg.mux.wrapPayload(routing.RouteID(2), []byte{0xCC, byte(i)}, leg0ID)
		if err != nil {
			t.Fatalf("wrapPayload(leg0): %v", err)
		}
		survivor[seq] = true
	}

	rg.rotationHook = dropHook{drop: []int{1}}
	rg.rotationServiceFn(0)

	rg.mu.Lock()
	legs := len(rg.tps)
	rg.mu.Unlock()
	if legs != 1 {
		t.Fatalf("route group has %d legs after the drop, want 1", legs)
	}
	got := conns[0].dataSeqs()
	if len(got) != len(want) {
		t.Fatalf("surviving leg resent %d seqs, want the dropped leg's %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resend[%d] = seq %d, want %d", i, got[i], want[i])
		}
		if survivor[got[i]] {
			t.Fatalf("resend included seq %d which rides the surviving leg", got[i])
		}
	}
}

// TestMuxPruneFlushesInFlightSeqs: a leg pruned because its transport closed
// (a relay going away) or stopped echoing strands its in-flight sequences the
// same way a policy drop does. They must be resent on a surviving leg at the
// prune, not left to age out of the retx buffer before a SACK asks for them.
func TestMuxPruneFlushesInFlightSeqs(t *testing.T) {
	rg, conns := createCapturingMuxRouteGroup(t)
	leg1ID := rg.tps[1].Entry.ID

	const nPkts = 12
	want := make([]uint32, 0, nPkts)
	for i := 0; i < nPkts; i++ {
		_, seq, err := rg.mux.wrapPayload(routing.RouteID(2), []byte{byte(i), 0xDD}, leg1ID)
		if err != nil {
			t.Fatalf("wrapPayload: %v", err)
		}
		want = append(want, seq)
	}

	if n := rg.pruneDeadLegs([]uuid.UUID{leg1ID}, "test: transport closed under the leg", "transport-closed"); n != 1 {
		t.Fatalf("pruned %d legs, want 1", n)
	}
	got := conns[0].dataSeqs()
	if len(got) != len(want) {
		t.Fatalf("surviving leg resent %d seqs, want the pruned leg's %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resend[%d] = seq %d, want %d", i, got[i], want[i])
		}
	}
}
