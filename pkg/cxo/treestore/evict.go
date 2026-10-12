package treestore

import (
	"fmt"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// A publisher with DropPublishedLeaves keeps no leaf bytes for a node it has
// published: the values stay as nil under their names, and evictedFrom names
// the published TreeNode that holds them. Encoding such a node fills the nil
// values from evictedFrom first, and Get and Walk read them from it.

// evict drops n's leaf bytes, which the TreeNode published as hash holds.
func evict(n *memNode, hash skycipher.SHA256) {
	if len(n.leaves) == 0 {
		return
	}
	for name := range n.leaves {
		n.leaves[name] = nil
	}
	n.evictedFrom = hash
}

// publishedLeaves reads the leaf entries of the TreeNode published as hash.
func publishedLeaves(up registry.Pack, hash skycipher.SHA256) (map[string][]byte, error) {
	var node TreeNode
	ref := registry.Ref{Hash: hash}
	if err := ref.Value(up, &node); err != nil {
		return nil, fmt.Errorf("treestore: load evicted node %s: %w", hash.Hex()[:16], err)
	}
	n, err := node.Children.Len(up)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, n)
	for i := 0; i < n; i++ {
		var entry TreeEntry
		if _, err := node.Children.ValueByIndex(up, i, &entry); err != nil {
			return nil, err
		}
		if len(entry.Leaf) > 0 {
			out[entry.Name] = entry.Leaf
		}
	}
	return out, nil
}

// fillEvicted puts back the values n holds as nil. A name the published node
// does not hold is dropped rather than published empty.
func fillEvicted(up registry.Pack, n *memNode) error {
	if n.evictedFrom == (skycipher.SHA256{}) {
		return nil
	}
	leaves, err := publishedLeaves(up, n.evictedFrom)
	if err != nil {
		return err
	}
	for name, v := range n.leaves {
		if v != nil {
			continue
		}
		if b, ok := leaves[name]; ok {
			n.leaves[name] = b
		} else {
			delete(n.leaves, name)
		}
	}
	n.evictedFrom = skycipher.SHA256{}
	return nil
}

// readEvicted returns the leaves of an evicted node for a read, without
// keeping them in memory.
func (p *Publisher) readEvicted(n *memNode) map[string][]byte {
	if n.evictedFrom == (skycipher.SHA256{}) {
		return nil
	}
	c := p.cxoNode.Container()
	up, err := c.Unpack(skycipher.SecKey(p.sk), Registry)
	if err != nil {
		p.log.WithError(err).Warn("treestore-pub: cannot read evicted leaves")
		return nil
	}
	defer up.Close() //nolint:errcheck
	leaves, err := publishedLeaves(up, n.evictedFrom)
	if err != nil {
		p.log.WithError(err).Warn("treestore-pub: cannot read evicted leaves")
		return nil
	}
	return leaves
}
