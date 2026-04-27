// Package treestore provides a hierarchical, prefix-routable
// key-value store published over CXO. Unlike the flat
// pkg/cxo/node/kv.go (which jams the entire map as JSON into a
// single Root.Descriptor field and re-broadcasts the whole thing on
// every Put), TreeStore models its dataset as a content-addressed
// object graph: each path level is a TreeNode whose Refs contain
// TreeEntry objects, and only the entries that change need to be
// re-encoded and re-broadcast. CXO's content-addressing means
// subscribers reuse unchanged-hash branches from the previous Root
// and only fetch the diff.
//
// Path semantics: slash-separated string segments (no escaping;
// segments must not contain '/'). Empty path "" addresses the root.
// Examples: "tiers/dmsg/2026-04-27", "visor/03ab.../transports/<id>".
//
// Subscribers connect by feed PK (which is the publisher node's PK —
// no separate keypair) and walk the tree via the Subscriber API.
// Prefix-filter at the subscriber API level today; Filler-level
// subtree skipping is a follow-up CXO core change tracked separately.
package treestore

import (
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// TreeNode is the on-the-wire representation of one level in the
// tree. Children carries the entries at this level; entries are
// looked up by Name (linear scan; cardinality at any level is
// expected to be small to moderate).
type TreeNode struct {
	Children registry.Refs `skyobject:"schema=cxo.treestore.TreeEntry"`
}

// TreeEntry describes a single named position at a tree level.
// Exactly one of Sub or Leaf must be set:
//   - Sub non-zero: this entry is an interior node; recurse into Sub
//     to walk its sub-tree.
//   - Leaf non-empty: this entry is a leaf carrying value bytes
//     directly (no further hashing for the value, since each leaf
//     is uniquely positioned in our intended uses).
//
// Inlining the leaf value here trades a small per-entry size hit for
// half as many object hashes per leaf and a simpler subscriber walk
// (no extra Get round-trip per leaf). At our cardinalities this is
// the right call; if leaves grow large, switch Leaf to a registry.Ref
// and add a LeafValue object type — the subscriber code only needs
// to add one fetch hop.
type TreeEntry struct {
	Name string
	Sub  registry.Ref `skyobject:"schema=cxo.treestore.TreeNode"`
	Leaf []byte
}

// Registry registers the TreeStore schema types. Constructed once at
// package init since the schema is fixed; callers reuse this single
// instance for both publisher Unpack and subscriber Pack.
var Registry = registry.NewRegistry(func(r *registry.Reg) {
	r.Register("cxo.treestore.TreeNode", TreeNode{})
	r.Register("cxo.treestore.TreeEntry", TreeEntry{})
})
