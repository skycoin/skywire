// Package cxoutils implements common utilities for
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
func RemoveRootObjects(c *skyobject.Container, keepLast int) (err error) {

	for _, pk := range c.Feeds() {

		var heads []uint64
		if heads, err = c.Heads(pk); err != nil {
			return err
		}

	HeadLoop:
		for _, nonce := range heads {
			var seq uint64
			if seq, err = c.LastRootSeq(pk, nonce); err != nil {
				err = nil // clear
				continue  // not a real error
			}

			if seq < uint64(keepLast) { //nolint:gosec
				continue
			}

			var goDown = seq - uint64(keepLast) //nolint:gosec // positive

			for ; goDown > 0; goDown-- {

				if err = c.DelRoot(pk, nonce, goDown); err != nil {
					if err == data.ErrNotFound {
						err = nil // clear error
						continue HeadLoop
					}
					return err // a failure (CXDS not found error?)
				}

			}

			// seq = 0 (goDown == 0)
			if err = c.DelRoot(pk, nonce, 0); err != nil {
				if err == data.ErrNotFound {
					err = nil // clear error
					continue HeadLoop
				}

				return err
			}

			// continue HeadLoop

		} // head loop

	} // feed loop

	return err
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
			_, isCached := cached[key]
			return !isCached, nil
		})

	return
}

// RemoveObjects could support "down to" threshold or timeout-based cleanup.
