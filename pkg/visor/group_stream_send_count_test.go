// Package visor pkg/visor/group_stream_send_count_test.go
//
// Pins the streamSendCount counter added to groupInbox so the
// rpcgrpc.StreamGroupMessages handler's per-Send bump (via the
// visorPingAdapter.RecordGroupStreamSend bridge) lands on a single
// monotonic atomic.Uint64 reachable from GroupList / GroupGet.
// Closes the layer-9 hop in the per-layer counter ladder:
//
//	peerUpdate → inbox.deliver → sub.ch fan-out → stream.Send → CLI
//
// The full handler is exercised end-to-end by the 3-agent
// reliability tests; this file pins the counter contract in
// isolation so a future refactor of the rpcgrpc handler can't
// silently re-route Sends past the counter.

package visor

import (
	"sync"
	"testing"
)

func TestGroupInbox_StreamSendCount_DefaultsZero(t *testing.T) {
	// Fresh inbox with no Sends recorded → 0. The counter must not
	// depend on the subscriber map or any deliver activity; it's a
	// pure accumulator bumped from the gRPC handler.
	g := newGroupInbox(0)
	if got := g.StreamSendCount(); got != 0 {
		t.Errorf("fresh inbox: StreamSendCount = %d, want 0", got)
	}
}

func TestGroupInbox_StreamSendCount_IncrementsOnRecord(t *testing.T) {
	// Each RecordStreamSend call bumps the counter by exactly one.
	// Pins the contract the rpcgrpc handler relies on: "one Send →
	// one bump" so DeliverCount − SubDropCount ≈ StreamSendCount
	// (modulo active-subscription sentinels) holds for the operator-
	// facing delta calculus.
	g := newGroupInbox(0)
	for i := 0; i < 7; i++ {
		g.RecordStreamSend()
	}
	if got := g.StreamSendCount(); got != 7 {
		t.Errorf("after 7 RecordStreamSend calls: got %d, want 7", got)
	}
}

func TestGroupInbox_StreamSendCount_IsAtomicUnderConcurrency(t *testing.T) {
	// The counter is read from GroupList / GroupGet while
	// concurrent gRPC handlers bump it from N parallel
	// StreamGroupMessages stream.Send loops. Bursty parallel bumps
	// must add up to the expected total — a non-atomic counter
	// would lose increments here under `go test -race`.
	g := newGroupInbox(0)

	const goroutines = 16
	const perG = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				g.RecordStreamSend()
			}
		}()
	}
	wg.Wait()

	want := uint64(goroutines * perG)
	if got := g.StreamSendCount(); got != want {
		t.Errorf("after %d concurrent bumps: got %d, want %d", want, got, want)
	}
}

func TestGroupInbox_StreamSendCount_IndependentOfDeliverAndSubDrop(t *testing.T) {
	// The three counters address different layers; bumping one must
	// never bleed into another. Deliveries don't move
	// StreamSendCount (the bump lives in the rpcgrpc handler, not
	// in deliver), and sub-drop bookkeeping doesn't touch it either.
	g := newGroupInbox(0)
	sub := g.subscribe(1)
	defer g.unsubscribe(sub)

	// Stage 1: only RecordStreamSend.
	g.RecordStreamSend()
	g.RecordStreamSend()
	if got := g.StreamSendCount(); got != 2 {
		t.Fatalf("post-Record: StreamSendCount = %d, want 2", got)
	}
	if got := g.deliverCount.Load(); got != 0 {
		t.Errorf("post-Record: deliverCount = %d, want 0 (Record must not touch deliver)", got)
	}
	if got := g.SubDropCount(); got != 0 {
		t.Errorf("post-Record: SubDropCount = %d, want 0 (Record must not touch sub-drop)", got)
	}
}
