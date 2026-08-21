package treestore

import (
	"bytes"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
)

// TestPublisherSelfHealsFillingTrapObject reproduces the PRODUCTION
// [stats]/TPD publisher freeze that #4036 did NOT fix, and proves the
// root-cause repair in Cache.setFilling.
//
// #4036 added a self-heal to publishRoot: on a "not found" during
// encode+save it drops the encode cache on the snapshot and re-encodes
// from memory (re-Set'ing every object) to re-materialize an evicted
// object, then retries once. Its regression test (TestPublisherSelfHeals
// EvictedCachedObject) runs on a CACHE-DISABLED node, where Cache.Set
// writes straight to the CXDS — so the re-Set always restores the object
// and the retry succeeds.
//
// Production runs with the write-behind cache ENABLED. There a
// content-addressed object can be tracked as a "filling" cache item
// (val==nil, retained in c.is because a filler inc keeps it) while its
// backing CXDS object is deleted (refcount GC / prune racing eviction).
// This happens naturally on a densely-shared transport graph: a
// transport A<->B is published in BOTH A's and B's tp-list, so the two
// visors' trees share the SAME content hash; when a visor also fills a
// peer's Root it registers that shared hash as a filler item, and its
// own published object becomes a filling placeholder. Before this fix,
// Cache.Set on such an item routed to setFilling->incFilling->db.Get —
// which only READS and returns "not found", never writing the value. So
// the self-heal re-encode could not restore the object: the retry failed
// identically and the served Root froze forever (heartbeat "republish
// failed: not found" every tick; TPD stuck reflecting a stale fraction).
//
// The tree mirrors the #4036 test: a "static" (cached) sibling and a
// "growing" (churning) sibling. We put the static branch's sub-TreeNode
// into the filling+missing-backing state, and additionally evict its
// parent TreeEntry element so the changed-root reference walk descends
// into it and reaches the trapped object. WITHOUT the fix publishRoot
// returns "not found" and growing/n1 is dropped (frozen Root); WITH the
// fix the re-encode's Set persists the value, the retry succeeds, and the
// served Root advances to the full current tree.
func TestPublisherSelfHealsFillingTrapObject(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	n := nopNode(t) // cache ENABLED (default CacheMaxAmount), unlike the #4036 test
	skyPK := skycipher.PubKey(pk)
	if err := n.Share(skyPK); err != nil {
		t.Fatalf("Share: %v", err)
	}
	p := &Publisher{
		log: mustLogger(), cxoNode: n, pk: pk, sk: sk,
		root:         newMemNode(),
		allow:        newAllowState(nil),
		batchWindow:  10 * time.Millisecond,
		wakeup:       make(chan struct{}, 1),
		done:         make(chan struct{}),
		cleanupNudge: make(chan struct{}, 1),
		cleanupDone:  make(chan struct{}),
	}
	close(p.cleanupDone)

	c := p.cxoNode.Container()
	cxds := c.DB().CXDS()

	if err := p.Put("static/x", []byte("v1")); err != nil {
		t.Fatalf("Put(static/x): %v", err)
	}
	if err := p.Put("growing/n0", []byte("d0")); err != nil {
		t.Fatalf("Put(growing/n0): %v", err)
	}
	if err := p.publishIfDirty(); err != nil {
		t.Fatalf("first publishIfDirty: %v", err)
	}

	staticNode := p.root.subs["static"]
	if staticNode == nil || !staticNode.cached || staticNode.pubHash == (skycipher.SHA256{}) {
		t.Fatalf("expected static sub-node cached with a pubHash; got %+v", staticNode)
	}
	staticHash := staticNode.pubHash

	// Put the static sub-TreeNode into the filling+missing-backing state:
	// give it a filler inc (so eviction keeps it as a filling placeholder),
	// drive its cached rc to zero (evicts the value but retains the c.is
	// entry because fc>0), then delete its backing object from the CXDS.
	fillCh := make(chan skyobject.Object, 1)
	if err := c.Want(staticHash, fillCh, 1); err != nil {
		t.Fatalf("Want(staticHash): %v", err)
	}
	if _, err := c.Inc(staticHash, -1<<20); err != nil {
		t.Fatalf("Inc(staticHash) down: %v", err)
	}
	if !c.IsCached(staticHash) {
		t.Fatalf("expected staticHash retained as a filling cache item")
	}
	if _, _, err := cxds.Get(staticHash, 0); err == nil {
		if err := cxds.Del(staticHash); err != nil {
			t.Fatalf("Del(staticHash): %v", err)
		}
	}
	if _, _, err := cxds.Get(staticHash, 0); err != data.ErrNotFound {
		t.Fatalf("staticHash should be gone from CXDS; Get err=%v", err)
	}

	// Fully evict the parent "static" TreeEntry element (from cache AND
	// CXDS) so the next re-encode re-creates it fresh (created=true) and
	// Save's reference walk DESCENDS into it, reaching the trapped
	// staticHash — otherwise the unchanged-subtree dedup would skip it.
	entryHash := staticTreeEntryHash(t, c, skyPK, sk)
	if _, err := c.Inc(entryHash, -1<<20); err != nil {
		t.Fatalf("Inc(entryHash) down: %v", err)
	}
	if _, _, err := cxds.Get(entryHash, 0); err == nil {
		if err := cxds.Del(entryHash); err != nil {
			t.Fatalf("Del(entryHash): %v", err)
		}
	}

	// Advance the churning sibling and republish. The reference walk hits
	// the trapped staticHash; the self-heal re-encodes and must now be able
	// to Set the value back. WITHOUT the fix this returns "not found".
	if err := p.Put("growing/n1", []byte("d1")); err != nil {
		t.Fatalf("Put(growing/n1): %v", err)
	}
	if err := p.publishIfDirty(); err != nil {
		t.Fatalf("republish should self-heal the filling-trap object, got: %v", err)
	}

	// The trapped object must be back in the store — proving Set (not just
	// Inc) re-materialized it via the setFilling repair.
	if _, _, err := cxds.Get(staticHash, 0); err != nil {
		t.Fatalf("staticHash should be restored by the setFilling repair: %v", err)
	}

	// The served Root must reflect the CURRENT tree, not a frozen one.
	nonce := c.ActiveHead(skyPK)
	if nonce == 0 {
		t.Fatalf("ActiveHead returned zero after publish")
	}
	r, err := c.LastRoot(skyPK, nonce)
	if err != nil {
		t.Fatalf("LastRoot: %v", err)
	}
	up, err := c.Unpack(skycipher.SecKey(sk), Registry)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	defer up.Close() //nolint:errcheck

	var rootNode TreeNode
	if err := r.Refs[0].Value(up, &rootNode); err != nil {
		t.Fatalf("decode rootNode: %v", err)
	}
	served := make(map[string][]byte)
	if err := walkTree(up, &rootNode, "", served); err != nil {
		t.Fatalf("walkTree (a dangling reference here means the Root is broken): %v", err)
	}

	want := map[string][]byte{
		"static/x":   []byte("v1"),
		"growing/n0": []byte("d0"),
		"growing/n1": []byte("d1"), // the advance a frozen Root would drop
	}
	for path, wantVal := range want {
		got, ok := served[path]
		if !ok {
			t.Errorf("served Root missing %q — Root is frozen (the pre-fix bug)", path)
			continue
		}
		if !bytes.Equal(got, wantVal) {
			t.Errorf("served Root %q = %q, want %q", path, got, wantVal)
		}
	}
	for path := range served {
		if _, ok := want[path]; !ok {
			t.Errorf("served Root has unexpected path %q", path)
		}
	}
}
