// Package cxoutils pkg/cxo/cxoutils/cxoutils.go c2-net-cxo
// CXO, that can be used, or can be not used by
// end-user. The package implements methods to
// remove old Root objects and to remove ownerless
// objects from databases.
//
// The CXO never remove objects, even if an objects
// is not used anymore. And every object has rc
// (references counter). If the rc is zero, then
// this object is ownerless and can be removed.
//
// And the same for Root objects. The CXO keeps all
// Root objects. But who interest old, replaced Root
// objects?
package cxoutils

import (
	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
)

// RemoveRootObjects removes old Root objects from given
// *skyobject.Container keeping last n-th. Call this method
// using some interval. The method affects all feeds of
// the Container and all heads.
//
// If a feed contains more then one head, then the method
// keeps last n-th Root objects of every head.
func RemoveRootObjects(c *skyobject.Container, keepLast int) error {

	for _, pk := range c.Feeds() {

		heads, err := c.Heads(pk)
		if err != nil {
			return err
		}

		for _, nonce := range heads {
			// Delete every stored Root older than the newest keepLast,
			// whatever gaps lie between them. This used to walk every
			// seq from the last one down to zero and DelRoot each —
			// gap-safe, but a feed republishing every 45 s gains ~1900
			// seqs a day, so on an aggregator holding thousands of
			// feeds each tick spent a third of TPD's CPU on DelRoot
			// calls that found nothing. Listing the stored seqs keeps
			// the gap safety (a hole never ends the sweep) at a cost
			// proportional to what is actually stored.
			seqs, err := c.RootSeqs(pk, nonce)
			if err != nil {
				continue // head gone since Heads — not a real error
			}
			if keepLast < 0 {
				keepLast = 0
			}
			if len(seqs) <= keepLast {
				continue
			}
			for _, seq := range seqs[:len(seqs)-keepLast] {
				if err := c.DelRoot(pk, nonce, seq); err != nil && err != data.ErrNotFound {
					return err // a real failure (CXDS not found error?)
				}
			}

		} // head loop

	} // feed loop

	return nil
}

// RemoveObjects deletes every CXDS object whose reference count has
// dropped to zero and that the cache is not currently tracking (so
// we don't race a Cache.Set "resurrecting" a key by rebumping its
// rc).
//
// Lock ordering: Cache.Set takes Cache.mu, then opens a bbolt RWTx
// via cxds.Set. Earlier versions of this function called IsCached
// from inside the IterateDel callback (i.e. inside the cleanup
// RWTx), which inverted that order: the cleanup goroutine held
// bbolt.rwlock while waiting on Cache.mu, and any concurrent
// Cache.Set held Cache.mu while waiting on bbolt.rwlock. Classic
// A/B deadlock observed in production after ~12 minutes of run.
//
// Fix: snapshot the cached-keys set ONCE up front (one Cache.mu
// acquire, no bbolt involvement), then drive IterateDel with a
// callback that only does a map lookup. The snapshot can race with
// concurrent Cache.Set but the race always errs on the safe side:
// a key that races into the cache after the snapshot stays on disk
// for one extra sweep, never gets prematurely deleted.
func RemoveObjects(c *skyobject.Container) (err error) {

	cached := c.CachedKeys()
	var db = c.DB().CXDS()

	err = db.IterateDel(
		func(key cipher.SHA256, rc uint32, _ []byte) (bool, error) {
			if rc != 0 {
				return false, nil
			}
			// Skipping cached keys is BY DESIGN: an object the in-memory Cache
			// still holds (a wanted item from an in-flight fill) must not be
			// deleted from under the fill. The residual — a rc==0 object kept
			// alive only by a hung fill's want entry — is bounded not here but
			// by MaxFillingTime (see head.go / #3562): when the fill times out
			// it Unwants, the object leaves the cache, and the next sweep
			// reclaims it. Do not "fix" this by deleting cached keys.
			_, isCached := cached[key]
			return !isCached, nil
		})

	return
}

// RemoveObjects could support "down to" threshold or timeout-based cleanup.
