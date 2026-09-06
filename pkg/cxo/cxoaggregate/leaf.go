// Package cxoaggregate pkg/cxo/cxoaggregate/leaf.go c4-net-discovery
//
// Tree-walk helpers shared by the fan-in aggregators. Both are tolerant
// of a PARTIAL fill: an object the fill never fetched simply reads as
// absent rather than erroring the whole walk, which is what lets the
// discovery-leaf recovery paths salvage a small top-level leaf from a
// Root whose bulky subtree never landed.
package cxoaggregate

import (
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"

	skycipher "github.com/skycoin/skycoin/src/cipher"
)

// LeafByName returns the leaf value stored directly under n at the given
// top-level name. Flat feeds (one leaf per name, no sub-nodes) need only
// this single level of lookup — no recursive walk.
func LeafByName(pack registry.Pack, n *treestore.TreeNode, name string) ([]byte, bool) {
	count, err := n.Children.Len(pack)
	if err != nil {
		return nil, false
	}
	for i := 0; i < count; i++ {
		var entry treestore.TreeEntry
		if _, err := n.Children.ValueByIndex(pack, i, &entry); err != nil {
			continue
		}
		if entry.Name == name && len(entry.Leaf) > 0 {
			return entry.Leaf, true
		}
	}
	return nil, false
}

// ChildByIndex returns the inline leaf bytes or the sub-node of n's child at
// the given POSITION, verifying that the child found there is really named
// want. Exactly one of leaf/sub is non-nil on ok=true.
//
// This is the cheap addressing mode, and the reason it exists: a level's
// children are individual objects fetched one per Get, so ChildByName costs up
// to one network round-trip per child, while this costs one. It is only usable
// when the reader can compute the position — treestore writes each level's
// children in sorted Name order (publisher.go sortedNames), so a publisher
// that keeps a level DENSE over a known name space (every child always
// published, empty ones included) makes the index a pure function of the key
// being looked up. See pkg/deployment/ar/arfeed, and note the fan-out it
// chooses: registry.Refs has degree 16, so a level wider than 16 makes an
// indexed fetch walk the branch nodes preceding the target and costs more for
// a key late in the level than for one early in it.
//
// The name check is what makes it safe: if the level is not the dense shape
// the caller assumed — an older publisher, a partial fill — the position holds
// some other child and this reports a miss instead of the wrong record.
func ChildByIndex(pack registry.Pack, n *treestore.TreeNode, index int, want string) (leaf []byte, sub *treestore.TreeNode, ok bool) {
	if index < 0 {
		return nil, nil, false
	}
	count, err := n.Children.Len(pack)
	if err != nil || index >= count {
		return nil, nil, false
	}
	var entry treestore.TreeEntry
	if _, err := n.Children.ValueByIndex(pack, index, &entry); err != nil {
		return nil, nil, false
	}
	if entry.Name != want {
		return nil, nil, false
	}
	if len(entry.Leaf) > 0 {
		return entry.Leaf, nil, true
	}
	if entry.Sub.Hash != (skycipher.SHA256{}) {
		var s treestore.TreeNode
		if err := entry.Sub.Value(pack, &s); err != nil {
			return nil, nil, false
		}
		return nil, &s, true
	}
	return nil, nil, false
}

// ChildByNameSorted returns the inline leaf bytes or sub-node of n's child
// with the given name, found by BINARY SEARCH rather than a scan. Valid
// because treestore encodes each level's children in sorted Name order; costs
// O(log N) fetches where ChildByName costs O(N).
//
// Use it as the fallback when ChildByIndex misses: it does not require the
// level to be dense, only sorted.
func ChildByNameSorted(pack registry.Pack, n *treestore.TreeNode, name string) (leaf []byte, sub *treestore.TreeNode, ok bool) {
	count, err := n.Children.Len(pack)
	if err != nil {
		return nil, nil, false
	}
	lo, hi := 0, count-1
	for lo <= hi {
		mid := int(uint(lo+hi) >> 1)
		var entry treestore.TreeEntry
		if _, err := n.Children.ValueByIndex(pack, mid, &entry); err != nil {
			return nil, nil, false // unreadable child: the search can't continue
		}
		switch {
		case entry.Name < name:
			lo = mid + 1
		case entry.Name > name:
			hi = mid - 1
		default:
			if len(entry.Leaf) > 0 {
				return entry.Leaf, nil, true
			}
			if entry.Sub.Hash != (skycipher.SHA256{}) {
				var s treestore.TreeNode
				if err := entry.Sub.Value(pack, &s); err != nil {
					return nil, nil, false
				}
				return nil, &s, true
			}
			return nil, nil, false
		}
	}
	return nil, nil, false
}

// ChildByName returns the inline leaf bytes or the sub-node of n's child
// with the given name. An unreadable child (its object absent from the
// store, i.e. never fetched by a partial fill) yields ok=false rather
// than erroring the walk. Exactly one of leaf/sub is non-nil on ok=true.
func ChildByName(pack registry.Pack, n *treestore.TreeNode, name string) (leaf []byte, sub *treestore.TreeNode, ok bool) {
	count, err := n.Children.Len(pack)
	if err != nil {
		return nil, nil, false
	}
	for i := 0; i < count; i++ {
		var entry treestore.TreeEntry
		if _, err := n.Children.ValueByIndex(pack, i, &entry); err != nil {
			continue // this child's index object wasn't fetched; skip it
		}
		if entry.Name != name {
			continue
		}
		if len(entry.Leaf) > 0 {
			return entry.Leaf, nil, true
		}
		if entry.Sub.Hash != (skycipher.SHA256{}) {
			var s treestore.TreeNode
			if err := entry.Sub.Value(pack, &s); err != nil {
				return nil, nil, false // sub-node object not fetched
			}
			return nil, &s, true
		}
		return nil, nil, false
	}
	return nil, nil, false
}
