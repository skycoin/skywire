package treestore

import (
	"bytes"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
)

// nopNodeNoCache is nopNode with the Container's in-memory object cache
// switched off (CacheMaxAmount=0). With the cache disabled every
// reference-walk Get/Inc goes straight to CXDS — exactly like a fresh
// post-crash-restart process whose in-memory cache is empty but whose
// persisted CXDS still holds (or, once GC'd, no longer holds) the
// object. That makes a CXDS.Del a true sole-copy eviction, which is
// what the freeze reduces to; with the default write-behind cache on,
// the reference walk would silently satisfy the missing hash from the
// cache and never expose the bug.
func nopNodeNoCache(t *testing.T) *node.Node {
	t.Helper()
	cfg := node.NewConfig()
	cfg.TCP.Listen = ""
	cfg.UDP.Listen = ""
	cfg.RPC = ""
	cfg.Config.InMemoryDB = true
	cfg.Config.CacheMaxAmount = 0 // switch the object cache off

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

// TestPublisherSelfHealsEvictedCachedObject reproduces the production
// "frozen Root" freeze and proves the self-heal fix.
//
// Background: the publisher keeps a per-memNode encode cache. When a
// sub-node is unchanged since its last successful publish encodeNode
// reuses its cached pubHash directly in the parent TreeEntry WITHOUT
// re-serializing that subtree. Container.Save's reference walk, when it
// descends through a CHANGED parent, still visits the unchanged sibling
// branch and increments the rc on the object it reaches there — without
// re-creating it. If that object has been evicted from the local CXDS
// (refcount GC / prune / crash-restart residue) the walk's Inc returns
// data.ErrNotFound, publishRoot returns the error, and — because the
// sub-node stays cached — every subsequent republish re-hits the same
// missing object. The published Root freezes forever at its last
// fully-encodable state. A live HV logged `treestore: heartbeat
// republish failed error="not found"` every ~20s while its served
// transport list stayed frozen at 175 entries though the live set had
// grown to 825.
//
// Tree shape: the root holds two sibling subtrees — "static" (never
// mutated, so its branch stays cached) and "growing" (a new leaf each
// tick, forcing the root to be re-encoded). When "growing" changes the
// root is re-created and Save's reference walk descends into it and
// visits "static"'s (unchanged, hence non-recreated) TreeEntry element
// — that element hash is the object we evict.
//
// The test drives publishRoot synchronously (no runLoop goroutine, so
// the sequence is deterministic), with the object cache disabled so a
// CXDS.Del is a real eviction. It asserts the republish SUCCEEDS, the
// evicted object is re-Added (proving the self-heal re-encode path
// fired rather than the fast path), and the served Root reflects the
// current tree.
func TestPublisherSelfHealsEvictedCachedObject(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	n := nopNodeNoCache(t)
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

	// Publish once. "static/x" stays unchanged across publishes so its
	// branch caches; "growing/n0" is the churning sibling.
	if err := p.Put("static/x", []byte("v1")); err != nil {
		t.Fatalf("Put(static/x): %v", err)
	}
	if err := p.Put("growing/n0", []byte("d0")); err != nil {
		t.Fatalf("Put(growing/n0): %v", err)
	}
	if err := p.publishIfDirty(); err != nil {
		t.Fatalf("first publishIfDirty: %v", err)
	}

	// The "static" sub-node must now be cached with a real pubHash: on
	// the next publish encodeNode reuses that pubHash (the static
	// sub-TreeNode) directly instead of re-serializing the branch.
	staticNode := p.root.subs["static"]
	if staticNode == nil || !staticNode.cached || staticNode.pubHash == (skycipher.SHA256{}) {
		t.Fatalf("expected static sub-node cached with a non-zero pubHash after first publish; got %+v", staticNode)
	}
	staticTreeNodeHash := staticNode.pubHash

	// The "static" TreeEntry element is the object hanging directly off
	// the root; the sub-TreeNode it points at (staticTreeNodeHash) is
	// the object the fast path reuses without re-adding. We evict BOTH:
	// removing the element forces the changed-root reference walk to
	// re-serialize + descend into the static TreeEntry (via the parent's
	// AppendValues), and removing the sub-TreeNode makes that descent hit
	// a missing object — exactly the frozen-Root condition. In
	// production a pruned/GC'd branch loses all its objects together, so
	// evicting the branch's objects models that faithfully.
	staticEntryHash := staticTreeEntryHash(t, c, skyPK, sk)

	for _, h := range []skycipher.SHA256{staticEntryHash, staticTreeNodeHash} {
		if _, _, err := cxds.Get(h, 0); err != nil {
			t.Fatalf("static branch object %s should exist before eviction: %v", h.Hex(), err)
		}
		if err := cxds.Del(h); err != nil {
			t.Fatalf("CXDS.Del(%s): %v", h.Hex(), err)
		}
		if _, _, err := cxds.Get(h, 0); err != data.ErrNotFound {
			t.Fatalf("static branch object %s should be gone after Del; Get err = %v (want ErrNotFound)", h.Hex(), err)
		}
	}

	// Advance the churning sibling and republish. The root is re-encoded
	// and Save's reference walk descends into the re-serialized static
	// TreeEntry and Inc's the cached (now dangling) sub-TreeNode. WITHOUT
	// the fix this returns data.ErrNotFound and the served Root freezes
	// (growing/n1 is never served). WITH the fix publishRoot detects the
	// missing object, clears the encode cache on the snapshot, re-encodes
	// from memory (re-serializing + re-Adding every object, including the
	// evicted static branch) and retries once — so this MUST return nil.
	if err := p.Put("growing/n1", []byte("d1")); err != nil {
		t.Fatalf("Put(growing/n1): %v", err)
	}
	if err := p.publishIfDirty(); err != nil {
		t.Fatalf("republish after eviction should self-heal, got: %v", err)
	}

	// The evicted sub-TreeNode must be back in the store — only the
	// slow-path re-encode re-Adds it; the fast path would have reused the
	// hash and left it missing. This proves the self-heal path fired.
	if _, _, err := cxds.Get(staticTreeNodeHash, 0); err != nil {
		t.Fatalf("static sub-TreeNode should be re-Added by the self-heal re-encode: %v", err)
	}

	// The served Root must reflect the CURRENT tree, not a frozen one.
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
		t.Fatalf("walkTree: %v", err)
	}

	want := map[string][]byte{
		"static/x":   []byte("v1"),
		"growing/n0": []byte("d0"),
		"growing/n1": []byte("d1"), // the advance that a frozen Root would drop
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

// staticTreeEntryHash reads the currently published Root and returns the
// content hash of the top-level TreeEntry element named "static". That
// element hash is exactly the object the reference walk of a later,
// changed Root increments for the unchanged (cached) static branch.
func staticTreeEntryHash(t *testing.T, c *skyobject.Container, skyPK skycipher.PubKey, sk cipher.SecKey) skycipher.SHA256 {
	t.Helper()
	nonce := c.ActiveHead(skyPK)
	if nonce == 0 {
		nonce = 1
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
	ln, err := rootNode.Children.Len(up)
	if err != nil {
		t.Fatalf("Children.Len: %v", err)
	}
	for i := 0; i < ln; i++ {
		var te TreeEntry
		h, err := rootNode.Children.ValueByIndex(up, i, &te)
		if err != nil {
			t.Fatalf("ValueByIndex(%d): %v", i, err)
		}
		if te.Name == "static" {
			return h
		}
	}
	t.Fatalf("static TreeEntry not found in published root")
	return skycipher.SHA256{}
}
