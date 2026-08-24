package treestore

import (
	"path/filepath"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// TestPublisherBatchRollbackDesyncFreeze reproduces the PRODUCTION hub
// freeze that the #4036 self-heal re-encode and the #4041 setFilling
// repair BOTH fail to cure — the residual "long-uptime hub freezes,
// fresh restart clears it" behavior.
//
// Why the existing regression tests miss it: every one of them runs on
// nopNode / nopNodeNoCache, i.e. the IN-MEMORY CXDS. memoryCXDS.RunBatch
// is a pure passthrough (fn(m)) with NO transaction and NO rollback, so a
// failed publish attempt's object writes survive in the store — the
// self-heal's second attempt then finds them and succeeds. Production
// runs on the DRIVE (bbolt) backend, where RunBatch == bolt.Update: a
// non-nil return ROLLS THE TX BACK and discards every object the failed
// attempt wrote to bbolt.
//
// The bug is a write-behind-cache invariant break. Cache.Set on a miss
// does db().Set(scoped-tx) THEN putItem(c.is) — so after attempt-1 rolls
// back, the freshly-encoded objects are GONE from bbolt but still SIT in
// c.is (with a value, cc>0). The #4036 self-heal then clears the encode
// cache and re-encodes from memory, re-Set'ing every object — but for an
// object still resident in c.is that re-Set hits Set's "effective cache
// set" branch (it.cc += inc; touch; return) and NEVER writes to bbolt.
// So attempt-2 "succeeds" (its reference walk is satisfied from c.is) and
// the Root advances ONCE, yet the objects it references are absent from
// CXDS. A subscriber filling that Root, or the publisher itself after the
// orphaned c.is entries later evict (cleanDown's delete → db().Inc →
// ErrNotFound), then wedges permanently on "not found" — the frozen hub.
//
// The deterministic reduction: publish a static (cacheable) sibling plus
// a growing (churning) sibling, evict the static branch's objects from
// BOTH the cache and the CXDS so the next publish's reference walk hits a
// real missing object (forcing the two-attempt self-heal path with a REAL
// bbolt rollback of attempt-1), then advance the growing sibling so
// attempt-1 encodes brand-new objects that the rollback orphans. After
// the self-heal we assert the invariant the fix must guarantee: EVERY
// object the freshly-published Root references is present in the CXDS
// (checked directly, bypassing the cache — exactly what a subscriber or a
// restarted process sees). On unmodified develop the orphaned growing
// objects are missing from bbolt and this FAILS.
func TestPublisherBatchRollbackDesyncFreeze(t *testing.T) {
	dir := t.TempDir()
	pk, sk := cipher.GenerateKeyPair()

	// bbolt-backed node with the object cache ENABLED (production shape).
	n := fileBackedNode(t, filepath.Join(dir, "node"), sk)
	defer func() { _ = n.Close() }() //nolint:errcheck

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
	staticTreeNodeHash := staticNode.pubHash
	staticEntryHash := staticTreeEntryHash(t, c, skyPK, sk)

	// Fully evict the static branch's objects from the cache AND the CXDS
	// so attempt-1's reference walk hits a genuine missing object and its
	// batch rolls back — modeling a pruned/GC'd branch.
	for _, h := range []skycipher.SHA256{staticEntryHash, staticTreeNodeHash} {
		if _, err := c.Inc(h, -1<<20); err != nil { // drop cache rc → evict from c.is
			t.Fatalf("Inc(%s) down: %v", h.Hex(), err)
		}
		if _, _, err := cxds.Get(h, 0); err == nil {
			if err := cxds.Del(h); err != nil {
				t.Fatalf("CXDS.Del(%s): %v", h.Hex(), err)
			}
		}
		if _, _, err := cxds.Get(h, 0); err != data.ErrNotFound {
			t.Fatalf("static object %s should be gone; Get err=%v", h.Hex(), err)
		}
	}

	// Advance the churning sibling: attempt-1 now encodes brand-new
	// objects (the growing TreeNode + leaf + a new root TreeNode) into the
	// batch tx BEFORE the reference walk hits the evicted static object
	// and rolls the tx back — orphaning those new objects in c.is.
	if err := p.Put("growing/n1", []byte("d1")); err != nil {
		t.Fatalf("Put(growing/n1): %v", err)
	}
	if err := p.publishIfDirty(); err != nil {
		t.Fatalf("republish (self-heal path) returned error: %v", err)
	}

	// Root must have advanced to include growing/n1.
	nonce := c.ActiveHead(skyPK)
	if nonce == 0 {
		t.Fatalf("ActiveHead returned zero after publish — root not persisted")
	}
	r, err := c.LastRoot(skyPK, nonce)
	if err != nil {
		t.Fatalf("LastRoot: %v", err)
	}
	if r == nil || len(r.Refs) == 0 {
		t.Fatalf("LastRoot returned empty root")
	}

	// The invariant: every TreeNode object the published Root references
	// must be present in the CXDS itself (not merely reachable through the
	// write-behind cache). Enumerate the Root's TreeNode hashes via the
	// pack, then check each one directly against the CXDS. On unmodified
	// develop the growing branch's TreeNode was orphaned by attempt-1's
	// rollback and its attempt-2 re-Set was a cache-hit no-op, so it is
	// missing here — the served Root cannot be filled.
	up, err := c.Unpack(skycipher.SecKey(sk), Registry)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	defer up.Close() //nolint:errcheck

	hashes := map[skycipher.SHA256]string{}
	if err := collectTreeNodeHashes(up, r.Refs[0].Hash, "", hashes); err != nil {
		t.Fatalf("collectTreeNodeHashes: %v", err)
	}

	var missing []string
	for h, path := range hashes {
		if _, _, err := cxds.Get(h, 0); err != nil {
			missing = append(missing, path+" ("+h.Hex()[:16]+"): "+err.Error())
		}
	}
	if len(missing) != 0 {
		t.Fatalf("published Root references %d object(s) absent from the CXDS — "+
			"the batch-rollback desync freeze (a subscriber filling this Root, or "+
			"this publisher after the orphaned cache entries evict, wedges on "+
			"\"not found\"):\n  %v", len(missing), missing)
	}
}

// collectTreeNodeHashes walks the published Root tree via the pack and
// records the CXDS hash of every TreeNode it reaches (the root node named
// "" plus each sub-node, keyed by its path). Leaves are inline in their
// parent's TreeEntry, so the objects that must exist in the CXDS are the
// TreeNodes themselves; the growing branch's orphaned TreeNode is caught
// here.
func collectTreeNodeHashes(up registry.Pack, nodeHash skycipher.SHA256, path string, out map[skycipher.SHA256]string) error {
	if nodeHash == (skycipher.SHA256{}) {
		return nil
	}
	label := path
	if label == "" {
		label = "<root>"
	}
	out[nodeHash] = label

	// Decode the TreeNode via a Ref (reads through the pack/cache, which
	// still holds the orphaned objects — traversal succeeds so we can
	// enumerate hashes; the CXDS presence check happens in the caller).
	var node TreeNode
	ref := registry.Ref{Hash: nodeHash}
	if err := ref.Value(up, &node); err != nil {
		// Node object itself is unfetchable; recorded above, caller reports it.
		return nil //nolint:nilerr
	}
	ln, err := node.Children.Len(up)
	if err != nil {
		return nil //nolint:nilerr
	}
	for i := 0; i < ln; i++ {
		var te TreeEntry
		if _, err := node.Children.ValueByIndex(up, i, &te); err != nil {
			continue
		}
		if len(te.Leaf) > 0 || te.Sub.Hash == (skycipher.SHA256{}) {
			continue
		}
		childPath := te.Name
		if path != "" {
			childPath = path + "/" + te.Name
		}
		if err := collectTreeNodeHashes(up, te.Sub.Hash, childPath, out); err != nil {
			return err
		}
	}
	return nil
}
