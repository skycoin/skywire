// Package cxoutils — pkg/cxo/cxoutils/cxoutils_test.go:
// regression coverage for the partial-walk recovery in
// RemoveRootObjects.
package cxoutils

import (
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

type tinyValue struct {
	N uint32
}

var tinyRegistry = registry.NewRegistry(func(r *registry.Reg) {
	r.Register("test.tinyValue", tinyValue{})
})

func newTestContainer(t *testing.T) *skyobject.Container {
	t.Helper()
	cfg := skyobject.NewConfig()
	cfg.InMemoryDB = true
	c, err := skyobject.NewContainer(cfg)
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}
	return c
}

// saveRoots publishes n successive Roots into (c, pk, nonce). The
// payload changes each iteration so every Root references at least
// one unique object; this gives the cleanup walk something to do
// when the Root is later deleted.
func saveRoots(t *testing.T, c *skyobject.Container, pk cipher.PubKey, sk cipher.SecKey, nonce uint64, n int) {
	t.Helper()
	if err := c.AddFeed(pk); err != nil {
		t.Fatalf("AddFeed: %v", err)
	}
	if err := c.AddHead(pk, nonce); err != nil {
		t.Fatalf("AddHead: %v", err)
	}
	up, err := c.Unpack(sk, tinyRegistry)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	defer up.Close() //nolint:errcheck
	r := &registry.Root{Pub: pk, Nonce: nonce}
	for i := 0; i < n; i++ {
		sch, serr := tinyRegistry.SchemaByName("test.tinyValue")
		if serr != nil {
			t.Fatalf("SchemaByName: %v", serr)
		}
		var dyn registry.Dynamic
		if verr := dyn.SetValue(up, &tinyValue{N: uint32(i + 1)}); verr != nil { //nolint:gosec
			t.Fatalf("SetValue #%d: %v", i, verr)
		}
		dyn.Schema = sch.Reference()
		r.Refs = []registry.Dynamic{dyn}
		if serr := c.Save(up, r); serr != nil {
			t.Fatalf("Save #%d: %v", i, serr)
		}
	}
}

// TestRemoveRootObjectsSurvivesMissingMidSeq asserts that
// RemoveRootObjects keeps deleting older Roots even when one of the
// intermediate seqs is already gone. The pre-fix loop used
// `continue HeadLoop` on ErrNotFound, which abandoned the entire
// goDown walk on the first gap and left every older Root and its
// orphaned objects behind forever. After the fix, the loop keeps
// going through every seq below `seq - keepLast`, so we end with
// exactly `keepLast` Roots regardless of holes.
func TestRemoveRootObjectsSurvivesMissingMidSeq(t *testing.T) {
	c := newTestContainer(t)
	defer c.Close() //nolint:errcheck
	pk, sk := cipher.GenerateKeyPair()
	const nonce = uint64(42)
	const total = 6

	saveRoots(t, c, pk, sk, nonce, total)

	// Sanity: the head's last seq should now be total-1 = 5.
	last, err := c.LastRootSeq(pk, nonce)
	if err != nil {
		t.Fatalf("LastRootSeq before: %v", err)
	}
	if last != total-1 {
		t.Fatalf("last seq: want %d got %d", total-1, last)
	}

	// Punch a hole at seq 2 to simulate a prior cleanup pass that
	// got partway through (idx entry removed; rc-decrement walk
	// aborted). Pre-fix RemoveRootObjects would hit ErrNotFound
	// here, `continue HeadLoop`, and leave seqs 0/1/3/4 behind.
	if err = c.DelRoot(pk, nonce, 2); err != nil {
		t.Fatalf("seed DelRoot(2): %v", err)
	}

	// keepLast=1: must clear every seq except the last (5).
	if err = RemoveRootObjects(c, 1); err != nil {
		t.Fatalf("RemoveRootObjects: %v", err)
	}

	// Verify by probing — every older seq must come back ErrNotFound
	// when we try to delete it again. (DelRoot is the only public way
	// to ask "does seq exist?" without depending on internal API.)
	for _, seq := range []uint64{0, 1, 3, 4} {
		err = c.DelRoot(pk, nonce, seq)
		if err != data.ErrNotFound {
			t.Errorf("seq %d: expected ErrNotFound after sweep, got %v", seq, err)
		}
	}
	// Seq 5 must survive — LastRootSeq still returns it.
	last, err = c.LastRootSeq(pk, nonce)
	if err != nil {
		t.Fatalf("LastRootSeq after: %v", err)
	}
	if last != total-1 {
		t.Fatalf("last seq after sweep: want %d got %d", total-1, last)
	}
}
