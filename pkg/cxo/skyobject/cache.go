package skyobject

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// A CachePolicy represents cache policy
// that is obvious
type CachePolicy int

// cache policies
const (
	LRU CachePolicy = iota // LRU cache
	LFU                    // LFU cache
)

// String implements fmt.Stringer interface
func (c CachePolicy) String() string {
	switch c {
	case LRU:
		return "LRU"
	case LFU:
		return "LFU"
	}
	return fmt.Sprintf("CachePolicy<%d>", c)
}

// An Object represents DB object
// that is []byte, its hash and
// references counter
type Object struct {
	Key cipher.SHA256 // hash of the Val
	Val []byte        // vlaue
	RC  int           // references count (hard)
	Err error         // max object size error
}

// points that depend on policy
type cachePoints int

// touch the points
func (c *cachePoints) touch(policy CachePolicy) {

	switch policy {
	case LRU:
		*c = cachePoints(time.Now().Unix()) // last access
	case LFU:
		*c++ // access
	default:
		panic("cache with undefined CachePolicy: " + policy.String())
	}

}

type item struct {
	// chan -> inc
	fwant map[chan<- Object]int // wanters

	// the points is time.Now().Unix() or number of acesses;
	// e.g. the points is LRU or LFU number and depends on
	// the cache policy
	cachePoints

	// hard rc = cc -fc
	// sync rc = cc - rc

	// if cc is 0, then item removed (even if fc > 0);
	// but in this case (fc > 0)

	fc  int    // fillers incs
	cc  int    // cached rc
	rc  int    // db incs
	val []byte // value
}

func (i *item) isWanted() (ok bool) {
	return len(i.fwant) > 0
}

func (i *item) isFilling() (ok bool) {
	return len(i.val) == 0
}

// itemRegistry
type itemRegistry struct {
	r           *registry.Registry // the Registry
	cachePoints                    // LRU or LFU points
}

// A Cache is internal and used by Container.
// The Cache can't be created and used outside
type Cache struct {
	mx sync.Mutex

	c      *Container // back reference
	enable bool       //

	amount int // number of items in the Cache
	volume int // volume of items in the Cache

	amountc int // clean down to this (const)
	volumec int // clean down to this (const)

	is map[cipher.SHA256]*item
	rs map[registry.RegistryRef]*itemRegistry

	stat *cxdsStat

	closeo sync.Once //nolint:unused

	// batchTx, when non-nil, redirects c.db()'s view of the underlying
	// CXDS to a transaction-scoped handle pinned by a WithBatch call.
	// Every Cache.Set / Cache.Get / Cache.Inc that fires while a batch
	// is active runs its bbolt work inside that single tx instead of
	// opening its own — collapsing the publisher tree-walk's per-leaf
	// commits into one.
	//
	// Reads happen under c.mx (every Cache write/read path holds it),
	// so concurrent Set/Get/Inc callers serialize their tx use through
	// the mutex; the bbolt tx is never touched by two goroutines at
	// once. WithBatch tears the pin down only after taking c.mx again
	// to drain any in-flight CXDS user — see WithBatch for the full
	// invariant.
	batchTx atomic.Pointer[data.CXDS]
}

// initialize the Cache
func (c *Container) initCache() {

	c.Cache.c = c // back reference

	c.Cache.enable = !(c.conf.CacheMaxAmount == 0 || c.conf.CacheMaxVolume == 0)

	c.Cache.amountc = int(
		float64(c.conf.CacheMaxAmount) * (1.0 - c.conf.CacheCleaning),
	)
	c.Cache.volumec = int(
		float64(c.conf.CacheMaxVolume) * (1.0 - c.conf.CacheCleaning),
	)

	c.Cache.amount = 0 // cache amount
	c.Cache.volume = 0 // cache volume

	c.Cache.is = make(map[cipher.SHA256]*item, c.conf.CacheMaxAmount)
	c.Cache.rs = make(map[registry.RegistryRef]*itemRegistry,
		c.conf.CacheRegistries)

	c.Cache.stat = newCxdsStat(c.conf.RollAvgSamples)
}

func (c *Cache) amountVolume() (a, v int) {
	c.mx.Lock()
	defer c.mx.Unlock()

	return c.amount, c.volume
}

// reset the Cache
func (c *Cache) reset() { //nolint:unused
	c.is = nil
	c.rs = nil
	c.stat.Close() //nolint:errcheck,gosec
	c.stat = nil
}

// db returns the CXDS that subsequent Cache operations should target.
// When a WithBatch call has pinned a transaction-scoped CXDS, db()
// returns that handle so the operation joins the open batch; otherwise
// it returns the underlying store.
//
// Callers must invoke db() while holding c.mx — the same mutex
// WithBatch synchronizes against — so the returned pointer remains
// valid for the duration of the caller's CXDS call.
func (c *Cache) db() data.CXDS {
	if tx := c.batchTx.Load(); tx != nil {
		return *tx
	}
	return c.c.db.CXDS()
}

// WithBatch runs fn with the Cache's underlying CXDS pinned to a
// single transaction-scoped handle. Every Cache.Set / Cache.Get /
// Cache.Inc invoked from fn (directly or transitively, including the
// recursive walks inside Container.Save) runs its bbolt work inside
// that one tx — replacing N per-leaf commits with a single one. fn
// returning nil commits the tx; a non-nil return rolls it back.
//
// The pin lives in c.batchTx (atomic). Reads happen under c.mx, so
// concurrent Cache writers/readers from outside fn naturally serialize
// against fn's tx use through the mutex — the bbolt tx is never
// touched by two goroutines at once. The tear-down sequence on the
// way out:
//
//  1. Store nil into c.batchTx so any newly-arriving Cache.Set sees
//     the untwisted CXDS (and will block in bbolt's writer mutex
//     until our tx commits, as it would have pre-batch).
//  2. Lock/unlock c.mx once to drain any in-flight Cache.Set that
//     captured the now-stale tx pointer before step 1 — they finish
//     their work under our serialization while we wait.
//  3. Return; the bbolt tx commits at this point.
//
// fn must not retain the pinned handle past its return — the tx is
// gone afterward.
func (c *Cache) WithBatch(fn func() error) error {
	return c.c.db.CXDS().RunBatch(func(scoped data.CXDS) (err error) {
		c.batchTx.Store(&scoped)
		defer func() {
			c.batchTx.Store(nil)
			// Wait for any in-flight Cache operation that may still be
			// using the scoped CXDS to finish before letting bbolt
			// commit. Holding the mutex briefly is sufficient: every
			// Cache CXDS call holds c.mx for its duration, so once we
			// acquire it nobody else is mid-tx.
			c.mx.Lock()
			c.mx.Unlock() //nolint:staticcheck // intentional drain
		}()
		return fn()
	})
}

// call it under lock
func (c *Cache) addRegistryToCache(r *registry.Registry) {

	// if it already exists

	if ir, ok := c.rs[r.Reference()]; ok == true { //nolint:staticcheck
		ir.touch(c.c.conf.CachePolicy)
		return
	}

	// clean if need

	if len(c.rs) == c.c.conf.CacheRegistries {

		var (
			tormr registry.RegistryRef // reference
			tormp cachePoints          // points
		)

		for rr, ir := range c.rs {
			if ir.cachePoints > tormp {
				tormr = rr
				tormp = ir.cachePoints
			}
		}

		delete(c.rs, tormr)

	}

	// add

	var ir = &itemRegistry{r: r}

	ir.touch(c.c.conf.CachePolicy)
	c.rs[r.Reference()] = ir
}

// AddRegistryToCache adds given registry to Cache
func (c *Cache) AddRegistryToCache(r *registry.Registry) {

	if c.c.conf.CacheRegistries <= 0 {
		return
	}

	c.mx.Lock()
	defer c.mx.Unlock()

	c.addRegistryToCache(r)

}

// Registry returns Registry by reference. The
// Registry looks the Cache first. If it gets Registry
// from DB, then it put the Registry to the Cache
func (c *Cache) Registry(
	rr registry.RegistryRef, // : the reference
) (
	r *registry.Registry, //    : registry
	err error, //               : an error
) {

	c.mx.Lock()
	defer c.mx.Unlock()

	// check out cache first

	if ir, ok := c.rs[rr]; ok == true { //nolint:staticcheck
		r = ir.r
		ir.touch(c.c.conf.CachePolicy)
		return r, err
	}

	// get from DB and add to cache after

	var val []byte
	if val, _, err = c.get(cipher.SHA256(rr), 0); err != nil {
		return r, err
	}

	if r, err = registry.DecodeRegistry(val); err != nil {
		return r, err
	}

	if c.c.conf.CacheRegistries >= 0 {
		c.addRegistryToCache(r)
	}

	return r, err
}

// Clsoe the Cache returning DB error
// or nil
func (c *Cache) Close() (err error) {

	c.mx.Lock()
	defer c.mx.Unlock()

	// sync items
	for key, it := range c.is {

		// make wanted items filling
		if it.isWanted() == true { //nolint:staticcheck
			for _, winc := range it.fwant {
				it.fc += winc
			}
			it.fwant = nil
		}

		it.cc -= it.fc // remove all fincs
		it.fc = 0

		if err = c.delete(key, it); err != nil { //nolint:errcheck,gosec
			return
		}

	}

	// close the CXDS
	err = c.c.db.CXDS().Close() //nolint:errcheck,gosec
	c.stat.Close()              // close goroutine

	return
}

// delete item from the Cache
func (c *Cache) delete(key cipher.SHA256, it *item) (err error) {

	var inc = it.cc - it.rc // real rc

	if inc != 0 {

		_, err = c.db().Inc(key, inc)
		c.stat.addWritingDBRequest() // write DB

		if err != nil {
			return err
		}

	}

	c.amount--
	c.volume -= len(it.val)

	if it.fc == 0 {
		delete(c.is, key)
		return err
	}

	// keep the item if the fc is not zero,
	// just make it filling

	it.val = nil
	it.rc = 0
	it.cc = 0
	it.cachePoints = 0

	return err
}

// clean the Cache down to lower boundary
func (c *Cache) cleanDown(vol int) (err error) {

	// stat (average cleaning time)
	var tp = time.Now()
	defer func() { c.stat.addCacheCleaning(time.Now().Sub(tp)) }() //nolint:staticcheck

	type rankItem struct {
		key cipher.SHA256
		it  *item
	}

	// Value slice (not []*rankItem) so we don't allocate a tiny heap
	// object per cache entry; this turns one make+N small allocs into
	// a single backing array per cleanDown call.
	var rank = make([]rankItem, 0, len(c.is))

	for key, it := range c.is {

		if it.isWanted() == true { //nolint:staticcheck
			continue // skip wanted
		}

		if it.isFilling() == true { //nolint:staticcheck
			continue // skip filling (where val is nil)
		}

		rank = append(rank, rankItem{key, it})

	}

	// sort the rank using cachePoints; we
	// are removing items with less points

	sort.Slice(rank, func(i, j int) bool {
		return rank[i].it.cachePoints < rank[j].it.cachePoints
	})

	// clean by amount first

	if c.amount+1 > c.c.conf.CacheMaxAmount {

		var (
			i  int      // to reduce the rank slice
			ri rankItem // for the range (we need the i)
		)

		for i, ri = range rank {

			if c.amount < c.amountc { // actually, `amount + 1 == ...`
				break
			}

			// delete item from the Cache
			if err = c.delete(ri.key, ri.it); err != nil { //nolint:errcheck,gosec
				return err // fail on first error
			}

		}

		rank = rank[i:] // shift

	}

	// clean by volume if need

	if c.volume+vol < c.c.conf.CacheMaxVolume {
		return err // enough
	}

	for _, ri := range rank {

		if c.volume+vol <= c.volumec {
			break
		}

		if err = c.delete(ri.key, ri.it); err != nil { //nolint:errcheck,gosec
			return err // fail on first error
		}

	}

	return err
}

// create regular item in the cache
func (c *Cache) putItem(
	key cipher.SHA256,
	val []byte,
	rc int,
) (
	err error,
) {

	if c.enable == false { //nolint:staticcheck
		return err
	}

	if len(val) > c.c.conf.CacheMaxItemSize {
		return err
	}

	if rc == 0 {
		return err // don't cache stale values
	}

	switch {
	case c.amount+1 > c.c.conf.CacheMaxAmount,
		c.volume+len(val) > c.c.conf.CacheMaxVolume:

		if err = c.cleanDown(len(val)); err != nil {
			return err
		}

	}

	var it = &item{rc: rc, cc: rc, val: val}

	it.touch(c.c.conf.CachePolicy)
	c.is[key] = it

	c.amount++
	c.volume += len(val)

	return err
}

// create item with fc > 0; e.g.
// add data to the filling item
func (c *Cache) putFillingItem(
	val []byte,
	rc int,
	it *item, // filling item
) (
	err error,
) {

	if c.enable == false { //nolint:staticcheck
		return err
	}

	if len(val) > c.c.conf.CacheMaxItemSize {
		return err
	}

	if rc == 0 {
		return err // don't cache stale values
	}

	switch {
	case c.amount+1 > c.c.conf.CacheMaxAmount,
		c.volume+len(val) > c.c.conf.CacheMaxVolume:

		if err = c.cleanDown(len(val)); err != nil {
			return err
		}

	}

	it.val = val
	it.rc = rc // real
	it.cc = rc // real

	it.touch(c.c.conf.CachePolicy)

	c.amount++
	c.volume += len(val)

	return err
}

func incr(cc, inc int) (dcc int) {
	if cc+inc < 0 {
		return // 0
	}
	return cc + inc
}

// if item is filling then it exist in DB, but removed
// from the cache (by the cleanDown, or if the cahe is
// not used (is disabled)); thus if the cache disabled,
// then the getFilling method is just (data.CXDS).Get;
// otherwise, the getFilling load item to the cache, but
// unlike the Get (non filling item), the rc will be a
// 'hard rc', e.g. the 'hard rc' is rc (or cc) - fc;
//
// this way, if an item put to DB by filler has hard rc
// greater then 0, then the item has entire subtree;
// e.g. the hard rc means that item is part of a full
// Root object and it is guaranteed that the item has
// entire subtree in the DB
//
// the guarantee is necessary for fillers; a filler
// can skip subtree (instead of digging it); but a
// filler can not skip subtree if the subtree is a
// subtree of another filler; because if the another
// filler fails, it (the another filler) removes the
// subtree from DB
func (c *Cache) getFilling(
	key cipher.SHA256,
	inc int,
	it *item,
) (
	val []byte,
	rc int,
	err error,
) {

	var urc uint32
	val, urc, err = c.db().Get(key, inc)
	c.stat.addDBGet(inc)

	if err != nil {
		return
	}

	rc = int(urc) - it.fc

	err = c.putFillingItem(val, int(urc), it)
	return
}

// like the Get, but don't put item into the
// cache if it's not in the cache and don't
// touch the item
func (c *Cache) getNoCache(
	key cipher.SHA256,
	inc int,
) (
	val []byte,
	rc int,
	err error,
) {

	c.mx.Lock()
	defer c.mx.Unlock()

	var it, ok = c.is[key]

	if ok == true { //nolint:staticcheck
		if it.isWanted() == true { //nolint:staticcheck
			return nil, 0, data.ErrNotFound
		}

		// nothing wrong with caching a filling item

		if it.isFilling() == true { //nolint:staticcheck
			return c.getFilling(key, inc, it)
		}

		// the delete below can clean the val field
		val = it.val

		// remove item if it's cc is zero
		if it.cc = incr(it.cc, inc); it.cc == 0 {
			c.delete(key, it) //nolint:errcheck,gosec
		} else {
			c.stat.addCacheGet(inc) // effective cache get
		}

		rc = it.cc - it.fc // hard rc
		return val, rc, err
	}

	// not found in the Cache

	var urc uint32
	val, urc, err = c.db().Get(key, inc)
	c.stat.addDBGet(inc)

	if err != nil {
		return val, rc, err
	}

	rc = int(urc) // hard rc

	// don't put to cache
	return val, rc, err
}

// under lock
func (c *Cache) get(
	key cipher.SHA256,
	inc int,
) (
	val []byte,
	rc int,
	err error,
) {

	var it, ok = c.is[key]

	if ok == true { //nolint:staticcheck
		if it.isWanted() == true { //nolint:staticcheck
			return nil, 0, data.ErrNotFound
		}

		if it.isFilling() == true { //nolint:staticcheck
			return c.getFilling(key, inc, it)
		}

		// the delete below can clean the val field
		val = it.val

		// remove item if it's cc is zero
		if it.cc = incr(it.cc, inc); it.cc == 0 {
			c.delete(key, it) //nolint:errcheck,gosec
		} else {
			c.stat.addCacheGet(inc) // effective cache get
			it.touch(c.c.conf.CachePolicy)
		}

		rc = it.cc - it.fc // hard rc

		return val, rc, err
	}

	// not found in the Cache

	var urc uint32
	val, urc, err = c.db().Get(key, inc)
	c.stat.addDBGet(inc)

	if err != nil {
		return val, rc, err
	}

	rc = int(urc) // hard rc

	err = c.putItem(key, val, rc)
	return val, rc, err
}

// IsCached returns true if items with given key is cached.
// The item is not value and a cached item may not exist
// in the cache and in DB too. If an item is cached, then
// it should not be removed from DB. Because of write-behind
// caching. Also, in some rare cases, a value can be
// "resurrected" if it's cached (e.g. its rc will be increased
// from zero).
//
// Use this method cleaning up DB.
//
// CAUTION: callers must not be holding any lock that Cache.Set's
// critical section can transitively reach. In particular, never
// call IsCached from inside a bbolt.DB.Update callback — Cache.Set
// takes c.mx then opens a bbolt RWTx, so calling IsCached from
// within a bbolt RWTx inverts that ordering and deadlocks. For
// iteration patterns, use CachedKeys to snapshot once up front.
func (c *Cache) IsCached(key cipher.SHA256) (yep bool) {

	c.mx.Lock()
	defer c.mx.Unlock()

	_, yep = c.is[key]
	return
}

// CachedKeys returns a fresh snapshot of every key currently tracked
// by the cache. The returned set has no ties to the live map — once
// the lock is released the cache can mutate freely, and callers can
// hold the set while doing IO (e.g. bbolt iteration) without risking
// the IsCached lock-ordering deadlock.
//
// The snapshot can race with Set: a key that's about to enter the
// cache may be missing, and a key that just left may still appear.
// Callers (DB cleanup) accept that — the goal is "don't delete an
// object the cache might still resurrect", and a slightly stale
// snapshot strictly favors safety: a key that races out of the
// cache after the snapshot was taken is still kept on disk for one
// extra sweep, never deleted while live.
func (c *Cache) CachedKeys() map[cipher.SHA256]struct{} {
	c.mx.Lock()
	defer c.mx.Unlock()
	out := make(map[cipher.SHA256]struct{}, len(c.is))
	for k := range c.is {
		out[k] = struct{}{}
	}
	return out
}

// Get from cache or DB, increasing, leaving as is, or
// reducing references counter. For most reading cases
// the inc argument should be zero. If value doesn't
// exist the Get returns data.ErrNotFound. It's possible
// to get and remove value using the inc argument. The
// Get returns value and new references counter. The Get
// can returns an error that doesn't relate to given
// element. Putting element to the Cache the Get can
// clean up cache to free space, syncing with DB. In this
// case if the synching fails, the Get can returns valid
// value and rc and some error. Anyway, any DB failure
// should be fatal. E.g. all errors except data.ErrNotFound
// and ObjectIsTooLargeError means that data in the DB can
// be lost
func (c *Cache) Get(
	key cipher.SHA256,
	inc int,
) (
	val []byte,
	rc int,
	err error,
) {

	c.mx.Lock()
	defer c.mx.Unlock()

	return c.get(key, inc)
}

// never block
func sendWanted(gc chan<- Object, obj Object) {
	select {
	case gc <- obj:
	default:
	}
}

func (c *Cache) setWanted(
	key cipher.SHA256,
	val []byte,
	inc int,
	it *item,
) (
	rc int,
	err error,
) {

	if len(val) > c.c.conf.MaxObjectSize {
		err = &ObjectIsTooLargeError{key}

		// ignore the inc

		var obj = Object{key, nil, 0, err}

		for gc := range it.fwant {
			sendWanted(gc, obj)
		}

		delete(c.is, key) // force
		return rc, err
	}

	var wincs int // incs of wants (add to fc after)

	// add incs of wants
	for _, winc := range it.fwant {
		wincs += winc
	}

	// save
	var urc uint32
	urc, err = c.db().Set(key, val, inc+wincs)
	c.stat.addWritingDBRequest()

	if err != nil {
		return rc, err // DB failure
	}

	rc = int(urc) - (wincs + it.fc) // hard rc

	// send hard rc to wanters
	var obj = Object{key, val, rc, nil}

	for gc := range it.fwant {
		sendWanted(gc, obj)
	}

	it.fwant = nil // not wanted anymore (GC)
	it.fc += wincs // incs of fillers (of wanters)

	err = c.putFillingItem(val, int(urc), it)
	return rc, err
}

func (c *Cache) setFilling(
	key cipher.SHA256,
	val []byte, //nolint:unparam
	inc int,
	it *item,
) (
	rc int,
	err error,
) {

	// the item is filling, and value in db,
	//
	// (1) cache disabled or
	// (2) the item will be dropped (turned filling)
	//     after cleanDown

	// in both cases we have to access DB

	// but if the item is filling, then it exists in DB
	// and we can avoid size check, and we can call
	// incItem instead of the Set, but for filling items
	// it's equal to call incFilling

	return c.incFilling(key, inc, it)
}

// Set adds value to DB and to the Cache if it's enabled.
// The inc argument must be greater then zero
func (c *Cache) Set(
	key cipher.SHA256,
	val []byte,
	inc int,
) (
	rc int,
	err error,
) {

	if inc <= 0 {
		panic("invalid inc argument of Set method: " + fmt.Sprint(inc))
	}

	c.mx.Lock()
	defer c.mx.Unlock()

	var it, ok = c.is[key]

	if ok == true { //nolint:staticcheck

		if it.isWanted() == true { //nolint:staticcheck
			return c.setWanted(key, val, inc, it)
		}

		if it.isFilling() == true { //nolint:staticcheck
			return c.setFilling(key, val, inc, it)
		}

		// the delete below can clean the it.val
		val = it.val //nolint:ineffassign,staticcheck

		// remove item if it's cc is zero
		if it.cc = incr(it.cc, inc); it.cc == 0 {
			c.delete(key, it) //nolint:errcheck,gosec // not effective cache set //nolint:gosec
		} else {
			c.stat.addWritingCacheRequest() // effective cache set
			it.touch(c.c.conf.CachePolicy)
		}

		rc = it.cc - it.fc // hard rc
		return rc, err

	}

	// not found in the cache

	if len(val) > c.c.conf.MaxObjectSize {
		err = &ObjectIsTooLargeError{key}
		return rc, err
	}

	var urc uint32
	urc, err = c.db().Set(key, val, inc)
	c.stat.addWritingDBRequest()

	if err != nil {
		return rc, err
	}

	rc = int(urc)
	err = c.putItem(key, val, rc)
	return rc, err
}

func (c *Cache) incFilling(
	key cipher.SHA256,
	inc int,
	it *item,
) (
	rc int,
	err error,
) {

	var (
		urc uint32
		val []byte
	)

	if c.enable == true { //nolint:staticcheck
		val, urc, err = c.db().Get(key, inc)
	} else {
		urc, err = c.db().Inc(key, inc)
	}

	c.stat.addDBGet(inc)

	if err != nil {
		return
	}

	rc = int(urc) - it.fc // hard rc

	err = c.putFillingItem(val, int(urc), it)
	return
}

func (c *Cache) incItem(
	key cipher.SHA256, // :
	inc int, //           :
	it *item, //          :
) (
	rc int, //            :
	err error, //         :
) {

	if it.isWanted() == true { //nolint:staticcheck
		return 0, data.ErrNotFound
	}

	if it.isFilling() == true { //nolint:staticcheck
		return c.incFilling(key, inc, it)
	}

	// remove item if it's cc is zero
	if it.cc = incr(it.cc, inc); it.cc == 0 {
		c.delete(key, it) //nolint:errcheck,gosec,gosec
	} else {
		c.stat.addCacheGet(inc) // effective cache get
		it.touch(c.c.conf.CachePolicy)
	}

	rc = it.cc - it.fc // hard rc
	return
}

// under lock
func (c *Cache) inc(
	key cipher.SHA256, // :
	inc int, //           :
) (
	rc int, //            :
	err error, //         :
) {

	var it, ok = c.is[key]

	if ok == true { //nolint:staticcheck
		return c.incItem(key, inc, it)
	}

	// not found in the Cache

	// get only if cache enabled

	var (
		urc uint32
		val []byte
	)

	if c.enable == true { //nolint:staticcheck
		val, urc, err = c.db().Get(key, inc)
	} else {
		urc, err = c.db().Inc(key, inc)
	}

	c.stat.addDBGet(inc)

	if err != nil {
		return rc, err
	}

	err = c.putItem(key, val, int(urc))
	return rc, err

}

// Inc used to change rc of an existing value
func (c *Cache) Inc(
	key cipher.SHA256, // :
	inc int, //           :
) (
	rc int, //            :
	err error, //         :
) {

	c.mx.Lock()
	defer c.mx.Unlock()

	return c.inc(key, inc)
}

//
// filling related methods
//

// Want subscribes to object. If object already exists
// then it will be sent through given channel. If the Want
// called many times with the same channel, then requested
// object will be received once. The Want used for filling
// and have very specific pitfalls. The inc is inc of fillers
// and after the Want, a filler have to call Unwant and Finc
// to notify the cache that this object now is part of a
// full Root objects. Or to notify the cache that filling
// breaks and inc of the object decrements. The cache never
// blocks sending to channels, thus, the channel should be
// buffered.
func (c *Cache) Want(
	key cipher.SHA256,
	gc chan<- Object,
	inc int,
) (
	err error,
) {

	c.mx.Lock()
	defer c.mx.Unlock()

	var it, ok = c.is[key]

	if ok == true { //nolint:staticcheck

		if it.isWanted() == true { //nolint:staticcheck
			it.fwant[gc] += inc
			return err
		}

		if it.isFilling() == true { //nolint:staticcheck // but not wanted

			var (
				val []byte
				rc  int
			)

			if val, rc, err = c.getFilling(key, inc, it); err != nil {

				if err == data.ErrNotFound {
					it.fwant = map[chan<- Object]int{gc: inc} // want
					err = nil                                 // clear
				}

				return err // DB failure or nil (not found)
			}

			// found

			// the getFilling method returns hard rc, but we have to
			// subtract the inc from it to make it real hard

			it.fc += inc
			sendWanted(gc, Object{key, val, rc - inc, nil})

			return err
		}

		// regular item in the cache

		it.fc += inc
		sendWanted(gc, Object{key, it.val, it.cc - it.fc, nil})

		if inc == 0 {
			c.stat.addReadingCacheRequest() // effective
		} else {
			c.stat.addWritingCacheRequest() // effective
		}

		it.touch(c.c.conf.CachePolicy)
		return err

	}

	// not in cache, but it can be in DB

	var (
		urc uint32
		val []byte
	)

	val, urc, err = c.db().Get(key, inc)
	c.stat.addDBGet(inc)

	if err != nil {

		if err == data.ErrNotFound {
			// create wanted item
			it = new(item)
			it.fwant = map[chan<- Object]int{gc: inc}
			c.is[key] = it
			err = nil // clear
		}

		return err // DB failure or 'not found'
	}

	// found

	sendWanted(gc, Object{key, val, int(urc) - inc, nil})

	// create filling item in the cache

	it = new(item)
	it.fc = inc
	c.is[key] = it

	err = c.putFillingItem(val, int(urc), it)
	return err
}

// Unwant used to unsibscribe after the Want, if,
// by some reasons, the object is not wanted anymore
func (c *Cache) Unwant(
	key cipher.SHA256,
	gc chan<- Object,
) {

	c.mx.Lock()
	defer c.mx.Unlock()

	if it := c.is[key]; it != nil && it.fwant != nil {
		delete(it.fwant, gc)
		if len(it.fwant) == 0 {
			it.fwant = nil // GC
			// not wanted, not filling, not regular
			if it.fc == 0 && len(it.val) == 0 {
				delete(c.is, key)
			}
		}
	}

}

// SetWanted is like the Set, but is set value
// only if the value is wanted. The SetWanted
// never returns "not wanted" error.
func (c *Cache) SetWanted(
	key cipher.SHA256,
	val []byte,
) (
	rc int,
	err error,
) {

	c.mx.Lock()
	defer c.mx.Unlock()

	var it, ok = c.is[key]

	if ok == false { //nolint:staticcheck
		return
	}

	if it.isWanted() == false { //nolint:staticcheck
		return
	}

	return c.setWanted(key, val, 0, it)
}

// Finc is like the Inc, but it used by fillers to
// apply incs of filler or reject them. See the
// Want for details. The method does nothing is
// item with given key is not a filling item.
// If the inc argument greater then zero, then
// it is the apply. Otherwise, it is the reject.
// The Finc must be called after the moment when
// item received through gc channell passed to
// the Want method. The Finc must not be called
// if the received item is erroneous
func (c *Cache) Finc(
	key cipher.SHA256,
	inc int,
) (
	err error,
) {

	if inc == 0 {
		panic("(Cache).Finc called with zero for: " + key.Hex()[:7])
	}

	c.mx.Lock()
	defer c.mx.Unlock()

	var it, ok = c.is[key]

	if ok == false { //nolint:staticcheck
		return err
	}

	if it.fc == 0 {
		return err // not a filling item
	}

	// apply

	if inc > 0 {

		it.fc -= inc

		if it.fc > 0 {
			return err
		}

		if it.fc < 0 {
			// Refcount inconsistency: a filler decremented fc by
			// more than the cache's recorded Inc total. Likely a
			// duplicate Finc on the same filler.incs map, or an
			// Inc/Finc mismatch across overlapping fillers. The
			// historical behavior was to panic — that's a hard
			// process kill that observably restarts TPD on prod
			// every 30-40s. Clamp to zero and continue so the
			// process stays alive; the worst case is a leaked
			// filling-item slot, not data corruption.
			log.Printf("[CXO] Finc apply clamped negative fc to 0 for %s (was %d, inc %d)",
				key.Hex(), it.fc+inc, inc)
			it.fc = 0
		}

		// the fc is zero

		// else, the fc is 0 (remove if it's filling)

		if it.isWanted() == true { //nolint:staticcheck
			return err // can't remove wanted item
		}

		if len(it.val) == 0 { // filling item (filling only)
			delete(c.is, key)
		}

		// a filling items turns to be a regular (since the fc is zero)

		// keep
		it.touch(c.c.conf.CachePolicy)
		return err
	}

	// reject

	it.fc += inc // fc = fc - abs(inc)

	if it.fc < 0 {
		// Same recovery as the apply branch — see comment above.
		log.Printf("[CXO] Finc reject clamped negative fc to 0 for %s (was %d, inc %d)",
			key.Hex(), it.fc-inc, inc)
		it.fc = 0
	}

	// and if it.fc turns to be zero, then the
	// incItem removes it

	_, err = c.incItem(key, inc, it) // in db
	return err
}
