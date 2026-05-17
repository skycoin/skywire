// Package visor pkg/visor/group_stream_send_count_test.go
//
// Pins the visor-wide stream_send_count counter added in #2665. The
// merged code shipped without test coverage; this file backfills
// invariants the rpcgrpc StreamGroupMessages handler relies on:
//
//   - groupStreamSendCount() returns 0 on a fresh visor.
//   - visorPingAdapter.IncGroupStreamSend bumps the visor-side
//     counter on every call (one-bump-per-Send contract).
//   - Counter is race-safe under concurrent bumps (the rpcgrpc
//     handler runs one goroutine per StreamGroupMessages call;
//     both PingServer instances — local CLI + dmsg-RPC — share
//     a single adapter and contend on the same atomic).
//
// Tests use a bare *Visor (no init_group.go wiring). The counter
// lives on Visor itself, not on groupInbox, so it's reachable
// without the full grouping subsystem — which is the point of
// this test file: pin the counter contract in isolation so a
// future refactor that moves the field can't silently lose the
// monotonic-counter property the operator-facing delta calculus
// (DeliverCount − SubDropCount ≈ StreamSendCount) depends on.

package visor

import (
	"sync"
	"testing"
)

func TestVisor_GroupStreamSendCount_DefaultsZero(t *testing.T) {
	// Fresh visor: counter starts at zero. Implied by atomic.Uint64
	// zero-value, but pinned explicitly so a future refactor that
	// moves the field to a wrapper struct or initializes it
	// non-zero is caught by this test.
	v := &Visor{}
	if got := v.groupStreamSendCount(); got != 0 {
		t.Errorf("fresh visor: groupStreamSendCount = %d, want 0", got)
	}
}

func TestVisor_GroupStreamSendCount_IncrementsViaAdapter(t *testing.T) {
	// The rpcgrpc handler calls VisorAPI.IncGroupStreamSend (not the
	// visor's own field) after each successful stream.Send.
	// visorPingAdapter.IncGroupStreamSend is the adapter wired into
	// both PingServer instances. Verify the call lands on the visor
	// counter via the public adapter path — same call site the
	// handler hits in prod.
	v := &Visor{}
	a := &visorPingAdapter{v: v}

	for i := 0; i < 13; i++ {
		a.IncGroupStreamSend()
	}
	if got := v.groupStreamSendCount(); got != 13 {
		t.Errorf("after 13 adapter calls: groupStreamSendCount = %d, want 13", got)
	}
}

func TestVisor_GroupStreamSendCount_IsAtomicUnderConcurrency(t *testing.T) {
	// Two PingServer instances (local CLI + dmsg-RPC) share the same
	// visorPingAdapter, so concurrent StreamGroupMessages handlers
	// across both transports race on the same atomic.Uint64. A
	// non-atomic counter would lose increments under `go test -race`.
	// Simulate the contention with 16 goroutines × 1000 bumps each.
	v := &Visor{}
	a := &visorPingAdapter{v: v}

	const goroutines = 16
	const perG = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				a.IncGroupStreamSend()
			}
		}()
	}
	wg.Wait()

	want := uint64(goroutines * perG)
	if got := v.groupStreamSendCount(); got != want {
		t.Errorf("after %d concurrent bumps: got %d, want %d", want, got, want)
	}
}

func TestVisor_GroupStreamSendCount_AccessorAndFieldAgree(t *testing.T) {
	// The accessor groupStreamSendCount() is currently a thin
	// wrapper around groupStreamSendCounter.Load(). Pin that
	// invariant so a future refactor that introduces a
	// transformation (e.g. clamping, normalization) is caught
	// and reviewed: the operator-facing GroupInfo.StreamSendCount
	// must reflect the raw counter, not a derived value.
	v := &Visor{}
	v.groupStreamSendCounter.Store(42)
	if got := v.groupStreamSendCount(); got != 42 {
		t.Errorf("accessor returned %d, want raw counter value 42", got)
	}
}
