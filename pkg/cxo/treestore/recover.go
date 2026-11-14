// Package treestore pkg/cxo/treestore/recover.go c2-net-cxo
package treestore

import (
	"fmt"
	"slices"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// watchOwnFeed recovers what this publisher lost to a restore. A peer that
// holds a newer Root of this feed serves it, and only then does one get filled.
func (p *Publisher) watchOwnFeed() {
	var prev func(*node.Node, *registry.Root)
	prev = p.cxoNode.SwapOnRootFilled(func(n *node.Node, r *registry.Root) {
		p.recoverRoot(r)
		if prev != nil {
			prev(n, r)
		}
	})
}

// OnRecovered sets the callback for leaves a newer Root of this feed brought
// back. It runs on the CXO filler goroutine, once per recovered Root.
func (p *Publisher) OnRecovered(cb UpdateCallback) {
	p.mu.Lock()
	p.onRecovered = cb
	p.mu.Unlock()
}

func (p *Publisher) recoverRoot(r *registry.Root) {
	if r == nil || len(r.Refs) == 0 || r.Pub != skycipher.PubKey(p.pk) {
		return
	}
	select {
	case <-p.done:
		return
	default:
	}
	incoming, err := p.treeOf(r)
	if err != nil {
		p.log.WithError(err).Warn("treestore-pub: cannot read a newer Root of this feed")
		return
	}
	var recovered []UpdateEvent
	p.mu.Lock()
	mergeMemNode(p.root, incoming, "", treeMutSeq.Add(1), func(path string, value []byte) {
		recovered = append(recovered, UpdateEvent{Path: path, Value: value})
	})
	p.markDirty()
	cb := p.onRecovered
	p.mu.Unlock()
	p.log.WithField("seq", r.Seq).WithField("leaves", len(recovered)).
		Info("treestore-pub: took a newer Root of this feed from a peer")
	if cb != nil && len(recovered) > 0 {
		slices.SortFunc(recovered, func(a, b UpdateEvent) int {
			if a.Path < b.Path {
				return -1
			}
			return 1
		})
		cb(recovered)
	}
}

// treeOf decodes the tree of a Root held by this publisher's node.
func (p *Publisher) treeOf(r *registry.Root) (*memNode, error) {
	c := p.cxoNode.Container()
	up, err := c.Unpack(skycipher.SecKey(p.sk), Registry)
	if err != nil {
		return nil, fmt.Errorf("Unpack: %w", err)
	}
	defer up.Close() //nolint:errcheck

	var rootNode TreeNode
	if err := r.Refs[0].Value(up, &rootNode); err != nil {
		return nil, fmt.Errorf("decode root TreeNode: %w", err)
	}
	tree := newMemNode()
	var dropped []string
	if err := hydrateMemNode(up, &rootNode, tree, &dropped, "", p.dropLeaves); err != nil {
		return nil, fmt.Errorf("walk: %w", err)
	}
	// A dangling sub-object ref drops only its subtree, so the rest stays
	// usable. Name it, so the object that trapped is identifiable.
	if len(dropped) > 0 {
		p.log.WithField("dropped", dropped).
			Warn("treestore-pub: hydrate skipped dangling sub-object refs (filling-trap culprits); kept the intact remainder")
	}
	return tree, nil
}

// mergeMemNode folds src, a newer tree of this feed, into dst and reports each
// leaf dst did not have. A node untouched since hydration takes src exactly, so
// src's deletes hold. A node changed here keeps its leaves and gains src's rest.
func mergeMemNode(dst, src *memNode, path string, gen uint64, added func(path string, value []byte)) {
	changedHere := dst.mutSeq != 0
	dst.cached = false
	dst.mutSeq = gen
	for name, value := range src.leaves {
		_, had := dst.leaves[name]
		if had && changedHere {
			continue
		}
		if !had {
			added(joinPath(path, name), value)
		}
		dst.leaves[name] = value
	}
	for name, sub := range src.subs {
		child, ok := dst.subs[name]
		if !ok {
			child = newMemNode()
			dst.subs[name] = child
		}
		mergeMemNode(child, sub, joinPath(path, name), gen, added)
	}
	if changedHere {
		return
	}
	for name := range dst.leaves {
		if _, ok := src.leaves[name]; !ok {
			delete(dst.leaves, name)
		}
	}
	for name := range dst.subs {
		if _, ok := src.subs[name]; !ok {
			delete(dst.subs, name)
		}
	}
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "/" + name
}
