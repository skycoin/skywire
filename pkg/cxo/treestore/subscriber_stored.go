// Package treestore pkg/cxo/treestore/subscriber_stored.go c2-net-cxo
package treestore

import (
	"fmt"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// A store-backed subscriber (SubConfig.StoreBacked) keeps no copy of the
// feed. It remembers the last Root it applied, which the node keeps in its
// store (see Config.KeepRoots), diffs each new Root against it by hash, and
// reads Get and Walk from the store.

// storedBaseLoad starts from the newest Root of the feed already in the store,
// so a restart reports only what changed since.
func (s *Subscriber) storedBaseLoad() {
	c := s.cxoNode.Container()
	pk := skycipher.PubKey(s.feedPK)
	nonce := c.ActiveHead(pk)
	if nonce == 0 {
		return
	}
	r, err := c.LastRoot(pk, nonce)
	if err != nil || r == nil || len(r.Refs) == 0 {
		return
	}
	s.base = r
}

// rootHash is the hash of r's root TreeNode, zero for an empty Root.
func rootHash(r *registry.Root) skycipher.SHA256 {
	if r == nil || len(r.Refs) == 0 {
		return skycipher.SHA256{}
	}
	return r.Refs[0].Hash
}

// handleRootFilledStored is handleRootFilled for a store-backed subscriber.
func (s *Subscriber) handleRootFilledStored(r *registry.Root) {
	if r == nil || r.Pub != skycipher.PubKey(s.feedPK) {
		return
	}
	s.storedApplyMu.Lock()
	defer s.storedApplyMu.Unlock()
	pack, err := s.cxoNode.Container().Pack(r, Registry)
	if err != nil {
		s.log.WithError(err).Warn("treestore-sub: get pack failed")
		return
	}
	s.mu.RLock()
	base := s.base
	prefixes := s.prefixes
	cb := s.callback
	onChange := s.onChange
	s.mu.RUnlock()

	var changes []UpdateEvent
	collect := func(path string, value []byte) {
		if pathMatchesAny(path, prefixes) {
			changes = append(changes, UpdateEvent{Path: path, Value: value})
		}
	}
	// A Root older than the one applied carries nothing new, and diffing
	// back to it would report the newer leaves as deleted.
	older := base != nil && base.Nonce == r.Nonce && r.Seq < base.Seq
	if !older {
		err = diffStored(pack, rootHash(base), rootHash(r), "", collect)
	}
	if err != nil && base != nil {
		// The Root we applied last is gone from the store. Report the
		// whole new tree, as a fresh subscriber would.
		s.log.WithError(err).WithField("feed", s.feedPK.Hex()).
			Warn("treestore-sub: last applied Root is not readable; reporting the whole tree")
		changes = changes[:0]
		err = diffStored(pack, skycipher.SHA256{}, rootHash(r), "", collect)
	}
	if err != nil {
		s.log.WithError(err).WithField("feed", s.feedPK.Hex()).WithField("seq", r.Seq).
			Warn("treestore-sub: walk tree failed")
		s.setFillErr(fmt.Errorf("%w: Root %d: %w", ErrIncompleteRoot, r.Seq, err))
		return
	}

	s.mu.Lock()
	if !older {
		s.base = r
	}
	s.filled, s.fillErr = true, nil
	s.mu.Unlock()
	if cb != nil && len(changes) > 0 {
		cb(changes)
	}
	if onChange != nil && len(changes) > 0 {
		onChange()
	}
	s.lastUpdateNs.Store(time.Now().UnixNano())
	s.fillBreakStreak.Store(0)
	s.quietStreak.Store(0)
	s.lastFillBreakKickNs.Store(0)
	s.signalRootObserved()
	s.nudgeCleanup()
}

// storedBase returns the pack and root TreeNode of the last applied Root.
func (s *Subscriber) storedBase() (registry.Pack, skycipher.SHA256, bool) {
	s.mu.RLock()
	base := s.base
	s.mu.RUnlock()
	if base == nil || len(base.Refs) == 0 {
		return nil, skycipher.SHA256{}, false
	}
	pack, err := s.cxoNode.Container().Pack(base, Registry)
	if err != nil {
		s.log.WithError(err).Debug("treestore-sub: get pack failed")
		return nil, skycipher.SHA256{}, false
	}
	return pack, rootHash(base), true
}

// storedNodeAt follows segs from hash and returns the TreeNode hash it names.
func storedNodeAt(pack registry.Pack, hash skycipher.SHA256, segs []string) (skycipher.SHA256, bool) {
	for _, seg := range segs {
		entries, err := storedEntries(pack, hash)
		if err != nil {
			return skycipher.SHA256{}, false
		}
		found := false
		for _, e := range entries {
			if e.Name == seg && e.Sub.Hash != (skycipher.SHA256{}) {
				hash, found = e.Sub.Hash, true
				break
			}
		}
		if !found {
			return skycipher.SHA256{}, false
		}
	}
	return hash, true
}

func (s *Subscriber) getStored(path string) ([]byte, bool) {
	segs, err := SplitPath(path)
	if err != nil || len(segs) == 0 {
		return nil, false
	}
	pack, hash, ok := s.storedBase()
	if !ok {
		return nil, false
	}
	parent, ok := storedNodeAt(pack, hash, segs[:len(segs)-1])
	if !ok {
		return nil, false
	}
	entries, err := storedEntries(pack, parent)
	if err != nil {
		return nil, false
	}
	for _, e := range entries {
		if e.Name == segs[len(segs)-1] && len(e.Leaf) > 0 {
			return append([]byte(nil), e.Leaf...), true
		}
	}
	return nil, false
}

func (s *Subscriber) walkStoredPrefix(prefix string, fn func(path string, value []byte) bool) {
	segs, err := SplitPath(prefix)
	if err != nil {
		return
	}
	pack, hash, ok := s.storedBase()
	if !ok {
		return
	}
	if hash, ok = storedNodeAt(pack, hash, segs); !ok {
		return
	}
	s.mu.RLock()
	prefixes := s.prefixes
	s.mu.RUnlock()
	if _, err := walkStored(pack, hash, joinSegs(segs), func(path string, value []byte) bool {
		if !pathMatchesAny(path, prefixes) {
			return true
		}
		return fn(path, value)
	}); err != nil {
		s.log.WithError(err).Debug("treestore-sub: walk of the stored feed failed")
	}
}
