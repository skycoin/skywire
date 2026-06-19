package dmsg

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func pk(b byte) cipher.PubKey {
	var p cipher.PubKey
	p[0] = 2
	p[1] = b
	return p
}

func TestPortHitTracker(t *testing.T) {
	tr := newPortHitTracker(3)

	// Repeat hits from the same (pk, port) coalesce + bump the count.
	tr.record(pk(1), 80)
	tr.record(pk(1), 80)
	tr.record(pk(1), 80)
	// Different port from the same pk is a distinct entry.
	tr.record(pk(1), 46)
	// Different pk, same port.
	tr.record(pk(2), 80)

	snap := tr.snapshot()
	if len(snap) != 3 {
		t.Fatalf("want 3 distinct entries, got %d", len(snap))
	}
	// Most-recent-first: pk(2):80 was recorded last.
	if snap[0].SrcPK != pk(2) || snap[0].DstPort != 80 {
		t.Errorf("snapshot[0] should be the most recent hit (pk2:80), got %s:%d", snap[0].SrcPK, snap[0].DstPort)
	}
	// The coalesced entry has count 3.
	var got uint64
	for _, h := range snap {
		if h.SrcPK == pk(1) && h.DstPort == 80 {
			got = h.Count
		}
	}
	if got != 3 {
		t.Errorf("pk1:80 count = %d, want 3", got)
	}

	// Exceeding cap evicts the least-recently-seen entry (pk1:80 — its
	// last hit predates the 46 and pk2:80 records).
	tr.record(pk(3), 22)
	snap = tr.snapshot()
	if len(snap) != 3 {
		t.Fatalf("after overflow want 3 (cap), got %d", len(snap))
	}
	for _, h := range snap {
		if h.SrcPK == pk(1) && h.DstPort == 80 {
			t.Errorf("pk1:80 (oldest) should have been evicted")
		}
	}

	// Nil tracker is safe.
	var nilTr *portHitTracker
	nilTr.record(pk(9), 1)
	if nilTr.snapshot() != nil {
		t.Errorf("nil tracker snapshot should be nil")
	}
}
