package tests

import (
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

func feedsHas(t *testing.T, feeds data.Feeds, pk cipher.PubKey, want bool) {
	if ok, err := feeds.Has(pk); err != nil {
		t.Error(err)
	} else if ok != want {
		if want == true { //nolint:staticcheck
			t.Error("missing feed")
		} else {
			t.Error("has feed (but should not)")
		}
	}
}

// FeedsAdd is test case for Feeds.Add
func FeedsAdd(t *testing.T, idx data.IdxDB) {

	var pk, _ = cipher.GenerateKeyPair()

	t.Run("add", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			if err = feeds.Add(pk); err != nil {
				return
			}
			feedsHas(t, feeds, pk, true)
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	if t.Failed() == true { //nolint:staticcheck
		return
	}

	t.Run("twice", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			if err = feeds.Add(pk); err != nil {
				return
			}
			feedsHas(t, feeds, pk, true)
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

}

// FeedsDel is test case for Feeds.Del
func FeedsDel(t *testing.T, idx data.IdxDB) {

	const nonce = 1 //nolint:unused

	var pk, _ = cipher.GenerateKeyPair()

	// not found
	t.Run("not found", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (_ error) { //nolint:errcheck,gosec
			if err := feeds.Del(pk); err == nil {
				t.Error("missing error")
			} else if err != data.ErrNoSuchFeed {
				t.Error("wrong error:", err)
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	if addFeed(t, idx, pk); t.Failed() {
		return
	}

	// delete
	t.Run("delete", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			if err = feeds.Del(pk); err != nil {
				return
			}
			feedsHas(t, feeds, pk, false)
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

}

// FeedsIterate is test case for Feeds.Iterate
func FeedsIterate(t *testing.T, idx data.IdxDB) {

	t.Run("no feeds", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var called int
			err = feeds.Iterate(func(pk cipher.PubKey) (err error) {
				called++
				return
			})
			if err != nil {
				return
			}
			if called != 0 {
				t.Error("called", called)
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	var (
		pk1, _ = cipher.GenerateKeyPair()
		pk2, _ = cipher.GenerateKeyPair()

		pks = make(map[cipher.PubKey]struct{})
	)

	for _, pk := range []cipher.PubKey{pk1, pk2} {
		if addFeed(t, idx, pk); t.Failed() {
			return
		}
		pks[pk] = struct{}{}
	}

	t.Run("iterate", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var called int
			err = feeds.Iterate(func(pk cipher.PubKey) (err error) {
				if _, ok := pks[pk]; !ok {
					t.Error("wrong pk given", pk.Hex())
				} else {
					delete(pks, pk)
				}
				called++
				return
			})
			if err != nil {
				return
			}
			if called != 2 {
				t.Error("called wrong times", called)
			}
			if len(pks) != 0 {
				t.Error("existing feeds was not iterated")
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	t.Run("stop iteration", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var called int
			err = feeds.Iterate(func(pk cipher.PubKey) (err error) {
				called++
				return data.ErrStopIteration
			})
			if err != nil {
				return
			}
			if called != 1 {
				t.Error("ErrStopIteration doesn't stop the iteration")
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	// mutating during iteration

	var pk3, _ = cipher.GenerateKeyPair()

	t.Run("mutate add", func(t *testing.T) {
		var called int
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			err = feeds.Iterate(func(pk cipher.PubKey) (err error) {
				called++
				if called == 1 {
					return feeds.Add(pk3)
				}
				return
			})
			if err != nil {
				return
			}
			if false == (called == 2 || called == 3) { //nolint:staticcheck
				t.Error("wrong times called", called)
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	t.Run("mutate delete", func(t *testing.T) {
		var called int
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			err = feeds.Iterate(func(pk cipher.PubKey) (err error) {
				called++
				if called == 1 {
					return feeds.Del(pk3)
				}
				return
			})
			if err != nil {
				return
			}
			if false == (called == 2 || called == 3) { //nolint:staticcheck
				t.Error("wrong times called", called)
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

}

// FeedsHas is test case for Feeds.Has
func FeedsHas(t *testing.T, idx data.IdxDB) {

	var pk, _ = cipher.GenerateKeyPair()

	t.Run("not exist", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (_ error) { //nolint:errcheck,gosec
			feedsHas(t, feeds, pk, false)
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	if addFeed(t, idx, pk); t.Failed() {
		return
	}

	t.Run("exists", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (_ error) { //nolint:errcheck,gosec
			feedsHas(t, feeds, pk, true)
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

}

func addHead(t *testing.T, idx data.IdxDB, pk cipher.PubKey, nonce uint64) {
	err := idx.Tx(func(fs data.Feeds) (err error) { //nolint:errcheck,gosec
		var hs data.Heads
		if hs, err = fs.Heads(pk); err != nil {
			return
		}
		var rs data.Roots
		if rs, err = hs.Add(nonce); err != nil {
			return
		} else if rs == nil {
			t.Error("got nil-Roots")
		}
		return
	})
	if err != nil {
		t.Error(err)
	}
}

// FeedsHeads is test case for Feeds.Heads
func FeedsHeads(t *testing.T, idx data.IdxDB) {

	var pk, _ = cipher.GenerateKeyPair()

	t.Run("no such feed", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err == nil {
				t.Error("missng data.ErrNoSuchFeed")
			} else if err != data.ErrNoSuchFeed {
				return // bubble the err up
			} else if hs != nil {
				t.Error("NoSuchFeed but Heads are not nil")
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	})

	if addFeed(t, idx, pk); t.Failed() {
		return
	}

	t.Run("empty", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return // bubble the err up
			}
			if hs == nil {
				t.Error("got nil")
			}
			return // ok mutherfucekr
		})
		if err != nil {
			t.Error(err)
		}
	})

	if addHead(t, idx, pk, 1); t.Failed() {
		return
	}

	t.Run("heads", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return // bubble the err up
			}
			if hs == nil {
				t.Error("got nil")
			}
			return // ok mutherfucekr
		})
		if err != nil {
			t.Error(err)
		}
	})

}
