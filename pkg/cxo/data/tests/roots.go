package tests

import (
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

func addFeed(t *testing.T, idx data.IdxDB, pk cipher.PubKey) {
	err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
		return feeds.Add(pk)
	})
	if err != nil {
		t.Error(err)
	}
}

func newRoot(seed string, sk cipher.SecKey) (r *data.Root) {
	r = new(data.Root)
	r.Create = 111
	r.Access = 222
	r.Time = 789

	r.Seq = 0
	r.Prev = cipher.SHA256{}
	r.Hash = cipher.SumSHA256([]byte(seed))
	r.Sig, _ = cipher.SignHash(r.Hash, sk) //nolint:errcheck,gosec
	return
}

func addRoot(
	t *testing.T,
	idx data.IdxDB,
	pk cipher.PubKey,
	nonce uint64,
	r *data.Root,
) {
	err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
		var hs data.Heads
		if hs, err = feeds.Heads(pk); err != nil {
			return
		}
		var rs data.Roots
		if rs, err = hs.Add(nonce); err != nil {
			return
		}
		return rs.Set(r)
	})
	if err != nil {
		t.Error(err)
	}
}

// RootsAscend is test case for Roots.Ascend
func RootsAscend(t *testing.T, idx data.IdxDB) {

	const nonce = 1

	var pk, sk = cipher.GenerateKeyPair()

	if addFeed(t, idx, pk); t.Failed() {
		return
	}

	t.Run("empty feed", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var called int
			err = rs.Ascend(func(r *data.Root) (err error) {
				called++
				return
			})
			if err != nil {
				return
			}
			if called != 0 {
				t.Error("called on empty feed:", called)
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	var (
		r1 = newRoot("r", sk)
		r2 = newRoot("e", sk)
	)

	r2.Seq, r2.Prev = 1, cipher.SumSHA256([]byte("random"))

	var ra = []*data.Root{r1, r2}

	for _, r := range ra {
		if addRoot(t, idx, pk, nonce, r); t.Failed() {
			return
		}
	}

	t.Run("ascend", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var called int
			err = rs.Ascend(func(r *data.Root) (err error) {
				if called > len(ra)-1 {
					t.Error("called too many times", called)
					return data.ErrStopIteration
				}
				x := ra[called]
				if r.Hash != x.Hash {
					t.Error("got wrong Root")
				}
				called++
				return
			})
			if err != nil {
				return
			}
			if called != 2 {
				t.Error("called too few times", called)
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	t.Run("stop iteration", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var called int
			err = rs.Ascend(func(r *data.Root) error {
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

	var r3 = newRoot("w", sk)

	r3.Seq, r3.Prev = 2, cipher.SumSHA256([]byte("random"))

	t.Run("mutate add", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var called int
			err = rs.Ascend(func(r *data.Root) (err error) {
				if called == 0 {
					if err = rs.Set(r3); err != nil {
						return
					}
				}
				called++
				return
			})
			if err != nil {
				return
			}
			if called != 3 {
				t.Error("wrong times called")
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	t.Run("mutate del", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var called int
			err = rs.Ascend(func(r *data.Root) (err error) {
				if called == 0 {
					if err = rs.Del(r3.Seq); err != nil {
						return
					}
				}
				called++
				return
			})
			if err != nil {
				return
			}
			if called != 2 {
				t.Error("wrong times called")
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

}

// RootsDescend is test case for Roots.Descend
func RootsDescend(t *testing.T, idx data.IdxDB) {

	var (
		pk, sk        = cipher.GenerateKeyPair()
		nonce  uint64 = 1
	)

	if addFeed(t, idx, pk); t.Failed() {
		return
	}

	t.Run("empty feed", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var called int
			err = rs.Descend(func(r *data.Root) (err error) {
				called++
				return
			})
			if err != nil {
				return
			}
			if called != 0 {
				t.Error("called on empty feed:", called)
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	var (
		r3 = newRoot("r", sk)
		r2 = newRoot("e", sk)
	)

	r3.Seq, r3.Prev = 2, cipher.SumSHA256([]byte("random"))
	r2.Seq, r2.Prev = 1, cipher.SumSHA256([]byte("random"))

	var ra = []*data.Root{r3, r2}

	for _, r := range ra {
		if addRoot(t, idx, pk, nonce, r); t.Failed() {
			return
		}
	}

	t.Run("descend", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var called int
			err = rs.Descend(func(r *data.Root) (err error) {
				if called > len(ra)-1 {
					t.Error("called too many times", called)
					return data.ErrStopIteration
				}
				x := ra[called]
				if r.Hash != x.Hash {
					t.Error("got wrong Root")
				}
				called++
				return
			})
			if err != nil {
				return
			}
			if called != 2 {
				t.Error("called too few times", called)
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	t.Run("stop iteration", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var called int
			err = rs.Descend(func(r *data.Root) error {
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

	r1 := newRoot("w", sk)

	t.Run("mutate add", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var called int
			err = rs.Descend(func(r *data.Root) (err error) {
				if called == 0 {
					if err = rs.Set(r1); err != nil {
						return
					}
				}
				called++
				return
			})
			if err != nil {
				return
			}
			if called != 3 {
				t.Error("wrong times called")
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	t.Run("mutate del", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var called int
			err = rs.Descend(func(r *data.Root) (err error) {
				if called == 0 {
					if err = rs.Del(r1.Seq); err != nil {
						return
					}
				}
				called++
				return
			})
			if err != nil {
				return
			}
			if called != 2 {
				t.Error("wrong times called")
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

}

// RootsSet is test case for Roots.Set
func RootsSet(t *testing.T, idx data.IdxDB) {

	const nonce = 1

	var pk, sk = cipher.GenerateKeyPair()

	if addFeed(t, idx, pk); t.Failed() {
		return
	}

	r := newRoot("r", sk)

	t.Run("create", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			if err = rs.Set(r); err != nil {
				return
			}
			if r.Create == 111 {
				t.Error("CreateTime not updated")
			}
			if r.Access != r.Create {
				t.Error("access time not set")
			}
			var x *data.Root
			if x, err = rs.Get(r.Seq); err != nil {
				return
			}
			if *x != *r {
				t.Error("wrong")
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	t.Run("update", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			if err = rs.Set(r); err != nil {
				return
			}
			var x *data.Root
			if x, err = rs.Get(r.Seq); err != nil {
				return
			}
			r.Access = x.Access
			if *x != *r {
				t.Error("wrong")
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

}

// RootsDel is test case for Roots.Del
func RootsDel(t *testing.T, idx data.IdxDB) {

	/*	pk, sk := cipher.GenerateKeyPair()

		if addFeed(t, idx, pk); t.Failed() {
			return
		}*/

}

// RootsGet is test case for Roots.Get
func RootsGet(t *testing.T, idx data.IdxDB) {

	const nonce = 1

	var pk, _ = cipher.GenerateKeyPair()

	if addFeed(t, idx, pk); t.Failed() {
		return
	}

	t.Run("not found", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			_, err = rs.Get(0)
			return
		})
		if err == nil {
			t.Error("missing error")
		} else if err != data.ErrNotFound {
			t.Error("unexpected error:", err)
		}
	})

}

// RootsHas is test case for Roots.Has
func RootsHas(t *testing.T, idx data.IdxDB) {

	const nonce = 1

	var pk, sk = cipher.GenerateKeyPair()

	if addFeed(t, idx, pk); t.Failed() {
		return
	}

	t.Run("not found", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var ok bool
			if ok, err = rs.Has(0); err != nil {
				t.Error(err)
			} else if ok == true { //nolint:staticcheck
				t.Error("has unexisting root")
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

	addRoot(t, idx, pk, nonce, newRoot("seed", sk))

	t.Run("has", func(t *testing.T) {
		err := idx.Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return
			}
			var rs data.Roots
			if rs, err = hs.Add(nonce); err != nil {
				return
			}
			var ok bool
			if ok, err = rs.Has(0); err != nil {
				t.Error(err)
			} else if ok == false { //nolint:staticcheck
				t.Error("missing root")
			}
			return
		})
		if err != nil {
			t.Error(err)
		}
	})

}
