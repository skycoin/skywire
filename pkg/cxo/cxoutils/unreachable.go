package cxoutils

import (
	"errors"
	"fmt"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
)

// unreachableBatch is how many objects one delete transaction removes, so a
// store with millions of orphans is never cleared in a single transaction.
var unreachableBatch = 100000

// RemoveUnreachable deletes every object that no stored Root reaches,
// whatever its reference count. Reference counts can stay above zero for
// objects nothing references, and RemoveObjects then never frees them. It
// must run while nothing publishes or fills, and it removes nothing when a
// stored Root cannot be walked in full.
func RemoveUnreachable(c *skyobject.Container) (removed, volume int, err error) {
	reach := map[cipher.SHA256]struct{}{}
	for _, pk := range c.Feeds() {
		heads, herr := c.Heads(pk)
		if herr != nil {
			continue
		}
		for _, nonce := range heads {
			for _, h := range storedRoots(c, pk, nonce) {
				r, rerr := c.RootByHash(h)
				if errors.Is(rerr, data.ErrNotFound) {
					continue // a Root without its object reaches nothing
				}
				if rerr != nil {
					return 0, 0, fmt.Errorf("root %s of %s: %w", h.Hex()[:16], pk.Hex(), rerr)
				}
				werr := c.Walk(r, func(h cipher.SHA256, _ int) (bool, error) {
					reach[h] = struct{}{}
					return true, nil
				})
				if werr != nil {
					return 0, 0, fmt.Errorf("walk root %s of %s: %w", h.Hex()[:16], pk.Hex(), werr)
				}
			}
		}
	}

	db := c.DB().CXDS()
	for {
		var batch []cipher.SHA256
		ierr := db.Iterate(func(k cipher.SHA256, _ uint32, v []byte) error {
			if _, ok := reach[k]; ok {
				return nil
			}
			batch = append(batch, k)
			volume += len(v)
			if len(batch) == unreachableBatch {
				return data.ErrStopIteration
			}
			return nil
		})
		if ierr != nil && ierr != data.ErrStopIteration {
			return removed, volume, ierr
		}
		if len(batch) == 0 {
			return removed, volume, nil
		}
		if err := db.RunBatch(func(s data.CXDS) error {
			for _, k := range batch {
				if err := s.Del(k); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return removed, volume, err
		}
		removed += len(batch)
	}
}

func storedRoots(c *skyobject.Container, pk cipher.PubKey, nonce uint64) (out []cipher.SHA256) {
	_ = c.DB().IdxDB().Tx(func(fs data.Feeds) error { //nolint:errcheck
		hs, err := fs.Heads(pk)
		if err != nil {
			return err
		}
		rs, err := hs.Roots(nonce)
		if err != nil {
			return err
		}
		return rs.Ascend(func(r *data.Root) error {
			out = append(out, r.Hash)
			return nil
		})
	})
	return out
}
