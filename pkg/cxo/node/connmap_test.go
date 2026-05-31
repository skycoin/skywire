// Package node — connmap_test.go: contract tests for the sharded
// connection maps. The point of the maps is to remove the single-
// mutex serialization that previously had ~1000 dmsg-accept
// goroutines piled up on Node.mx in production transport-discovery;
// these tests pin the invariants the shard implementation relies
// on so a future "simplification" back to a global mutex can't
// silently regress that.

package node

import (
	"fmt"
	"sync"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
)

// TestAddrConnMap_ReservePending_Idempotent: two callers reserving
// the same addr concurrently both observe a single insert.
// Mirrors onNewConn's contract — one wins isNew, the other gets
// the existing entry back.
func TestAddrConnMap_ReservePending_Idempotent(t *testing.T) {
	m := newAddrConnMap()
	addr := "02b307aee5c8ce1666c63891f8af25ad2f0a47a243914c963942b3ba35b9d095ae:50"
	c1 := &Conn{}
	c2 := &Conn{}

	_, _, isNew1, err := m.reservePending(addr, c1, nil)
	if err != nil {
		t.Fatalf("first reserve errored: %v", err)
	}
	existing, isPending, isNew2, err := m.reservePending(addr, c2, nil)
	if err != nil {
		t.Fatalf("second reserve errored: %v", err)
	}

	if !isNew1 {
		t.Fatalf("first reserve should be new")
	}
	if isNew2 {
		t.Fatalf("second reserve must not be new — c2 shouldn't have replaced c1")
	}
	if !isPending {
		t.Errorf("second reserve should report isPending=true (entry is still pending)")
	}
	if existing != c1 {
		t.Errorf("second reserve returned wrong existing entry: got %p, want %p", existing, c1)
	}
	if got := m.pendingLen(); got != 1 {
		t.Errorf("pendingLen after double reserve: got %d, want 1", got)
	}
}

// TestAddrConnMap_Promote_IdentityCheck: promote with a stale Conn
// pointer must not wipe the slot now holding a live replacement.
// Pre-shard removeConn already had this invariant; the shard
// implementation has to preserve it (see connmap.go promote comment).
func TestAddrConnMap_Promote_IdentityCheck(t *testing.T) {
	m := newAddrConnMap()
	addr := "stale-vs-live-addr"

	stale := &Conn{}
	live := &Conn{}

	// Stale path goes through reserve → promote.
	if _, _, _, err := m.reservePending(addr, stale, nil); err != nil {
		t.Fatalf("reserve stale errored: %v", err)
	}
	m.promote(addr, stale)
	if got := m.activeLen(); got != 1 {
		t.Fatalf("active len after stale promote: got %d, want 1", got)
	}

	// Live replacement: a new Conn takes the same addr slot.
	m.removeActive(addr, stale)
	if _, _, _, err := m.reservePending(addr, live, nil); err != nil {
		t.Fatalf("reserve live errored: %v", err)
	}
	m.promote(addr, live)

	// A late "remove stale" must not wipe the live slot.
	m.removeActive(addr, stale)
	if got := m.activeLen(); got != 1 {
		t.Errorf("activeLen after late stale-remove: got %d, want 1 (live entry stranded)", got)
	}
}

// TestAddrConnMap_CapCheckVeto: capCheck returning non-nil must
// veto the insert AND leave the counters untouched.
func TestAddrConnMap_CapCheckVeto(t *testing.T) {
	m := newAddrConnMap()
	addr := "veto-addr"
	c := &Conn{}

	vetoErr := fmt.Errorf("cap exceeded")
	existing, isPending, isNew, err := m.reservePending(addr, c, func() error {
		return vetoErr
	})

	if err != vetoErr {
		t.Errorf("expected vetoErr, got %v", err)
	}
	if isNew || isPending || existing != nil {
		t.Errorf("vetoed reserve returned non-nil state: existing=%v isPending=%v isNew=%v", existing, isPending, isNew)
	}
	if got := m.pendingLen(); got != 0 {
		t.Errorf("pendingLen after veto: got %d, want 0 (insert leaked past cap check)", got)
	}
}

// TestAddrConnMap_ConcurrentInsertsDifferentShards: the central
// regression target — N goroutines inserting on N different addrs
// should make progress in parallel, not serialize. Functionally:
// every insert must succeed exactly once, regardless of scheduling.
//
// This isn't a benchmark — it's a correctness check that hammers
// the shard mutexes the way the production accept loop does
// (different addr per goroutine = different shards in expectation).
func TestAddrConnMap_ConcurrentInsertsDifferentShards(t *testing.T) {
	m := newAddrConnMap()
	const N = 1000

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		addr := fmt.Sprintf("peer-%04d", i)
		c := &Conn{}
		go func() {
			defer wg.Done()
			_, _, isNew, err := m.reservePending(addr, c, nil)
			if err != nil {
				t.Errorf("reserve for unique addr %q errored: %v", addr, err)
			}
			if !isNew {
				t.Errorf("reserve for unique addr %q reported not new", addr)
			}
		}()
	}
	wg.Wait()

	if got := m.pendingLen(); got != N {
		t.Errorf("pendingLen after %d concurrent unique inserts: got %d, want %d", N, got, N)
	}
}

// TestPkConnMap_RemoveIdentityCheck: same evict-replace race as
// addrConnMap, on the pubkey side. The pre-shard removeConn
// identity-checked Conn pointers against pkToConn — the sharded
// version must do the same.
func TestPkConnMap_RemoveIdentityCheck(t *testing.T) {
	m := newPkConnMap()
	var pk cipher.PubKey
	pk[0] = 0x02
	pk[1] = 0xab
	pk[2] = 0xcd
	pk[3] = 0xef

	stale := &Conn{}
	live := &Conn{}

	m.set(pk, stale)
	m.set(pk, live) // live replaces

	// Late remove of stale must not wipe the live slot.
	m.remove(pk, stale)

	got, ok := m.get(pk)
	if !ok || got != live {
		t.Errorf("late stale-remove wiped live entry: ok=%v got=%p want=%p", ok, got, live)
	}
}

// TestAddrShardIndex_PowerOfTwoMask: relies on connMapShards being
// a power of two so the modulo lowers to a mask. Catches a future
// change to a non-power-of-two count that would silently break
// shardFor — the hash returns 0..uint32 max, the mask returns
// 0..connMapShards-1, the modulo wouldn't.
func TestAddrShardIndex_PowerOfTwoMask(t *testing.T) {
	if connMapShards&(connMapShards-1) != 0 {
		t.Errorf("connMapShards=%d is not a power of two — addrShardIndex's mask wraps incorrectly", connMapShards)
	}
}
