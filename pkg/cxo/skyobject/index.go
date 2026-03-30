package skyobject

import (
	"errors"
	"sync"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// keep latest and tracked Root objects only
type indexHeads struct {
	h       map[uint64]*data.Root // last Root
	activen uint64                // head with latest root (nonce)
	activet int64                 // timestamp of the last root
}

// heads
func newIndexHeads() (hs *indexHeads) {
	hs = new(indexHeads)
	hs.h = make(map[uint64]*data.Root)
	return
}

// under lock
func (i *indexHeads) setActive() {

	// reset first
	i.activet = 0
	i.activen = 0

	for nonce, dr := range i.h {

		if i.activet <= dr.Time {
			i.activet = dr.Time
			i.activen = nonce
		}

	}

}

// Index is internal and used by Container.
// The Index can't be creaed and used outside.
// The Index keeps information about last Root
// objects for fast access
type Index struct {
	mx sync.Mutex

	c *Container // back reference (for db.IdxDB and for the Cache)

	loadTime int64 // load time

	feeds  map[cipher.PubKey]*indexHeads
	feedsl []cipher.PubKey // change on write

	stat   *indexStat
	closeo sync.Once //nolint:unused // close once
}

func (i *Index) load(c *Container) (err error) {

	// create statistic
	i.stat = newIndexStat(c.conf.RollAvgSamples)

	i.feeds = make(map[cipher.PubKey]*indexHeads)
	i.c = c

	err = i.c.db.IdxDB().Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec

		// range feeds

		return feeds.Iterate(func(pk cipher.PubKey) (err error) {

			var hs data.Heads
			if hs, err = feeds.Heads(pk); err != nil {
				return err
			}

			// range heads

			var feedMap = newIndexHeads()

			err = hs.Iterate(func(nonce uint64) (err error) {

				var rs data.Roots
				if rs, err = hs.Roots(nonce); err != nil {
					return err
				}

				// get last

				var ir *data.Root

				err = rs.Descend(func(dr *data.Root) (err error) {
					ir = dr
					return data.ErrStopIteration // break
				})

				if err != nil {
					return err
				}

				feedMap.h[nonce] = ir // head (or nil)

				if ir != nil && feedMap.activet <= ir.Time {

					feedMap.activet = ir.Time // last timestamp of active feeds
					feedMap.activen = nonce   // active head (nonce)

				}

				return err
			})

			if err != nil {
				return err
			}

			i.feeds[pk] = feedMap

			return err
		})

	})

	if err != nil {
		return err
	}

	i.loadTime = time.Now().UnixNano()

	return err
}

// call under lock
func (i *Index) lastRoot(
	pk cipher.PubKey, // :
	nonce uint64, //     :
) (
	dr *data.Root, //    :
	err error, //        :
) {

	var hs, ok = i.feeds[pk]

	if ok == false { //nolint:staticcheck
		return nil, data.ErrNoSuchFeed
	}

	if dr, ok = hs.h[nonce]; ok == false { //nolint:staticcheck
		return nil, data.ErrNoSuchHead
	}

	if dr == nil {
		return nil, data.ErrNotFound // blank head
	}

	return
}

// AddFeed adds feed
func (i *Index) AddFeed(pk cipher.PubKey) (err error) {

	i.mx.Lock()
	defer i.mx.Unlock()

	if _, ok := i.feeds[pk]; ok {
		return // alrady has
	}

	err = i.c.db.IdxDB().Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
		return feeds.Add(pk)
	})

	if err != nil {
		return
	}

	// reset the change-on-write list;
	// we can't use i.feedsl[:0], because
	// next call rewrite list that can be
	// used by end-user
	i.feedsl = nil

	if _, ok := i.feeds[pk]; ok == false { //nolint:staticcheck
		i.feeds[pk] = newIndexHeads() // add to Index
	}

	return
}

// under lock
func (i *Index) findRoot(
	pk cipher.PubKey, // :
	nonce uint64, //     :
	seq uint64, //       :
) (
	dr *data.Root, //    :
	err error, //        :
) {

	// check out index first

	var hs, ok = i.feeds[pk]

	if ok == false { //nolint:staticcheck
		return nil, data.ErrNoSuchFeed
	}

	if dr, ok = hs.h[nonce]; ok == false { //nolint:staticcheck
		return nil, data.ErrNoSuchHead
	}

	if dr == nil {
		return nil, data.ErrNotFound // blank head
	}

	if dr.Seq == seq {
		return dr, err // found (the last)
	}

	// take a look the IdxDB

	err = i.c.db.IdxDB().Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
		var heads data.Heads
		if heads, err = feeds.Heads(pk); err != nil {
			return
		}
		var roots data.Roots
		if roots, err = heads.Roots(nonce); err != nil {
			return
		}
		dr, err = roots.Get(seq)
		return
	})

	return dr, err

}

func (i *Index) receivedRoot(
	pk cipher.PubKey,
	sig cipher.Sig,
	val []byte,
) (
	r *registry.Root,
	err error,
) {

	var hash = cipher.SumSHA256(val)
	if err = cipher.VerifyPubKeySignedHash(pk, sig, hash); err != nil {
		return
	}

	if r, err = registry.DecodeRoot(val); err != nil {
		return
	}

	r.Hash = hash // set the hash
	r.Sig = sig   // set the signature

	return
}

// PreviewRoot method used by node package to check
// a root recevied for preview-request. The method
// doesn't check feed, e.g. the ReceviedRoot method
// can returns data.ErrNoSuchFeed error. This method
// never return this error. And this method never set
// IsFull fields to true, if this Container already
// have this Root
func (i *Index) PreviewRoot(
	pk cipher.PubKey,
	sig cipher.Sig,
	val []byte,
) (
	r *registry.Root,
	err error,
) {

	i.mx.Lock()
	defer i.mx.Unlock()

	return i.receivedRoot(pk, sig, val)
}

// ReceivedRoot called by the node package to
// check a received root. The method verify hash
// and signature of the Root. The method also
// check database, may be DB already has this
// root. The method changes nothing in DB, it
// only checks the Root. The method set IsFull
// field of the Root to true if DB already have
// this Root
func (i *Index) ReceivedRoot(
	pk cipher.PubKey,
	sig cipher.Sig,
	val []byte,
) (
	r *registry.Root,
	err error,
) {

	i.mx.Lock()
	defer i.mx.Unlock()

	if r, err = i.receivedRoot(pk, sig, val); err != nil {
		r = nil // GC
		return
	}

	if _, err = i.findRoot(r.Pub, r.Nonce, r.Seq); err == nil {
		r.IsFull = true
		return
	} else if err == data.ErrNoSuchHead || err == data.ErrNotFound {
		err = nil // just not found (e.g. not full)
		return
	}

	return // data.ErrNoSuchFeed
}

func (i *Index) addRoot(r *registry.Root) (alreadyHave bool, err error) {

	if r.IsFull == false { //nolint:staticcheck
		return false, errors.New("can't add non-full Root: " + r.Short())
	}

	if r.Pub == (cipher.PubKey{}) {
		return false, errors.New("blank public key of Root: " + r.Short())
	}

	if r.Hash == (cipher.SHA256{}) {
		return false, errors.New("blank hash of Root: " + r.Short())
	}

	if r.Sig == (cipher.Sig{}) {
		return false, errors.New("blank signature of Root: " + r.Short())
	}

	// if the Root already exist

	var ir, dr *data.Root

	switch ir, err = i.findRoot(r.Pub, r.Nonce, r.Seq); {
	case err == nil:
		ir.Access = time.Now().UnixNano()
		alreadyHave = true
		return alreadyHave, err
	case err == data.ErrNotFound || err == data.ErrNoSuchHead:
	default:
		return alreadyHave, err // an error (data.ErrNoSuchFeed or another)
	}

	// save in the IdxDB

	err = i.c.db.IdxDB().Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec

		var hs data.Heads
		if hs, err = feeds.Heads(r.Pub); err != nil {
			return
		}

		var rs data.Roots
		if rs, err = hs.Add(r.Nonce); err != nil {
			return
		}

		// the Set never return "already exist" error

		if alreadyHave, err = rs.Has(r.Seq); err != nil {
			return
		}

		// create data.Root

		dr = new(data.Root)
		dr.Seq = r.Seq
		dr.Prev = r.Prev
		dr.Hash = r.Hash
		dr.Sig = r.Sig
		dr.Time = r.Time

		return rs.Set(dr)
	})

	if err != nil {
		return alreadyHave, err
	}

	if ir != nil && r.Seq < ir.Seq {
		// don't add to the Index the fucking, old,
		// outdated, never need, nobody need Root
		return alreadyHave, err
	}

	// add to the Index

	var hs = i.feeds[r.Pub]

	// replace the last

	hs.h[r.Nonce] = dr

	if hs.activet <= r.Time {
		hs.activet = r.Time
		hs.activen = r.Nonce
	}

	// add to stat
	i.stat.addRoot()

	return alreadyHave, err

}

func (i *Index) addSavedRoot(r *registry.Root, dr *data.Root) {

	// add to the Index

	var hs = i.feeds[r.Pub]

	// replace the last

	hs.h[r.Nonce] = dr

	if hs.activet <= r.Time {
		hs.activet = r.Time
		hs.activen = r.Nonce
	}

	// add to stat
	i.stat.addRoot()

	return //nolint:staticcheck
}

// AddRoot to DB. The method doesn't create feed of the root
// but if head of the root doesn't exist, then the method
// creates the head. The method never return already have error,
// it returns alreadyHave reply instead. E.g. if the Container
// already have this Root, then the alreadyHave reply will be
// true. The method never save the Root inside CXDS. E.g. the
// method adds the Root to index (that is necessary)
func (i *Index) AddRoot(r *registry.Root) (alreadyHave bool, err error) {

	i.mx.Lock()
	defer i.mx.Unlock()

	return i.addRoot(r)
}

// ActiveHead returns nonce of head that contains
// newest Root object of given feed. If the feed
// doesn't have Root objects, then reply will be
// zero. The ActiveHead method looks for timestamps
// of last Root objects only. E.g. the newest is
// the newest of last. For example if there are
// three heads with 100 Root oebjcts, then only
// timestamps of three last Root objects will
// be compared. If given feed doesn't exist in DB
// then reply will be zero too.
//
// Every new Root object can change the ActiveHead
// value if its head is different. And it's impossible
// to change the ActiveHead value other ways neither
// than inserting Root
func (i *Index) ActiveHead(pk cipher.PubKey) (nonce uint64) {

	i.mx.Lock()
	defer i.mx.Unlock()

	var hs, ok = i.feeds[pk]

	if ok == false { //nolint:staticcheck
		return
	}

	return hs.activen

}

// Heads returns list of heads of given feed.
// The list is list of nonces. One possible
// error is data.ErrNoSuchFeed
func (i *Index) Heads(pk cipher.PubKey) (heads []uint64, err error) {

	i.mx.Lock()
	defer i.mx.Unlock()

	var hs, ok = i.feeds[pk]

	if ok == false { //nolint:staticcheck
		return nil, data.ErrNoSuchFeed
	}

	heads = make([]uint64, 0, len(hs.h))

	for nonce := range hs.h {
		heads = append(heads, nonce)
	}

	return
}

// LastRootSeq returns seq of last Root of given head of
// given feed. The last Root is Root with the greatest
// seq number.
func (i *Index) LastRootSeq(
	pk cipher.PubKey, // : feed
	nonce uint64, //     : head
) (
	seq uint64, //       : seq of the last Root
	err error, //        : an error
) {

	i.mx.Lock()
	defer i.mx.Unlock()

	var lr *data.Root
	if lr, err = i.lastRoot(pk, nonce); err != nil {
		return // no such feed, no such head, not found
	}

	seq = lr.Seq
	return
}

// LastRoot returns last Root of given head of given feed.
// The last Root is Root with the greatest seq number. If
// given head of the feed is blank, then the LastRoot
// returns data.ErrNotFound. If the head does not exist
// then the LastRoot returns data.ErrNoSuchFeed.
//
// See also ActiveHead method. To get really last Root
// of a feed combine the methods
//
//	r, err = c.LastRoot(pk, c.ActiveHead())
//
// The LastRoot returns Root with signature
func (i *Index) LastRoot(
	pk cipher.PubKey, // : feed
	nonce uint64, //     : head
) (
	r *registry.Root, // : the Root
	err error, //        : an error
) {

	i.mx.Lock()
	defer i.mx.Unlock()

	var lr *data.Root
	if lr, err = i.lastRoot(pk, nonce); err != nil {
		return
	}

	r, err = i.c.rootByHash(lr.Hash)

	r.IsFull = true
	r.Sig = lr.Sig
	return
}

// delFeed deletes feed from IdxDB and from the Index
func (i *Index) delFeed(
	pk cipher.PubKey,
) (
	rhs []cipher.SHA256, // hashes of all roots
	err error,
) {

	// check out the feed

	var hs, ok = i.feeds[pk]

	if ok == false { //nolint:staticcheck
		return nil, data.ErrNoSuchFeed
	}

	// delete from IdxDB first

	err = i.c.db.IdxDB().Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec

		var heads data.Heads
		if heads, err = feeds.Heads(pk); err != nil {
			return
		}

		for nonce := range hs.h {

			var roots data.Roots
			if roots, err = heads.Roots(nonce); err != nil {
				return
			}

			err = roots.Ascend(func(dr *data.Root) (err error) { //nolint:errcheck,gosec
				rhs = append(rhs, dr.Hash)
				return
			})

			if err != nil {
				return
			}

		}

		return feeds.Del(pk) // remove the feed
	})

	if err != nil {
		return nil, err
	}

	// delete from the Index

	delete(i.feeds, pk)
	i.feedsl = nil // clear the list

	return rhs, err
}

// with lock
func (i *Index) delFeedLock(
	pk cipher.PubKey,
) (
	rhs []cipher.SHA256,
	err error,
) {

	i.mx.Lock()
	defer i.mx.Unlock()

	return i.delFeed(pk)
}

// DelFeed deletes feed with all heads and Root objects
func (i *Index) DelFeed(pk cipher.PubKey) (err error) {

	// with lock
	var rhs []cipher.SHA256
	if rhs, err = i.delFeedLock(pk); err != nil {
		return
	}

	// without lock
	for _, hash := range rhs {
		if err = i.delRootRelatedValues(hash); err != nil {
			return
		}
	}

	return
}

// delHead deletes head from IdxDB and from the Index
func (i *Index) delHead(
	pk cipher.PubKey,
	nonce uint64,
) (
	rhs []cipher.SHA256, // root hashes
	err error,
) {

	var hs, ok = i.feeds[pk]

	if ok == false { //nolint:staticcheck
		return nil, data.ErrNoSuchFeed
	}

	if _, ok = hs.h[nonce]; ok == false { //nolint:staticcheck
		return nil, data.ErrNoSuchHead
	}

	// delete from IdxDB first

	err = i.c.db.IdxDB().Tx(func(feed data.Feeds) (err error) { //nolint:errcheck,gosec

		var hs data.Heads
		if hs, err = feed.Heads(pk); err != nil {
			return
		}

		var roots data.Roots
		if roots, err = hs.Roots(nonce); err != nil {
			return
		}

		err = roots.Ascend(func(dr *data.Root) (err error) { //nolint:errcheck,gosec
			rhs = append(rhs, dr.Hash)
			return
		})

		if err != nil {
			return
		}

		return hs.Del(nonce) // remove the head
	})

	if err != nil {
		return nil, err
	}

	// delete from the Index

	delete(hs.h, nonce)

	return rhs, err

}

// with lock
func (i *Index) delHeadLock(
	pk cipher.PubKey,
	nonce uint64,
) (
	rhs []cipher.SHA256,
	err error,
) {

	i.mx.Lock()
	defer i.mx.Unlock()

	return i.delHead(pk, nonce)
}

// DelHead deletes given head. It can't remove head if at least one
// Root of the head is held, returning ErrRootIsHeld error
func (i *Index) DelHead(pk cipher.PubKey, nonce uint64) (err error) {

	// with lock

	var rhs []cipher.SHA256
	if rhs, err = i.delHeadLock(pk, nonce); err != nil {
		return
	}

	// without lock

	for _, hash := range rhs {
		if err = i.delRootRelatedValues(hash); err != nil {
			return
		}
	}

	return
}

// delRoot removes Root from IdxDB and from the Index
// and returns the Root to decrement all related values;
// the delRoot must be called undex lock; but decrementing
// should be performed outside the lock to release the
// Index
func (i *Index) delRoot(
	pk cipher.PubKey, //       : feed
	nonce uint64, //           : head
	seq uint64, //             : seq
) (
	rootHash cipher.SHA256, // : hash of the Root
	err error, //              : an error
) {

	// find the Root in the Index first

	var hs, ok = i.feeds[pk]

	if ok == false { //nolint:staticcheck
		err = data.ErrNoSuchFeed
		return rootHash, err
	}

	var ir *data.Root
	if ir, ok = hs.h[nonce]; ok == false { //nolint:staticcheck
		err = data.ErrNoSuchHead
		return rootHash, err
	}

	// remove from IdxDB first

	var (
		replaced bool // last Root replaced with older
		removed  bool // last Root removed and head is clean
	)

	err = i.c.db.IdxDB().Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec

		var hs data.Heads
		if hs, err = feeds.Heads(pk); err != nil {
			return err
		}

		var rs data.Roots
		if rs, err = hs.Roots(nonce); err != nil {
			return err
		}

		var dr *data.Root
		if dr, err = rs.Get(seq); err != nil {
			return err // DB failure or  'not found'
		}

		// keep hash of the Root to remove
		// from CXDS with all related objects

		rootHash = dr.Hash

		// if found, then the ir is not nil, because the head is not
		// blank and we keep last Root of every head in the Index
		// for fast access

		if err = rs.Del(seq); err != nil || seq != ir.Seq {
			return err
		}

		removed, replaced = true, true

		// we have to find last, since we delete it
		err = rs.Descend(func(dr *data.Root) (err error) {
			ir = dr         // last
			removed = false // replaced, not removed
			return data.ErrStopIteration
		})

		return err

	})

	if err != nil {
		return rootHash, err // DB failure
	}

	if removed == true { //nolint:staticcheck
		hs.h[nonce] = nil // blank head
	} else if replaced == true { //nolint:staticcheck
		hs.h[nonce] = ir
	}

	// cahnge active head if the Root is last for active head
	if ir.Time == hs.activet {
		hs.setActive()
	}

	return rootHash, err
}

// delRootLock is delRoot with lock
func (i *Index) delRootLock(
	pk cipher.PubKey, //       : feed
	nonce uint64, //           : head
	seq uint64, //             : seq
) (
	rootHash cipher.SHA256, // : hash of the Root
	err error, //              : an error
) {

	i.mx.Lock()
	defer i.mx.Unlock()

	return i.delRoot(pk, nonce, seq)
}

func (i *Index) delPackWalkFunc(
	r *registry.Root, //           : Root
) (
	pack registry.Pack, //         : special Pack for deleting
	walkFunc registry.WalkFunc, // : walk deleting
	err error, //                  : an error
) {

	var dpack *delPack
	if dpack, err = i.c.getDelPack(r); err != nil {
		return pack, walkFunc, err
	}

	walkFunc = func(
		hash cipher.SHA256, // : hash of object to decrement
		_ int, //              : never used
	) (
		deepper bool, //       : go deepper
		err error, //          : a DB error
	) {

		var (
			rc  int
			val []byte
		)

		// use Get instead of Inc(hash, -1) to get value
		// if it will be deleted by the -1

		if val, rc, err = i.c.getNoCache(hash, -1); err != nil {
			return deepper, err
		}

		// keep last if it was deleted
		if rc == 0 {
			dpack.last = hash
			dpack.val = val

			deepper = true // and go deepper
		}

		// we are going deepper only if value has been deleted by the Get
		return deepper, err

	}

	pack = dpack

	return pack, walkFunc, err
}

// delRootRelatedValues decrements all values related to
// given Root, including the Root itself and its Registry
func (i *Index) delRootRelatedValues(rootHash cipher.SHA256) (err error) {

	var r *registry.Root
	if r, err = i.c.rootByHash(rootHash); err != nil {
		return
	}

	var (
		pack     registry.Pack
		walkFunc registry.WalkFunc
	)

	if pack, walkFunc, err = i.delPackWalkFunc(r); err != nil {
		return
	}

	return i.c.walkRoot(pack, r, walkFunc)
}

// DelRoot deletes Root. The method returns data.ErrNotFound if
// Root doesn't exist
func (i *Index) DelRoot(pk cipher.PubKey, nonce, seq uint64) (err error) {

	// with lock
	var rootHash cipher.SHA256
	if rootHash, err = i.delRootLock(pk, nonce, seq); err != nil {
		return
	}

	// without lock
	return i.delRootRelatedValues(rootHash)
}

// Feeds returns list of feeds. For performance
// the list built once until changes (add/remove
// feed), thus it's unsafe to modify the list.
// Copy the list if you want to modify it
func (i *Index) Feeds() (feeds []cipher.PubKey) {

	i.mx.Lock()
	defer i.mx.Unlock()

	if len(i.feedsl) != len(i.feeds) {
		for pk := range i.feeds {
			i.feedsl = append(i.feedsl, pk)
		}
	}

	return i.feedsl

}

// HasFeed returns true if feed exists
func (i *Index) HasFeed(pk cipher.PubKey) (yep bool) {

	i.mx.Lock()
	defer i.mx.Unlock()

	_, yep = i.feeds[pk]
	return
}

// HasHead returns true if head exists. It
// returns false if given feed doesn't exist
func (i *Index) HasHead(pk cipher.PubKey, nonce uint64) (yep bool) {

	i.mx.Lock()
	defer i.mx.Unlock()

	var hs *indexHeads

	if hs, yep = i.feeds[pk]; yep == false { //nolint:staticcheck
		return
	}

	_, yep = hs.h[nonce]
	return
}

// AddHead adds head. A head will be added automatically if
// AddRoot called and head of the Root doesn't exist. But
// it's possible to add an empty head to save it in DB.
// It has no practical value. The head will be saved in DB
// and the Heads method will return it even if it empty
func (i *Index) AddHead(pk cipher.PubKey, nonce uint64) (err error) {

	i.mx.Lock()
	defer i.mx.Unlock()

	// check out Index first

	var hs, ok = i.feeds[pk]

	if ok == false { //nolint:staticcheck
		return data.ErrNoSuchFeed
	}

	if _, ok = hs.h[nonce]; ok {
		return err // already exists
	}

	// add to DB

	err = i.c.db.IdxDB().Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
		var heads data.Heads
		if heads, err = feeds.Heads(pk); err != nil {
			return err
		}

		_, err = heads.Add(nonce)
		return err
	})

	if err != nil {
		return err
	}

	// add to the Index

	i.feeds[pk] = newIndexHeads()
	return err

}

// Close Index syncing it with DB. Access time
// of Root objects is not saved in DB and should
// be synchronized with the Index
func (i *Index) Close() (err error) {
	i.mx.Lock()
	defer i.mx.Unlock()

	i.stat.Close() // close statistic first

	// Access time tracking is not implemented (would enable LRU eviction
	//                   is not implemented as well)

	return
}

func (i *Index) dataRoot(
	pk cipher.PubKey,
	nonce uint64,
	seq uint64,
) (
	dr *data.Root,
	err error,
) {

	i.mx.Lock()
	defer i.mx.Unlock()

	// the root can be last

	var hs, ok = i.feeds[pk]

	if ok == false { //nolint:staticcheck
		return nil, data.ErrNoSuchFeed
	}

	var last *data.Root
	if last, ok = hs.h[nonce]; ok == false { //nolint:staticcheck
		return nil, data.ErrNoSuchHead
	}

	if last == nil {
		return nil, data.ErrNotFound
	}

	if last.Seq == seq {
		return last, nil
	}

	// take a look DB

	err = i.c.db.IdxDB().Tx(func(feeds data.Feeds) (err error) { //nolint:errcheck,gosec
		var heads data.Heads
		if heads, err = feeds.Heads(pk); err != nil {
			return
		}
		var roots data.Roots
		if roots, err = heads.Roots(nonce); err != nil {
			return
		}
		dr, err = roots.Get(seq)
		return
	})

	return dr, err
}

// Root of feed-head  by seq number
func (i *Index) Root(
	feed cipher.PubKey,
	nonce uint64,
	seq uint64,
) (
	r *registry.Root,
	err error,
) {

	var dr *data.Root

	if dr, err = i.dataRoot(feed, nonce, seq); err != nil {
		return
	}

	if r, err = i.c.rootByHash(dr.Hash); err != nil {
		return
	}

	r.IsFull = true
	r.Sig = dr.Sig

	return
}
