// Package treestore pkg/cxo/treestore/fold.go c2-net-cxo
package treestore

import (
	"cmp"
	"fmt"
	"slices"
	"sync/atomic"

	skycipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"

	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// A publisher with MemoryEntries keeps a published sub-tree in memory only
// while it is in use. foldToBudget turns the least recently touched ones into
// a bare hash (memNode.folded), and expand reads one level back from the store
// when a Put, Get or Delete needs it. Walk reads folded sub-trees from the
// store without keeping them.

// treeTouchSeq stamps memNode.touched, so foldToBudget can fold the oldest.
var treeTouchSeq atomic.Uint64

// foldedNode is a sub-tree that lives in the store as the TreeNode hash.
func foldedNode(hash skycipher.SHA256) *memNode {
	return &memNode{folded: true, cached: true, pubHash: hash}
}

// storeReader opens the publisher's pack on first use, so a path that never
// meets a folded node costs nothing.
type storeReader struct {
	p  *Publisher
	up *skyobject.Unpack
}

func (r *storeReader) pack() (registry.Pack, error) {
	if r.up != nil {
		return r.up, nil
	}
	up, err := r.p.cxoNode.Container().Unpack(skycipher.SecKey(r.p.sk), Registry)
	if err != nil {
		return nil, err
	}
	r.up = up
	return up, nil
}

func (r *storeReader) close() {
	if r.up != nil {
		_ = r.up.Close() //nolint:errcheck
	}
}

// storedEntries reads the entries of the stored TreeNode hash. The zero hash
// is the empty node.
func storedEntries(pack registry.Pack, hash skycipher.SHA256) ([]TreeEntry, error) {
	if hash == (skycipher.SHA256{}) {
		return nil, nil
	}
	var node TreeNode
	ref := registry.Ref{Hash: hash}
	if err := ref.Value(pack, &node); err != nil {
		return nil, fmt.Errorf("treestore: load node %s: %w", hash.Hex()[:16], err)
	}
	var out []TreeEntry
	err := node.Children.Ascend(pack, func(_ int, h skycipher.SHA256) error {
		val, err := pack.Get(h)
		if err != nil {
			return err
		}
		var entry TreeEntry
		if _, err := encoder.DeserializeRawNoCopy(val, &entry); err != nil {
			return err
		}
		out = append(out, entry)
		return nil
	})
	return out, err
}

// expand reads a folded node's own level back from the store. Its leaves stay
// in the store (evictedFrom) and its sub-trees come back folded. A node whose
// objects are gone comes back empty, so the rest of the tree stays usable.
func (p *Publisher) expand(rd *storeReader, n *memNode) {
	if !n.folded {
		return
	}
	n.folded = false
	n.leaves = make(map[string][]byte)
	n.subs = make(map[string]*memNode)
	n.touched = treeTouchSeq.Add(1)
	pack, err := rd.pack()
	var entries []TreeEntry
	if err == nil {
		entries, err = storedEntries(pack, n.pubHash)
	}
	if err != nil {
		p.log.WithError(err).Warn("treestore-pub: folded sub-tree is not in the store; dropping it")
		n.cached = false
		n.pubHash = skycipher.SHA256{}
		n.mutSeq = treeMutSeq.Add(1)
		return
	}
	for _, e := range entries {
		switch {
		case len(e.Leaf) > 0:
			n.leaves[e.Name] = nil
		case e.Sub.Hash != (skycipher.SHA256{}):
			n.subs[e.Name] = foldedNode(e.Sub.Hash)
		}
	}
	if len(n.leaves) > 0 {
		n.evictedFrom = n.pubHash
	}
}

// expandPath expands every folded node on the way to segs and stamps them
// touched. The last segment is a leaf name for Put/Get/Delete and is expanded
// too when it names a sub-tree, as PrunePrefix and Walk need.
func (p *Publisher) expandPath(segs []string) {
	if p.memoryEntries <= 0 {
		return
	}
	rd := &storeReader{p: p}
	defer rd.close()
	stamp := treeTouchSeq.Add(1)
	cur := p.root
	p.expand(rd, cur)
	cur.touched = stamp
	for _, seg := range segs {
		next, ok := cur.subs[seg]
		if !ok {
			return
		}
		p.expand(rd, next)
		next.touched = stamp
		cur = next
	}
}

// fold drops n's expanded content and returns how many entries that freed.
func fold(n *memNode) int {
	if n.folded {
		return 0
	}
	freed := len(n.leaves) + len(n.subs)
	for _, sub := range n.subs {
		freed += fold(sub)
	}
	n.leaves, n.subs = nil, nil
	n.evictedFrom = skycipher.SHA256{}
	n.folded = true
	return freed
}

// foldToBudget folds the least recently touched published sub-trees until the
// expanded tree holds at most memoryEntries entries. The root never folds.
// Callers hold p.mu.
func (p *Publisher) foldToBudget() {
	if p.memoryEntries <= 0 {
		return
	}
	type cand struct {
		n     *memNode
		depth int
	}
	var (
		total int
		cands []cand
	)
	var visit func(n *memNode, depth int)
	visit = func(n *memNode, depth int) {
		total += len(n.leaves) + len(n.subs)
		if depth > 0 && n.cached {
			cands = append(cands, cand{n, depth})
		}
		for _, sub := range n.subs {
			if !sub.folded {
				visit(sub, depth+1)
			}
		}
	}
	visit(p.root, 0)
	if total <= p.memoryEntries {
		return
	}
	// Oldest first. A path is stamped as one, so on a tie the deeper node
	// goes first and a parent never folds away a child that could stay.
	slices.SortFunc(cands, func(a, b cand) int {
		if c := cmp.Compare(a.n.touched, b.n.touched); c != 0 {
			return c
		}
		return cmp.Compare(b.depth, a.depth)
	})
	for _, c := range cands {
		if total <= p.memoryEntries {
			return
		}
		total -= fold(c.n)
	}
}

// walkStored visits every leaf of the stored TreeNode hash in name order.
// It returns false when fn stopped the walk.
func walkStored(pack registry.Pack, hash skycipher.SHA256, path string, fn func(path string, value []byte) bool) (bool, error) {
	entries, err := storedEntries(pack, hash)
	if err != nil {
		return true, err
	}
	slices.SortFunc(entries, func(a, b TreeEntry) int { return cmp.Compare(a.Name, b.Name) })
	for _, e := range entries {
		full := joinPath(path, e.Name)
		if len(e.Leaf) > 0 {
			if !fn(full, append([]byte(nil), e.Leaf...)) {
				return false, nil
			}
			continue
		}
		if e.Sub.Hash == (skycipher.SHA256{}) {
			continue
		}
		more, err := walkStored(pack, e.Sub.Hash, full, fn)
		if err != nil || !more {
			return more, err
		}
	}
	return true, nil
}

// diffStored reports each leaf that differs between the stored TreeNodes
// oldHash and newHash: added or changed with its new value, removed with nil.
// Sub-trees with the same hash are skipped, so the cost follows the change.
func diffStored(pack registry.Pack, oldHash, newHash skycipher.SHA256, path string, fn func(path string, value []byte)) error {
	if oldHash == newHash {
		return nil
	}
	oldEntries, err := storedEntries(pack, oldHash)
	if err != nil {
		return err
	}
	newEntries, err := storedEntries(pack, newHash)
	if err != nil {
		return err
	}
	old := make(map[string]TreeEntry, len(oldEntries))
	for _, e := range oldEntries {
		old[e.Name] = e
	}
	all := func(hash skycipher.SHA256, full string, removed bool) error {
		_, err := walkStored(pack, hash, full, func(p string, v []byte) bool {
			if removed {
				v = nil
			}
			fn(p, v)
			return true
		})
		return err
	}
	for _, e := range newEntries {
		full := joinPath(path, e.Name)
		o, had := old[e.Name]
		delete(old, e.Name)
		newLeaf := len(e.Leaf) > 0
		oldLeaf := had && len(o.Leaf) > 0
		switch {
		case newLeaf && oldLeaf:
			if !bytesEqual(o.Leaf, e.Leaf) {
				fn(full, append([]byte(nil), e.Leaf...))
			}
		case newLeaf:
			if had {
				if err := all(o.Sub.Hash, full, true); err != nil {
					return err
				}
			}
			fn(full, append([]byte(nil), e.Leaf...))
		case oldLeaf:
			fn(full, nil)
			if err := all(e.Sub.Hash, full, false); err != nil {
				return err
			}
		default:
			if err := diffStored(pack, o.Sub.Hash, e.Sub.Hash, full, fn); err != nil {
				return err
			}
		}
	}
	for name, o := range old {
		full := joinPath(path, name)
		if len(o.Leaf) > 0 {
			fn(full, nil)
			continue
		}
		if err := all(o.Sub.Hash, full, true); err != nil {
			return err
		}
	}
	return nil
}

// hydrateFolded opens the published root TreeNode with everything below its
// own level folded, so a restart reads nothing more than that from the store.
func (p *Publisher) hydrateFolded(r *registry.Root) {
	p.hydratedRoot = r.Refs[0].Hash
	root := foldedNode(r.Refs[0].Hash)
	rd := &storeReader{p: p}
	defer rd.close()
	p.expand(rd, root)
	p.root = root
}

// recoverFolded takes what a newer Root of this feed holds that ours does
// not. Unlike mergeMemNode it adds and updates leaves but drops none.
func (p *Publisher) recoverFolded(r *registry.Root) {
	ours := p.hydratedRoot
	if last := p.lastRoot.Load(); last != nil && len(last.Refs) > 0 {
		ours = last.Refs[0].Hash
	}
	rd := &storeReader{p: p}
	defer rd.close()
	pack, err := rd.pack()
	if err != nil {
		p.log.WithError(err).Warn("treestore-pub: cannot read a newer Root of this feed")
		return
	}
	var recovered []UpdateEvent
	err = diffStored(pack, ours, r.Refs[0].Hash, "", func(path string, value []byte) {
		if value != nil {
			recovered = append(recovered, UpdateEvent{Path: path, Value: value})
		}
	})
	if err != nil {
		p.log.WithError(err).Warn("treestore-pub: cannot read a newer Root of this feed")
		return
	}
	p.mu.Lock()
	kept := recovered[:0]
	for _, ev := range recovered {
		segs, err := SplitPath(ev.Path)
		if err != nil || len(segs) == 0 {
			continue
		}
		p.expandPath(segs)
		if putAt(p.root, segs, ev.Value) == nil {
			kept = append(kept, ev)
		}
	}
	if len(kept) > 0 {
		p.markDirty()
	}
	cb := p.onRecovered
	p.mu.Unlock()
	p.log.WithField("seq", r.Seq).WithField("leaves", len(kept)).
		Info("treestore-pub: took a newer Root of this feed from a peer")
	if cb != nil && len(kept) > 0 {
		cb(kept)
	}
}

// walkFolded walks a folded sub-tree from the store for Walk. Callers hold p.mu.
func (p *Publisher) walkFolded(n *memNode, path string, fn func(string, []byte) bool) bool {
	rd := &storeReader{p: p}
	defer rd.close()
	pack, err := rd.pack()
	if err != nil {
		p.log.WithError(err).Warn("treestore-pub: cannot read a folded sub-tree")
		return true
	}
	more, err := walkStored(pack, n.pubHash, path, fn)
	if err != nil {
		p.log.WithError(err).Warn("treestore-pub: cannot read a folded sub-tree")
	}
	return more
}
