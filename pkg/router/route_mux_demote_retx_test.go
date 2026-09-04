//go:build !tinygo || (js && wasm)

package router

import (
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// capturingTransport is a workingTransport that records every packet written to
// it, so a test can assert WHICH leg a retransmit went out on and for which
// sequences. Writes still succeed immediately (no real peer).
type capturingTransport struct {
	*workingTransport
	mu      sync.Mutex
	written []routing.Packet
}

func newCapturingTransport() *capturingTransport {
	return &capturingTransport{workingTransport: newWorkingTransport()}
}

func (t *capturingTransport) Write(b []byte) (int, error) {
	n, err := t.workingTransport.Write(b)
	if err == nil {
		cp := make([]byte, len(b))
		copy(cp, b)
		t.mu.Lock()
		t.written = append(t.written, routing.Packet(cp))
		t.mu.Unlock()
	}
	return n, err
}

// dataSeqs returns the sequence numbers of the sequenced DataPackets captured on
// this transport, in write order.
func (t *capturingTransport) dataSeqs() []uint32 {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []uint32
	for _, p := range t.written {
		if p.Type() == routing.DataPacket {
			out = append(out, p.SequenceNumber())
		}
	}
	return out
}

// createCapturingMuxRouteGroup mirrors createMuxRouteGroup but enables SACK/retx
// (so wrapPayload populates the retx buffer) and uses capturing transports so
// the test can observe retransmitted packets per leg.
func createCapturingMuxRouteGroup(t *testing.T) (*RouteGroup, []*capturingTransport) {
	t.Helper()
	const nTransports = 2

	l := logging.NewMasterLogger()
	rt := routing.NewTable(l.PackageLogger("rgt-demote"))

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	desc := routing.NewRouteDescriptor(pk1, pk2, 1, 2)

	rg := NewRouteGroup(DefaultRouteGroupConfig(), rt, desc, l)
	rg.mux = newRouteMux(l.PackageLogger("mux-demote"), true) // SACK/retx ON

	mts := make([]*transport.ManagedTransport, 0, nTransports)
	conns := make([]*capturingTransport, 0, nTransports)
	fwds := make([]routing.Rule, 0, nTransports)
	rvss := make([]routing.Rule, 0, nTransports)

	for i := 0; i < nTransports; i++ {
		tpID := uuid.New()
		fwd := routing.ForwardRule(DefaultRouteKeepAlive, routing.RouteID(i+1), routing.RouteID(i+100), tpID, pk2, pk1, 1, 2) //nolint:gosec
		rvs := routing.ConsumeRule(DefaultRouteKeepAlive, routing.RouteID(i+100), pk1, pk2, 2, 1)                             //nolint:gosec
		rt.SaveRule(fwd)                                                                                                      //nolint:errcheck,gosec
		rt.SaveRule(rvs)                                                                                                      //nolint:errcheck,gosec

		conn := newCapturingTransport()
		mt := transport.NewManagedTransportForTest(conn)
		mt.Entry = transport.Entry{ID: tpID, Type: "test"}

		mts = append(mts, mt)
		conns = append(conns, conn)
		fwds = append(fwds, fwd)
		rvss = append(rvss, rvs)
	}

	rg.mu.Lock()
	rg.tps = mts
	rg.fwd = fwds
	rg.rvs = rvss
	rg.mux.rebuildWeights(mts)
	rg.mux.growLegs(len(mts))
	for i := range mts {
		rg.mux.markLegReady(i)
	}
	rg.mu.Unlock()

	return rg, conns
}

// demoteHook is a one-shot RotationHook that demotes a fixed set of leg indices
// to warm standby on its next tick.
type demoteHook struct{ demote []int }

func (h demoteHook) OnTick(_ DialInfo, _ []LegInfo) RotationAction {
	return RotationAction{DemoteToStandby: h.demote}
}

// TestMuxDemoteFlushesInFlightSeqs is the wire-correctness proof for the Gate-5
// no-dip hot-swap. A leg carrying unACKed, in-flight sequences is demoted to
// warm standby. Those sequences must not be stranded: the demote path must
// proactively re-send the demoted leg's outstanding sequences onto an ACTIVE,
// non-standby leg immediately (a self-issued SACK recovery), so the peer's
// no-skip reorder gap fills without waiting for a SACK round-trip that can lose
// the race with the bounded retx buffer's aging (#86 / the live "17MB -> 0 at
// first demote" stall).
//
// The flush is NARROWED to the demoted leg's sequences (plus unknown-leg
// entries): sequences riding still-active legs heal through the normal SACK
// path, and re-sending them too duplicated the entire in-flight window on
// every demote (measured live: 33.8MB of duplicates against 14.7MB of payload
// on one leg of a 20MB transfer). The activeSeqs sent on leg 0 below prove the
// narrowing: they are held, but must NOT be flushed when leg 1 demotes.
func TestMuxDemoteFlushesInFlightSeqs(t *testing.T) {
	rg, conns := createCapturingMuxRouteGroup(t)

	// Tags are TRANSPORT UUIDs (stable across slice compaction), never leg
	// indices — an index tag went stale the moment pruneDeadTransports
	// compacted tps[] and the flush then missed genuinely stranded sequences.
	leg0ID := rg.tps[0].Entry.ID
	leg1ID := rg.tps[1].Entry.ID

	// Sequences in flight ON LEG 1's transport (the leg about to be demoted) —
	// these are stranded by the demote and must be flushed.
	const nPkts = 32
	want := make([]uint32, 0, nPkts)
	for i := 0; i < nPkts; i++ {
		_, seq, err := rg.mux.wrapPayload(routing.RouteID(2), []byte{byte(i), 0xAA, 0xBB}, leg1ID)
		if err != nil {
			t.Fatalf("wrapPayload: %v", err)
		}
		want = append(want, seq)
	}
	// Sequences in flight on LEG 0's transport (stays active) — NOT stranded;
	// the narrowed flush must leave them to the normal SACK path.
	const nActive = 8
	activeSeqs := make(map[uint32]bool, nActive)
	for i := 0; i < nActive; i++ {
		_, seq, err := rg.mux.wrapPayload(routing.RouteID(2), []byte{0xCC, byte(i)}, leg0ID)
		if err != nil {
			t.Fatalf("wrapPayload(leg0): %v", err)
		}
		activeSeqs[seq] = true
	}
	if got := rg.mux.retxBuf.Len(); got != nPkts+nActive {
		t.Fatalf("retxBuf holds %d unACKed seqs, want %d", got, nPkts+nActive)
	}

	// Demote leg 1 to warm standby via the real rotation apply path.
	rg.rotationHook = demoteHook{demote: []int{1}}
	rg.rotationServiceFn(0)

	if !rg.mux.isLegStandby(1) {
		t.Fatal("leg 1 should be on standby after demote")
	}

	// The demoted leg (1) must carry NONE of the flush — it is parked.
	if got := conns[1].dataSeqs(); len(got) != 0 {
		t.Errorf("demoted leg 1 carried %d resent DataPackets, want 0 (%v)", len(got), got)
	}

	// The active leg (0) must have received a resend of every DEMOTED-leg
	// sequence, ascending (lowest gap first) — and none of the leg-0 sequences
	// (they are not stranded; re-sending them is the measured duplicate storm).
	got := conns[0].dataSeqs()
	if len(got) != len(want) {
		t.Fatalf("active leg 0 resent %d seqs, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resend[%d] = seq %d, want %d (demoted-leg window: got %v want %v)",
				i, got[i], want[i], got, want)
		}
		if activeSeqs[got[i]] {
			t.Fatalf("resend included seq %d which rides the ACTIVE leg — flush not narrowed", got[i])
		}
	}
}

// TestMuxDemoteFlushNoActiveLegIsSafe: demoting when there is no other active
// leg to resend on must be a safe no-op, not a panic or a wedge. (In practice
// leg 0 is never standby, so this is a defensive guard.)
func TestMuxDemoteFlushNoActiveLegIsSafe(t *testing.T) {
	rg, conns := createCapturingMuxRouteGroup(t)

	for i := 0; i < 8; i++ {
		if _, _, err := rg.mux.wrapPayload(routing.RouteID(2), []byte{byte(i)}, uuid.Nil); err != nil {
			t.Fatalf("wrapPayload: %v", err)
		}
	}

	// Force leg 0 not-ready so no active send leg remains once leg 1 is demoted.
	rg.mux.legMu.Lock()
	rg.mux.ready[0] = false
	rg.mux.legMu.Unlock()

	rg.rotationHook = demoteHook{demote: []int{1}}
	rg.rotationServiceFn(0) // must not panic

	if n := len(conns[0].dataSeqs()) + len(conns[1].dataSeqs()); n != 0 {
		t.Errorf("no-active-leg demote resent %d packets, want 0 (safe no-op)", n)
	}
}
