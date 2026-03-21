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

// RemoveObjects with rc == 0 from CXDS
func RemoveObjects(c *skyobject.Container) (err error) {

	var db = c.DB().CXDS()

	err = db.IterateDel(
		func(key cipher.SHA256, rc uint32, _ []byte) (bool, error) {
			return (rc == 0) && (c.IsCached(key) == false), nil //nolint:staticcheck
		})

	return
}

// TODO (kostyarin): RemoveObjects with "down to" or "timeout" feature
