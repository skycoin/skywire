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
