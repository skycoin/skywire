package treestore

import (
	"bytes"
	"sync"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// nopNode builds a CXO Node with an in-memory container and no
// network listeners. Sufficient for exercising publishRoot end-to-end
// — the broadcast side is a no-op when no peers are connected.
func nopNode(t *testing.T) *node.Node {
	t.Helper()
	cfg := node.NewConfig()
	cfg.TCP.Listen = ""
	cfg.UDP.Listen = ""
	cfg.RPC = ""
	cfg.Config.InMemoryDB = true

	n, err := node.NewNode(cfg)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	t.Cleanup(func() {
		if err := n.Close(); err != nil {
			t.Logf("node.Close: %v", err)
		}
	})
	return n
}

func newTestPublisher(t *testing.T) (*Publisher, cipher.SecKey) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	_ = pk
	p, err := New(nopNode(t), sk, Config{BatchWindow: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Logf("publisher.Close: %v", err)
		}
	})
	return p, sk
}

func TestPublisherInMemoryGetWalk(t *testing.T) {
	p, _ := newTestPublisher(t)

	if err := p.Put("tiers/dmsg/2026-04-26", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := p.Put("tiers/dmsg/2026-04-27", []byte("second")); err != nil {
		t.Fatal(err)
	}
	if err := p.Put("tiers/process/2026-04-27", []byte("third")); err != nil {
		t.Fatal(err)
	}
	if err := p.Put("services/vpn-server/2026-04-27", []byte("vpn")); err != nil {
		t.Fatal(err)
	}

	if v, ok := p.Get("tiers/dmsg/2026-04-26"); !ok || string(v) != "first" {
		t.Fatalf("Get tiers/dmsg/2026-04-26 = (%q, %v)", v, ok)
	}

	var paths []string
	p.Walk("tiers/", func(path string, _ []byte) bool {
		paths = append(paths, path)
		return true
	})
	want := []string{"tiers/dmsg/2026-04-26", "tiers/dmsg/2026-04-27", "tiers/process/2026-04-27"}
	if !equalSlice(paths, want) {
		t.Fatalf("Walk(tiers/) = %v, want %v", paths, want)
	}

	// Caller-supplied prefix must be segment-aware.
	paths = nil
	p.Walk("services", func(path string, _ []byte) bool {
		paths = append(paths, path)
		return true
	})
	if len(paths) != 1 || paths[0] != "services/vpn-server/2026-04-27" {
		t.Fatalf("Walk(services) = %v", paths)
	}
}

func TestPublisherFlushSavesRoot(t *testing.T) {
	p, sk := newTestPublisher(t)

	if err := p.Put("a/b", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	c := p.cxoNode.Container()
	skyPK := skycipher.MustPubKeyFromSecKey(skycipher.SecKey(sk))
	nonce := c.ActiveHead(skyPK)
	if nonce == 0 {
		nonce = 1
	}
	r, err := c.LastRoot(skyPK, nonce)
	if err != nil {
		t.Fatalf("LastRoot: %v", err)
	}
	if r == nil || r.Hash == (skycipher.SHA256{}) {
		t.Fatal("expected a non-zero Root hash after Flush")
	}
	if len(r.Refs) != 1 {
		t.Fatalf("Root.Refs len = %d, want 1", len(r.Refs))
	}

	// Walk the Root and verify a non-trivial number of objects were
	// written (root TreeNode + Refs index + TreeEntry + leaf).
	count := 0
	if err := c.Walk(r, func(_ skycipher.SHA256, _ int) (bool, error) {
		count++
		return true, nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if count < 2 {
		t.Fatalf("Walk visited %d hashes, expected ≥2 for root tree + entries", count)
	}
}

func TestPublisherUnchangedSubtreeKeepsHash(t *testing.T) {
	p, sk := newTestPublisher(t)

	if err := p.Put("a/x", []byte("ax")); err != nil {
		t.Fatal(err)
	}
	if err := p.Put("b/y", []byte("by")); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}

	skyPK := skycipher.MustPubKeyFromSecKey(skycipher.SecKey(sk))
	c := p.cxoNode.Container()
	nonce := c.ActiveHead(skyPK)

	r1, err := c.LastRoot(skyPK, nonce)
	if err != nil {
		t.Fatal(err)
	}
	r1Refs0Hash := r1.Refs[0].Hash

	// Snapshot the set of object hashes reachable from r1.
	r1Set := walkHashSet(t, c, r1)

	// Now mutate only b/y. The "a" subtree should keep its hash,
	// which means r1's TreeEntry for "a" remains in the new graph.
	if err := p.Put("b/y", []byte("by-updated")); err != nil {
		t.Fatal(err)
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}

	r2, err := c.LastRoot(skyPK, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Hash == r1.Hash {
		t.Fatal("Root hash should change after a mutation")
	}
	if r2.Refs[0].Hash == r1Refs0Hash {
		t.Fatal("Root TreeNode hash should change because a child entry changed")
	}

	r2Set := walkHashSet(t, c, r2)

	// At least some objects (the unchanged "a" subtree's entry +
	// leaf, plus their TreeNode container if any) should be shared
	// between the two Roots — that's the content-addressing win.
	shared := 0
	for h := range r2Set {
		if _, ok := r1Set[h]; ok {
			shared++
		}
	}
	if shared == 0 {
		t.Fatal("expected at least one object hash to be shared between Roots; got 0 (content-addressing broken)")
	}
}

// walkHashSet returns the set of object hashes reachable from r in
// container c. Used by the dedup test to confirm shared objects
// across consecutive Roots.
func walkHashSet(t *testing.T, c *skyobject.Container, r *registry.Root) map[skycipher.SHA256]struct{} {
	t.Helper()
	out := map[skycipher.SHA256]struct{}{}
	if err := c.Walk(r, func(h skycipher.SHA256, _ int) (bool, error) {
		out[h] = struct{}{}
		return true, nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return out
}

func TestPublisherDeleteAndPrune(t *testing.T) {
	p, _ := newTestPublisher(t)

	for _, path := range []string{"a/x", "a/y", "b/z"} {
		if err := p.Put(path, []byte(path)); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Flush(); err != nil {
		t.Fatal(err)
	}

	// Delete a single leaf — sibling stays.
	if err := p.Delete("a/x"); err != nil {
		t.Fatal(err)
	}
	if v, ok := p.Get("a/y"); !ok || !bytes.Equal(v, []byte("a/y")) {
		t.Fatalf("sibling lost after delete: %q ok=%v", v, ok)
	}

	// PrunePrefix wipes an entire subtree.
	if err := p.PrunePrefix("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Get("a/y"); ok {
		t.Fatal("a/y should be gone after PrunePrefix(a)")
	}
	if v, ok := p.Get("b/z"); !ok || !bytes.Equal(v, []byte("b/z")) {
		t.Fatal("PrunePrefix should not affect b/z")
	}

	// PrunePrefix("") clears everything.
	if err := p.PrunePrefix(""); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.Get("b/z"); ok {
		t.Fatal("PrunePrefix(\"\") should clear all leaves")
	}
}

func TestPublisherPathConflictReporting(t *testing.T) {
	p, _ := newTestPublisher(t)
	if err := p.Put("a/b", []byte("v")); err != nil {
		t.Fatal(err)
	}
	// Putting a leaf at a position currently holding a sub-tree
	// must surface PathConflictError, not silently overwrite.
	err := p.Put("a", []byte("conflict"))
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*PathConflictError); !ok {
		t.Fatalf("expected *PathConflictError, got %T (%v)", err, err)
	}
}

// TestEncodeCacheInvalidatesOnlyAffectedPath pins the per-memNode
// encode-cache contract: after a publish, every sub-node along an
// untouched branch must remain `cached==true` so the next publish
// can short-circuit the recursive encodeNode walk and skip the
// encoder.Serialize / Cache.Set chain for that branch. A Put under
// one branch must clear cached on its ancestors only, leaving sibling
// branches reusable.
func TestEncodeCacheInvalidatesOnlyAffectedPath(t *testing.T) {
	p, _ := newTestPublisher(t)

	for _, path := range []string{
		"a/x/1",
		"a/y/1",
		"b/x/1",
		"b/y/1",
	} {
		if err := p.Put(path, []byte("v")); err != nil {
			t.Fatalf("Put %s: %v", path, err)
		}
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	p.mu.Lock()
	root := p.root
	a := root.subs["a"]
	b := root.subs["b"]
	ax := a.subs["x"]
	ay := a.subs["y"]
	bx := b.subs["x"]
	by := b.subs["y"]
	for name, n := range map[string]*memNode{"a": a, "b": b, "a/x": ax, "a/y": ay, "b/x": bx, "b/y": by} {
		if !n.cached {
			t.Fatalf("after first Flush, %s.cached = false; want true", name)
		}
	}
	p.mu.Unlock()

	// Mutate only under a/x. Expect cached to clear on the path
	// root → a → a/x but stay set on every sibling.
	if err := p.Put("a/x/2", []byte("w")); err != nil {
		t.Fatalf("Put a/x/2: %v", err)
	}

	p.mu.Lock()
	if a.cached {
		t.Fatalf("a.cached = true after mutation under a/x; want false")
	}
	if ax.cached {
		t.Fatalf("a/x.cached = true after mutation under a/x; want false")
	}
	if !ay.cached {
		t.Fatalf("a/y.cached = false after unrelated mutation; want true (sibling)")
	}
	if !b.cached {
		t.Fatalf("b.cached = false after unrelated mutation; want true (sibling subtree)")
	}
	if !bx.cached || !by.cached {
		t.Fatalf("b/x.cached=%v b/y.cached=%v; want both true (unrelated subtree)", bx.cached, by.cached)
	}
	bxHashBefore := bx.pubHash
	byHashBefore := by.pubHash
	p.mu.Unlock()

	if err := p.Flush(); err != nil {
		t.Fatalf("second Flush: %v", err)
	}

	// After the second publish, sibling subtree hashes must be
	// preserved bit-for-bit — the publisher reused them via the cache
	// path rather than re-encoding.
	p.mu.Lock()
	defer p.mu.Unlock()
	if bx.pubHash != bxHashBefore {
		t.Fatalf("b/x.pubHash changed across an unrelated publish: %x → %x", bxHashBefore, bx.pubHash)
	}
	if by.pubHash != byHashBefore {
		t.Fatalf("b/y.pubHash changed across an unrelated publish: %x → %x", byHashBefore, by.pubHash)
	}
	// And every sub-node should be cached again post-publish.
	for name, n := range map[string]*memNode{"a": a, "b": b, "a/x": ax, "a/y": ay, "b/x": bx, "b/y": by} {
		if !n.cached {
			t.Fatalf("after second Flush, %s.cached = false; want true", name)
		}
	}
}

func TestPublisherConcurrentPutsAreSafe(t *testing.T) {
	p, _ := newTestPublisher(t)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path, jErr := JoinPath("g", "k", string(rune('a'+i%26)))
			if jErr != nil {
				return
			}
			if err := p.Put(path, []byte{byte(i & 0xff)}); err != nil { //nolint:gosec
				return
			}
		}(i)
	}
	wg.Wait()
	if err := p.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}
