package cxds

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
	bolt "go.etcd.io/bbolt"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/util/bbolthealth"
)

var (
	objsBucket = []byte("o") // objects bucket
	metaBucket = []byte("m") // meta information

	versionKey = []byte("version") // version

	amountAllKey  = []byte("amount_all")  // amount all
	amountUsedKey = []byte("amount_used") // amount used

	volumeAllKey  = []byte("volume_all")  // volume all
	volumeUsedKey = []byte("volume_used") // volume all
)

type driveCXDS struct {
	mx sync.Mutex // lock amounts and volumes

	amountAll  int // amount of all objects
	amountUsed int // amount of used objects

	volumeAll  int // volume of all objects
	volumeUsed int // volume of used objects

	b *bolt.DB
}

// DriveOptions tune the on-disk CXDS. Zero values use safe defaults.
type DriveOptions struct {
	// NoSync skips the fdatasync call at the end of each bbolt
	// transaction commit. Trades durability for an order-of-magnitude
	// reduction in disk write traffic (relevant for CXDS because
	// every refcount mutation today triggers a separate fsync — see
	// Get-with-inc and Set in this file).
	//
	// Appropriate when the underlying data is rebuildable on the
	// next process restart. CXDS is content-addressed: any leaf that
	// survives the crash is still indexable by its hash; any leaf
	// lost in a torn write can be re-fetched from peers via the next
	// CXO subscription cycle. The publisher's in-memory tree
	// (republished on every BatchWindow tick) authoritatively owns
	// the value set.
	//
	// Leave false (the default) for CXDS instances that must survive
	// a hard crash with byte-exact contents (e.g. operator-managed
	// archival CXO stores without peer republish).
	NoSync bool
}

// NewDriveCXDS opens existing CXDS-database
// or creates new by given file name. Underlying
// database is boltdb (github.com/boltdb/bolt).
// E.g. this stores data on disk. Uses safe defaults; for caller-
// tuned behavior use NewDriveCXDSWithOptions.
func NewDriveCXDS(fileName string) (ds data.CXDS, err error) {
	return NewDriveCXDSWithOptions(fileName, DriveOptions{})
}

// NewDriveCXDSWithOptions opens the on-disk CXDS with the given
// options. See DriveOptions for tunables.
func NewDriveCXDSWithOptions(fileName string, opts DriveOptions) (ds data.CXDS, err error) {

	var created bool // true if the file does not exist

	_, err = os.Stat(fileName)
	created = os.IsNotExist(err)

	// Probe-and-repair before the real open. RepairIfCorrupt returns
	// nil when the file is absent, healthy, or successfully renamed
	// aside as ".corrupt.<unix-ts>" — in the latter case the bolt.Open
	// below proceeds to create a fresh empty CXDS. This guards against
	// a commit-time panic later (bbolt's "circular dependency"
	// assertion fires in an internal batch goroutine and is not
	// recoverable from caller code). Treating CXDS corruption as
	// recreate-empty is safe: CXDS is content-addressed and any leaf
	// lost in a torn write is re-fetched from peers on the next CXO
	// subscription cycle.
	if rErr := bbolthealth.RepairIfCorrupt(fileName); rErr != nil {
		return nil, fmt.Errorf("cxds: integrity-check %s: %w", fileName, rErr)
	}
	if !created {
		if _, statErr := os.Stat(fileName); os.IsNotExist(statErr) {
			created = true
		}
	}

	var b *bolt.DB
	b, err = bolt.Open(fileName, 0644, &bolt.Options{
		Timeout: time.Millisecond * 500,
		NoSync:  opts.NoSync,
	})

	if err != nil {
		return ds, err
	}

	defer func() {

		if err != nil {
			b.Close()            //nolint:errcheck,gosec // close
			if created == true { //nolint:staticcheck
				os.Remove(fileName) //nolint:errcheck,gosec // clean up
			}
		}

	}()

	var saveStat bool

	err = b.Update(func(tx *bolt.Tx) (err error) {

		// first of all, take a look the meta bucket
		var info = tx.Bucket(metaBucket)

		if info == nil {

			// if the file has not been created, then
			// this DB file seems outdated (version 0)
			if created == false { //nolint:staticcheck
				return ErrMissingMetaInfo // report
			}

			// create the bucket and put meta information
			if info, err = tx.CreateBucket(metaBucket); err != nil {
				return err
			}

			// put version
			if err = info.Put(versionKey, versionBytes()); err != nil {
				return err
			}

			// put stat

			saveStat = true // save zeroes

		} else {

			// check out the version

			var vb []byte
			if vb = info.Get(versionKey); len(vb) == 0 {
				return ErrMissingVersion
			}

			switch vers := int(binary.BigEndian.Uint32(vb)); {
			case vers == Version: // ok
			case vers < Version:
				return ErrOldVersion
			case vers > Version:
				return ErrNewVersion
			}

		}

		_, err = tx.CreateBucketIfNotExists(objsBucket)
		return err

	})

	if err != nil {
		return ds, err
	}

	var dr = &driveCXDS{b: b} // wrap

	// stat

	if saveStat == true { //nolint:staticcheck
		err = dr.saveStat()
	} else {
		err = dr.loadStat()
	}

	if err != nil {
		return ds, err
	}

	ds = dr
	return ds, err
}

func (d *driveCXDS) loadStat() (err error) {

	d.mx.Lock()
	defer d.mx.Unlock()

	return d.b.View(func(tx *bolt.Tx) (err error) {

		var (
			info = tx.Bucket(metaBucket)
			val  []byte
		)

		// amount all

		if val = info.Get(amountAllKey); len(val) != 4 {
			return ErrWrongValueLength
		}

		d.amountAll = int(decodeUint32(val))

		// amount used

		if val = info.Get(amountUsedKey); len(val) != 4 {
			return ErrWrongValueLength
		}

		d.amountUsed = int(decodeUint32(val))

		// volume all

		if val = info.Get(volumeAllKey); len(val) != 4 {
			return ErrWrongValueLength
		}

		d.volumeAll = int(decodeUint32(val))

		// volume used

		if val = info.Get(volumeUsedKey); len(val) != 4 {
			return ErrWrongValueLength
		}

		d.volumeUsed = int(decodeUint32(val))

		return err

	})

}

func (d *driveCXDS) saveStat() (err error) {

	d.mx.Lock()
	defer d.mx.Unlock()

	return d.b.Update(func(tx *bolt.Tx) (err error) {

		var info = tx.Bucket(metaBucket)

		// amount all

		err = info.Put(amountAllKey, encodeUint32(uint32(d.amountAll))) //nolint:gosec

		if err != nil {
			return err
		}

		// amount used

		err = info.Put(amountUsedKey, encodeUint32(uint32(d.amountUsed))) //nolint:gosec

		if err != nil {
			return err
		}

		// volume all

		err = info.Put(volumeAllKey, encodeUint32(uint32(d.volumeAll))) //nolint:gosec

		if err != nil {
			return err
		}

		// volume used

		err = info.Put(volumeUsedKey, encodeUint32(uint32(d.volumeUsed))) //nolint:gosec
		return err

	})

}

func (d *driveCXDS) av(rc, nrc uint32, vol int) {

	d.mx.Lock()
	defer d.mx.Unlock()

	if rc == 0 { // was dead
		if nrc > 0 { // an be resurrected
			d.amountUsed++
			d.volumeUsed += vol
		}
		return // else -> as is
	}

	// rc > 0 (was alive)

	if nrc == 0 { // and be killed
		d.amountUsed--
		d.volumeUsed -= vol
	}

}

func (d *driveCXDS) incr(
	o *bolt.Bucket, // : objects
	key []byte, //     : key[:]
	val []byte, //     : value without leading rc (4 bytes)
	rc uint32, //      : existing rc
	inc int, //        : change the rc
) (
	nrc uint32, //     : new rc
	err error, //      : an error
) {

	switch {
	case inc == 0:
		nrc = rc // all done (no changes)
		return nrc, err
	case inc < 0:
		inc = -inc                           // change its sign
		if uinc := uint32(inc); uinc >= rc { //nolint:gosec
			nrc = 0 // zero
		} else {
			nrc = rc - uinc // reduce (rc > 0)
		}
	case inc > 0:
		nrc = rc + uint32(inc) //nolint:gosec // increase the rc
	}

	var repl = make([]byte, 4, 4+len(val))
	setRefsCount(repl, nrc)
	repl = append(repl, val...)
	err = o.Put(key[:], repl)

	if rc != nrc {
		d.av(rc, nrc, len(val))
	}

	return nrc, err
}

// getInTx is Get's bucket-level work scoped to a caller-provided tx.
// Returns the value (copy), the (possibly updated) rc, and any error.
// Used by both the per-op Get (which opens its own tx) and the batch
// shim (which inherits a parent tx).
func (d *driveCXDS) getInTx(
	tx *bolt.Tx,
	key cipher.SHA256,
	inc int,
) (
	val []byte,
	rc uint32,
	err error,
) {

	var (
		o   = tx.Bucket(objsBucket)
		got = o.Get(key[:])
	)

	if len(got) == 0 {
		return nil, 0, data.ErrNotFound
	}

	rc = getRefsCount(got)
	val = make([]byte, len(got)-4)
	copy(val, got[4:])

	rc, err = d.incr(o, key[:], val, rc, inc)
	return val, rc, err
}

// Get value by key changing or
// leaving as is references counter.
//
// Concurrency / write-batching: when inc != 0 the read-modify-write
// is routed through bbolt's db.Batch instead of db.Update. Batch
// coalesces concurrent calls into a single transaction → a single
// fdatasync (or single page dirty in NoSync mode), instead of one
// fsync per refcount bump. The semantics for a single call are
// identical to Update: the function still runs in one atomic tx
// and the caller blocks until the batch commits.
//
// CXO callers (cache.Get-with-inc on cache miss, subscriber pulls,
// publisher tree-walk holds) frequently fire concurrent refcount
// bumps during sync cycles; that's where Batch pays for itself.
func (d *driveCXDS) Get(
	key cipher.SHA256, // :
	inc int, //           :
) (
	val []byte, //        :
	rc uint32, //         :
	err error, //         :
) {

	fn := func(tx *bolt.Tx) (err error) {
		val, rc, err = d.getInTx(tx, key, inc)
		return err
	}

	if inc == 0 {
		err = d.b.View(fn) // lookup only — no transaction commit at all
	} else {
		err = d.b.Batch(fn) // refcount bump — coalesces with concurrent calls
	}

	return val, rc, err
}

func panicf(format string, args ...interface{}) {
	panic(fmt.Sprintf(format, args...))
}

func (d *driveCXDS) addAll(vol int) {
	d.mx.Lock()
	defer d.mx.Unlock()

	d.amountAll++
	d.volumeAll += vol
}

// setInTx is Set's bucket-level work scoped to a caller-provided tx.
// The tx must be writable (i.e. opened via db.Update or db.Batch).
// Callers are responsible for the value-length and inc preconditions
// — setInTx will panic on inc <= 0 mirroring the exported Set.
func (d *driveCXDS) setInTx(
	tx *bolt.Tx,
	key cipher.SHA256,
	val []byte,
	inc int,
) (
	rc uint32,
	err error,
) {

	if inc <= 0 {
		panicf("invalid inc argument in CXDS.Set: %d", inc)
	}

	if len(val) == 0 {
		return 0, ErrEmptyValue
	}

	var (
		o   = tx.Bucket(objsBucket)
		got = o.Get(key[:])
	)

	if len(got) == 0 {
		d.addAll(len(val))
		return d.incr(o, key[:], val, 0, 1)
	}

	return d.incr(o, key[:], got[4:], getRefsCount(got), inc)
}

// Set value and its references counter.
//
// Two paths: (1) the key is new — bucket has no entry — and the
// value must be created with rc=1. (2) the key exists — only its
// refcount is being bumped. Path (2) is the hot one (every
// publisher tree-walk hold fires it), so it uses db.Batch for
// concurrent coalescing. Path (1) uses db.Update for normal
// new-object durability semantics.
//
// We don't know which path applies without reading the bucket
// first, so we do a quick View probe and dispatch. The probe is
// cheap (no tx commit, no fsync) and the saved write cost when the
// key exists is large.
func (d *driveCXDS) Set(
	key cipher.SHA256,
	val []byte,
	inc int,
) (
	rc uint32,
	err error,
) {

	// Probe whether the key already exists so we can pick Batch
	// (refcount-only) vs Update (new value) without rolling the
	// branch decision into a single committed tx.
	var exists bool
	if vErr := d.b.View(func(tx *bolt.Tx) error {
		exists = len(tx.Bucket(objsBucket).Get(key[:])) > 0
		return nil
	}); vErr != nil {
		return rc, vErr
	}

	fn := func(tx *bolt.Tx) (err error) {
		rc, err = d.setInTx(tx, key, val, inc)
		return err
	}

	if exists {
		err = d.b.Batch(fn) // refcount bump only — coalesce
	} else {
		err = d.b.Update(fn) // new value — durable single-tx
	}

	return rc, err
}

// incInTx is Inc's bucket-level work scoped to a caller-provided tx.
func (d *driveCXDS) incInTx(
	tx *bolt.Tx,
	key cipher.SHA256,
	inc int,
) (
	rc uint32,
	err error,
) {

	var (
		o   = tx.Bucket(objsBucket)
		got = o.Get(key[:])
	)

	if len(got) == 0 {
		return 0, data.ErrNotFound
	}

	rc = getRefsCount(got)

	if inc == 0 {
		return rc, nil // presence check only
	}

	return d.incr(o, key[:], got[4:], rc, inc)
}

// Inc changes references counter
func (d *driveCXDS) Inc(
	key cipher.SHA256,
	inc int,
) (
	rc uint32,
	err error,
) {

	fn := func(tx *bolt.Tx) (err error) {
		rc, err = d.incInTx(tx, key, inc)
		return err
	}

	if inc == 0 {
		err = d.b.View(fn) // lookup only
	} else {
		err = d.b.Batch(fn) // refcount bump — coalesce concurrent calls
	}

	return rc, err
}

func (d *driveCXDS) del(rc uint32, vol int) {

	d.mx.Lock()
	defer d.mx.Unlock()

	if rc > 0 {
		d.amountUsed--
		d.volumeUsed -= vol
	}

	d.amountAll--
	d.volumeAll -= vol
}

// delInTx is Del's bucket-level work scoped to a caller-provided tx.
func (d *driveCXDS) delInTx(tx *bolt.Tx, key cipher.SHA256) (err error) {

	var (
		o   = tx.Bucket(objsBucket)
		got = o.Get(key[:])
	)

	if len(got) == 0 {
		return nil // not found
	}

	if err = o.Delete(key[:]); err != nil {
		return err
	}

	d.del(getRefsCount(got), len(got)-4)
	return nil
}

// Del deletes value unconditionally
func (d *driveCXDS) Del(
	key cipher.SHA256,
) (
	err error,
) {

	// Batch: cache cleaning sweeps fire many Dels in rapid
	// succession; coalescing them into one transaction (and one
	// commit) is appropriate. Visibility semantics are preserved —
	// Batch blocks the caller until the enclosing tx commits.
	err = d.b.Batch(func(tx *bolt.Tx) (err error) {
		return d.delInTx(tx, key)
	})

	return
}

// Iterate all keys
func (d *driveCXDS) Iterate(iterateFunc data.IterateObjectsFunc) (err error) {

	err = d.b.View(func(tx *bolt.Tx) (err error) {

		var (
			key cipher.SHA256
			c   = tx.Bucket(objsBucket).Cursor()
		)

		for k, v := c.First(); k != nil; k, v = c.Next() {

			copy(key[:], k)

			if err = iterateFunc(key, getRefsCount(v), v[4:]); err != nil {
				if err == data.ErrStopIteration {
					err = nil
				}
				return
			}

		}

		return

	})

	return
}

// IterateDel all keys deleting
func (d *driveCXDS) IterateDel(
	iterateFunc data.IterateObjectsDelFunc,
) (
	err error,
) {

	err = d.b.Update(func(tx *bolt.Tx) (err error) {

		var (
			key cipher.SHA256
			rc  uint32
			c   = tx.Bucket(objsBucket).Cursor()
			del bool
		)

		// Seek instead of the Next, because we allows modifications
		// and the BoltDB requires Seek after mutating

		for k, v := c.First(); k != nil; k, v = c.Seek(key[:]) {

			copy(key[:], k)

			rc = getRefsCount(v)

			if del, err = iterateFunc(key, rc, v[4:]); err != nil {
				if err == data.ErrStopIteration {
					err = nil
				}
				return err
			}

			if del == true { //nolint:staticcheck
				if err = c.Delete(); err != nil {
					return err
				}

				d.del(rc, len(v)-4) // stat
			}

			incSlice(key[:]) // next
		}

		return err

	})

	return err
}

// Amount of objects
func (d *driveCXDS) Amount() (all, used int) {
	d.mx.Lock()
	defer d.mx.Unlock()

	return d.amountAll, d.amountUsed
}

// Volume of objects (only values)
func (d *driveCXDS) Volume() (all, used int) {
	d.mx.Lock()
	defer d.mx.Unlock()

	return d.volumeAll, d.volumeUsed
}

// Close DB
func (d *driveCXDS) Close() (err error) {

	if err = d.saveStat(); err != nil && err != bolt.ErrDatabaseNotOpen { //nolint:staticcheck
		d.b.Close() //nolint:errcheck,gosec // drop error
		return
	}

	return d.b.Close() //nolint:errcheck,gosec
}

func copySlice(in []byte) (got []byte) { //nolint:unused
	got = make([]byte, len(in))
	copy(got, in)
	return
}

// RunBatch opens one writable bbolt tx and exposes a CXDS view scoped
// to it. Every Set/Get/Inc/Del invoked on the scoped handle runs
// inside that single tx, so the cost of opening + committing the tx
// is amortized across the whole batch. Callers must not retain the
// scoped handle past fn's return — the tx is committed (or rolled
// back on error) when fn exits.
//
// The dominant workload that drives this path is the publisher
// tree-walk (skyobject.Container.Save via cxds.Set per encoded leaf),
// where pre-batch each leaf opened its own db.Update. Coalescing N
// per-leaf transactions into one removes N-1 meta-page writes,
// freelist re-allocations, and (when NoSync is off) fdatasyncs.
//
// Iterate, IterateDel, and Close are deliberately unavailable on the
// scoped handle — the publisher path doesn't need them and exposing
// them inside the writer tx risks deadlocking against bbolt's reader
// semantics. Callers that need a write-then-iterate cycle should
// commit the batch first.
func (d *driveCXDS) RunBatch(fn func(scoped data.CXDS) error) (err error) {
	return d.b.Update(func(tx *bolt.Tx) error {
		return fn(&txDriveCXDS{parent: d, tx: tx})
	})
}

// txDriveCXDS is a CXDS view bound to a single writable bbolt tx.
// All Set/Get/Inc/Del operations route through the parent driveCXDS's
// *InTx helpers using the pinned tx, so they share a single commit at
// the end of the enclosing RunBatch.
type txDriveCXDS struct {
	parent *driveCXDS
	tx     *bolt.Tx
}

func (s *txDriveCXDS) Get(key cipher.SHA256, inc int) ([]byte, uint32, error) {
	return s.parent.getInTx(s.tx, key, inc)
}

func (s *txDriveCXDS) Set(key cipher.SHA256, val []byte, inc int) (uint32, error) {
	return s.parent.setInTx(s.tx, key, val, inc)
}

func (s *txDriveCXDS) Inc(key cipher.SHA256, inc int) (uint32, error) {
	return s.parent.incInTx(s.tx, key, inc)
}

func (s *txDriveCXDS) Del(key cipher.SHA256) error {
	return s.parent.delInTx(s.tx, key)
}

func (s *txDriveCXDS) Iterate(data.IterateObjectsFunc) error {
	return ErrUnsupportedInBatch
}

func (s *txDriveCXDS) IterateDel(data.IterateObjectsDelFunc) error {
	return ErrUnsupportedInBatch
}

// Amount/Volume read the parent's atomic counters; safe to call
// during a batch.
func (s *txDriveCXDS) Amount() (all, used int) { return s.parent.Amount() }
func (s *txDriveCXDS) Volume() (all, used int) { return s.parent.Volume() }

func (s *txDriveCXDS) Close() error {
	return ErrUnsupportedInBatch
}

// RunBatch on a batch-scoped handle reuses the active tx — nested
// batch is just a passthrough.
func (s *txDriveCXDS) RunBatch(fn func(scoped data.CXDS) error) error {
	return fn(s)
}
