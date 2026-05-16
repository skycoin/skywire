// Package treestore — pkg/cxo/treestore/publisher_rehydrate_test.go:
// pins the publisher-restart hydration contract added so that
// subscribers' applySnapshot doesn't observe a truncated tree after
// the publisher process restarts. Without this contract, the first
// Put after restart publishes a Root whose Refs point at a TreeNode
// containing only that single new leaf, every historical leaf gets
// dropped from subscribers' caches, and the application layer sees a
// silent catastrophic data loss across the receive-side feed.
//
// The integration story rides the existing manual E2E plan for the
// full subscriber↔publisher loop; this file pins the publisher-side
// invariant in isolation: Close + reopen on the same DataDir
// reproduces the in-memory tree from the previously-published Root.
package treestore

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// fileBackedNode constructs a CXO Node whose container persists to
// disk under dataDir. Used by the rehydrate tests because the
// in-memory backend the rest of the suite uses doesn't survive a
// Close + reopen cycle.
func fileBackedNode(t *testing.T, dataDir string, sk cipher.SecKey) *node.Node {
	t.Helper()
	cfg := node.NewConfig()
	cfg.SecKey = skycipher.SecKey(sk)
	cfg.Config = skyobject.NewConfig()
	cfg.Config.DataDir = dataDir
	cfg.TCP.Listen = ""
	cfg.UDP.Listen = ""
	cfg.RPC = ""
	n, err := node.NewNode(cfg)
	if err != nil {
		t.Fatalf("NewNode(%q): %v", dataDir, err)
	}
	return n
}

// closePublisher tears down a publisher in the test's preferred order
// (publisher Close → node Close), surfacing the first error and
// failing the test if either step errors.
func closePublisher(t *testing.T, p *Publisher) {
	t.Helper()
	if err := p.Close(); err != nil {
		t.Fatalf("publisher.Close: %v", err)
	}
}

func TestPublisherHydrateFreshNodeNoOp(t *testing.T) {
	// A freshly-constructed Publisher on a brand-new DataDir has no
	// previously-published Root. hydrateFromContainer must short-
	// circuit with no error and leave p.root as the default empty
	// memNode. Pre-fix, the early-return for nonce=0 wasn't there
	// and LastRoot returned data.ErrNoSuchFeed — accepting that as
	// "fresh, skip" is the contract.
	dir := t.TempDir()
	_, sk := cipher.GenerateKeyPair()

	n := fileBackedNode(t, filepath.Join(dir, "node"), sk)
	defer func() {
		_ = n.Close() //nolint:errcheck
	}()

	p, err := New(n, sk, Config{BatchWindow: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer closePublisher(t, p)

	// Empty tree → root has no leaves and no sub-trees.
	if got := len(p.root.leaves); got != 0 {
		t.Errorf("fresh publisher: expected zero leaves, got %d", got)
	}
	if got := len(p.root.subs); got != 0 {
		t.Errorf("fresh publisher: expected zero sub-trees, got %d", got)
	}
}

func TestPublisherHydrateRebuildsTreeAcrossRestart(t *testing.T) {
	// End-to-end of the bug + fix: publish a few leaves through one
	// publisher instance, Close it, reopen on the same DataDir, and
	// confirm the new publisher's in-memory tree mirrors the
	// pre-Close state. Without hydration the second publisher's
	// p.root is empty; with hydration it carries the historical
	// leaves so the next Put publishes a Root with both old + new.
	dataDir := t.TempDir()
	nodeDir := filepath.Join(dataDir, "node")
	_, sk := cipher.GenerateKeyPair()

	// First publisher instance: write three leaves at distinct
	// paths (root-level + a nested path) so the test exercises both
	// leaf and sub-tree hydration branches.
	n1 := fileBackedNode(t, nodeDir, sk)
	p1, err := New(n1, sk, Config{BatchWindow: 10 * time.Millisecond})
	if err != nil {
		_ = n1.Close() //nolint:errcheck
		t.Fatalf("New (instance 1): %v", err)
	}
	puts := []struct {
		path  string
		value []byte
	}{
		{"alpha", []byte("alpha-value")},
		{"beta/one", []byte("beta-one-value")},
		{"beta/two", []byte("beta-two-value")},
	}
	for _, put := range puts {
		if err := p1.Put(put.path, put.value); err != nil {
			_ = p1.Close() //nolint:errcheck
			_ = n1.Close() //nolint:errcheck
			t.Fatalf("Put(%q): %v", put.path, err)
		}
	}
	if err := p1.Flush(); err != nil {
		_ = p1.Close() //nolint:errcheck
		_ = n1.Close() //nolint:errcheck
		t.Fatalf("Flush: %v", err)
	}
	if err := p1.Close(); err != nil {
		_ = n1.Close() //nolint:errcheck
		t.Fatalf("publisher.Close: %v", err)
	}
	if err := n1.Close(); err != nil {
		t.Fatalf("node.Close: %v", err)
	}

	// Second publisher instance: same SecKey, same DataDir. The
	// in-memory tree should be rebuilt from the previously-published
	// Root before runLoop starts.
	n2 := fileBackedNode(t, nodeDir, sk)
	defer func() {
		_ = n2.Close() //nolint:errcheck
	}()
	p2, err := New(n2, sk, Config{BatchWindow: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New (instance 2): %v", err)
	}
	defer closePublisher(t, p2)

	// Verify leaf reachable at root and via the nested sub-tree.
	if got, ok := p2.Get("alpha"); !ok || !bytes.Equal(got, []byte("alpha-value")) {
		t.Errorf("Get(alpha): ok=%v got=%q want=%q", ok, got, "alpha-value")
	}
	if got, ok := p2.Get("beta/one"); !ok || !bytes.Equal(got, []byte("beta-one-value")) {
		t.Errorf("Get(beta/one): ok=%v got=%q want=%q", ok, got, "beta-one-value")
	}
	if got, ok := p2.Get("beta/two"); !ok || !bytes.Equal(got, []byte("beta-two-value")) {
		t.Errorf("Get(beta/two): ok=%v got=%q want=%q", ok, got, "beta-two-value")
	}
}

func TestPublisherHydrateNextPublishContainsHistoricalLeaves(t *testing.T) {
	// The end-to-end consequence the receive-side stall hinged on:
	// after Close + reopen, the FIRST publish has to include
	// historical leaves alongside the newly-added one. Without
	// hydration, the first publishRoot encoded a 1-leaf TreeNode
	// and subscribers' applySnapshot replaced their cache with the
	// truncated tree (emitting delete events for everything
	// historical). With hydration the new Root's TreeNode contains
	// both, and a subscriber-side applySnapshot diff would be a
	// pure insert for the new leaf with no deletes.
	dataDir := t.TempDir()
	nodeDir := filepath.Join(dataDir, "node")
	pk, sk := cipher.GenerateKeyPair()

	n1 := fileBackedNode(t, nodeDir, sk)
	p1, err := New(n1, sk, Config{BatchWindow: 10 * time.Millisecond})
	if err != nil {
		_ = n1.Close() //nolint:errcheck
		t.Fatalf("New (instance 1): %v", err)
	}
	for _, path := range []string{"msgs/one", "msgs/two"} {
		if err := p1.Put(path, []byte(path+"-body")); err != nil {
			_ = p1.Close() //nolint:errcheck
			_ = n1.Close() //nolint:errcheck
			t.Fatalf("Put(%q): %v", path, err)
		}
	}
	if err := p1.Flush(); err != nil {
		_ = p1.Close() //nolint:errcheck
		_ = n1.Close() //nolint:errcheck
		t.Fatalf("Flush: %v", err)
	}
	_ = p1.Close() //nolint:errcheck
	_ = n1.Close() //nolint:errcheck

	n2 := fileBackedNode(t, nodeDir, sk)
	defer func() {
		_ = n2.Close() //nolint:errcheck
	}()
	p2, err := New(n2, sk, Config{BatchWindow: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New (instance 2): %v", err)
	}
	defer closePublisher(t, p2)

	// Add a new leaf and publish. Walk the resulting Root's TreeNode
	// to confirm it contains all three paths (msgs/one, msgs/two
	// from instance 1 + msgs/three from instance 2).
	if err := p2.Put("msgs/three", []byte("msgs/three-body")); err != nil {
		t.Fatalf("Put(msgs/three): %v", err)
	}
	if err := p2.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Read the latest Root and walk its tree to compare against the
	// expected leaf set.
	c := p2.cxoNode.Container()
	skyPK := skycipher.PubKey(pk)
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
	defer func() {
		_ = up.Close() //nolint:errcheck
	}()

	var rootNode TreeNode
	if err := r.Refs[0].Value(up, &rootNode); err != nil {
		t.Fatalf("decode rootNode: %v", err)
	}
	walked := make(map[string][]byte)
	if err := walkTree(up, &rootNode, "", walked); err != nil {
		t.Fatalf("walkTree: %v", err)
	}

	want := map[string][]byte{
		"msgs/one":   []byte("msgs/one-body"),
		"msgs/two":   []byte("msgs/two-body"),
		"msgs/three": []byte("msgs/three-body"),
	}
	for path, wantVal := range want {
		got, ok := walked[path]
		if !ok {
			t.Errorf("post-restart Root missing %q (this is the pre-fix bug)", path)
			continue
		}
		if !bytes.Equal(got, wantVal) {
			t.Errorf("post-restart Root %q: got %q want %q", path, got, wantVal)
		}
	}
	for path := range walked {
		if _, ok := want[path]; !ok {
			t.Errorf("post-restart Root has unexpected path %q", path)
		}
	}
}

func TestHydrateMemNodeWalksLeavesAndSubTrees(t *testing.T) {
	// Direct exercise of the hydration walker. Build a TreeNode with
	// one leaf + one sub-tree (which itself contains a leaf), save
	// it through a Pack, then walk it via hydrateMemNode and confirm
	// the resulting memNode mirror has both shapes. Avoids the
	// publisher-restart machinery so the test fails fast on a regression
	// in the walker itself.
	dir := t.TempDir()
	_, sk := cipher.GenerateKeyPair()

	n := fileBackedNode(t, dir, sk)
	defer func() {
		_ = n.Close() //nolint:errcheck
	}()

	c := n.Container()
	up, err := c.Unpack(skycipher.SecKey(sk), Registry)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	defer func() {
		_ = up.Close() //nolint:errcheck
	}()

	// Build inner TreeNode (one leaf), then outer TreeNode (one leaf
	// at the root + an entry whose Sub points at inner).
	innerLeaf := TreeEntry{Name: "deep", Leaf: []byte("deep-value")}
	innerNode := TreeNode{}
	if err := innerNode.Children.AppendValues(up, innerLeaf); err != nil {
		t.Fatalf("inner.AppendValues: %v", err)
	}
	var innerRef registry.Ref
	if err := innerRef.SetValue(up, &innerNode); err != nil {
		t.Fatalf("inner.SetValue: %v", err)
	}

	outerLeaf := TreeEntry{Name: "alpha", Leaf: []byte("alpha-value")}
	outerSub := TreeEntry{Name: "beta", Sub: innerRef}
	outerNode := TreeNode{}
	if err := outerNode.Children.AppendValues(up, outerLeaf, outerSub); err != nil {
		t.Fatalf("outer.AppendValues: %v", err)
	}

	dest := newMemNode()
	if err := hydrateMemNode(up, &outerNode, dest); err != nil {
		t.Fatalf("hydrateMemNode: %v", err)
	}
	if got, want := string(dest.leaves["alpha"]), "alpha-value"; got != want {
		t.Errorf("dest.leaves[alpha]: got %q want %q", got, want)
	}
	beta, ok := dest.subs["beta"]
	if !ok {
		t.Fatalf("dest.subs[beta]: missing")
	}
	if got, want := string(beta.leaves["deep"]), "deep-value"; got != want {
		t.Errorf("dest.subs[beta].leaves[deep]: got %q want %q", got, want)
	}
}
