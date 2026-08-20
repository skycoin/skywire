package skyobject

import (
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

// TestCacheSetRepairsFillingItemWithMissingBacking proves the
// filling-invariant repair in Cache.setFilling.
//
// A "filling" cache item (val==nil, so isFilling()==true) is tracked on
// the assumption that its value already lives in the CXDS — incFilling
// only Get/Inc's it. When the backing object is deleted while the
// placeholder lingers in c.is (refcount GC / prune racing a cache
// eviction), that invariant is violated. Before the repair, Cache.Set on
// such an item routed to setFilling->incFilling->db.Get, which returns
// "not found" and never writes the value — so the object could never be
// restored (the treestore self-heal freeze). After the repair, Set
// carries the value and persists it, restoring the object.
func TestCacheSetRepairsFillingItemWithMissingBacking(t *testing.T) {
	conf := NewConfig()
	conf.InMemoryDB = true
	c, err := NewContainer(conf)
	if err != nil {
		t.Fatalf("NewContainer: %v", err)
	}
	defer c.Close() //nolint:errcheck

	val := []byte("payload")
	key := cipher.SumSHA256(val)

	// Create a regular cached object (rc=1).
	if _, err := c.Set(key, val, 1); err != nil {
		t.Fatalf("initial Set: %v", err)
	}
	// Register a filler inc so that eviction keeps a filling placeholder.
	ch := make(chan Object, 1)
	if err := c.Want(key, ch, 1); err != nil {
		t.Fatalf("Want: %v", err)
	}
	// Drive the cached rc to zero: delete() evicts the value but, because
	// fc>0, retains the c.is entry as a filling placeholder (val==nil).
	if _, err := c.Inc(key, -1<<20); err != nil {
		t.Fatalf("Inc down: %v", err)
	}
	if !c.IsCached(key) {
		t.Fatalf("expected a filling placeholder retained in the cache")
	}
	// Delete the backing object from the CXDS — the invariant violation.
	cxds := c.DB().CXDS()
	if err := cxds.Del(key); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if _, _, err := cxds.Get(key, 0); err != data.ErrNotFound {
		t.Fatalf("backing object should be gone; Get err=%v", err)
	}

	// Set the value again (what the treestore self-heal re-encode does).
	// The repair must persist it rather than returning "not found".
	if _, err := c.Set(key, val, 1); err != nil {
		t.Fatalf("Set on filling item with missing backing should repair, got: %v", err)
	}

	// The value must now be retrievable from the CXDS again.
	got, _, err := cxds.Get(key, 0)
	if err != nil {
		t.Fatalf("value should be restored in the CXDS after the repair Set: %v", err)
	}
	if string(got) != string(val) {
		t.Fatalf("restored value = %q, want %q", got, val)
	}
}
