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

// NewDriveCXDS opens existing CXDS-database
// or creates new by given file name. Underlying
// database is boltdb (github.com/boltdb/bolt).
// E.g. this stores data on disk
func NewDriveCXDS(fileName string) (ds data.CXDS, err error) { //nolint:nakedret

	var created bool // true if the file does not exist

	_, err = os.Stat(fileName)
	created = os.IsNotExist(err)

	var b *bolt.DB
	b, err = bolt.Open(fileName, 0644, &bolt.Options{
		Timeout: time.Millisecond * 500,
	})

	if err != nil {
		return
	}

	defer func() {

		if err != nil {
			b.Close() // close
			if created == true {
				os.Remove(fileName) // clean up
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
			if created == false {
				return ErrMissingMetaInfo // report
			}

			// create the bucket and put meta information
			if info, err = tx.CreateBucket(metaBucket); err != nil {
				return
			}

			// put version
			if err = info.Put(versionKey, versionBytes()); err != nil {
				return
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
		return

	})

	if err != nil {
		return
	}

	var dr = &driveCXDS{b: b} // wrap

	// stat

	if saveStat == true {
		err = dr.saveStat()
	} else {
		err = dr.loadStat()
	}

	if err != nil {
		return
	}

	ds = dr
	return
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

		return

	})

}

func (d *driveCXDS) saveStat() (err error) {

	d.mx.Lock()
	defer d.mx.Unlock()

	return d.b.Update(func(tx *bolt.Tx) (err error) {

		var info = tx.Bucket(metaBucket)

		// amount all

		err = info.Put(amountAllKey, encodeUint32(uint32(d.amountAll)))

		if err != nil {
			return
		}

		// amount used

		err = info.Put(amountUsedKey, encodeUint32(uint32(d.amountUsed)))

		if err != nil {
			return
		}

		// volume all

		err = info.Put(volumeAllKey, encodeUint32(uint32(d.volumeAll)))

		if err != nil {
			return
		}

		// volume used

		err = info.Put(volumeUsedKey, encodeUint32(uint32(d.volumeUsed)))
		return

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
		return
	case inc < 0:
		inc = -inc // change its sign
		if uinc := uint32(inc); uinc >= rc {
			nrc = 0 // zero
		} else {
			nrc = rc - uinc // reduce (rc > 0)
		}
	case inc > 0:
		nrc = rc + uint32(inc) // increase the rc
	}

	var repl = make([]byte, 4, 4+len(val))
	setRefsCount(repl, nrc)
	repl = append(repl, val...)
	err = o.Put(key[:], repl)

	if rc != nrc {
		d.av(rc, nrc, len(val))
	}

	return
}

// Get value by key changing or
// leaving as is references counter
func (d *driveCXDS) Get(
	key cipher.SHA256, // :
	inc int, //           :
) (
	val []byte, //        :
	rc uint32, //         :
	err error, //         :
) {

	var tx = func(tx *bolt.Tx) (err error) {

		var (
			o   = tx.Bucket(objsBucket)
			got = o.Get(key[:])
		)

		if len(got) == 0 {
			return data.ErrNotFound // pass through
		}

		rc = getRefsCount(got)
		val = make([]byte, len(got)-4)
		copy(val, got[4:])

		rc, err = d.incr(o, key[:], val, rc, inc)
		return
	}

	if inc == 0 {
		err = d.b.View(tx) // lookup only
	} else {
		err = d.b.Update(tx) // some changes
	}

	return
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

// Set value and its references counter
func (d *driveCXDS) Set(
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
		err = ErrEmptyValue
		return
	}

	err = d.b.Update(func(tx *bolt.Tx) (err error) {

		var (
			o   = tx.Bucket(objsBucket)
			got = o.Get(key[:])
		)

		if len(got) == 0 {

			// created
			d.addAll(len(val))

			rc, err = d.incr(o, key[:], val, 0, 1)
			return
		}

		rc, err = d.incr(o, key[:], got[4:], getRefsCount(got), inc)
		return
	})

	return
}

// Inc changes references counter
func (d *driveCXDS) Inc(
	key cipher.SHA256,
	inc int,
) (
	rc uint32,
	err error,
) {

	var tx = func(tx *bolt.Tx) (_ error) {

		var (
			o   = tx.Bucket(objsBucket)
			got = o.Get(key[:])
		)

		if len(got) == 0 {
			return data.ErrNotFound
		}

		rc = getRefsCount(got)

		if inc == 0 {
			return // done
		}

		rc, err = d.incr(o, key[:], got[4:], rc, inc)
		return
	}

	if inc == 0 {
		err = d.b.View(tx) // lookup only
	} else {
		err = d.b.Update(tx) // changes required
	}

	return
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

// Del deletes value unconditionally
func (d *driveCXDS) Del(
	key cipher.SHA256,
) (
	err error,
) {

	err = d.b.Update(func(tx *bolt.Tx) (err error) {

		var (
			o   = tx.Bucket(objsBucket)
			got = o.Get(key[:])
		)

		if len(got) == 0 {
			return // not found
		}

		if err = o.Delete(key[:]); err != nil {
			return
		}

		d.del(getRefsCount(got), len(got)-4)
		return // nil
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
				return
			}

			if del == true {
				if err = c.Delete(); err != nil {
					return
				}

				d.del(rc, len(v)-4) // stat
			}

			incSlice(key[:]) // next
		}

		return

	})

	return
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

	if err = d.saveStat(); err != nil && err != bolt.ErrDatabaseNotOpen {
		d.b.Close() // drop error
		return
	}

	return d.b.Close() //nolint:errcheck,gosec
}

func copySlice(in []byte) (got []byte) {
	got = make([]byte, len(in))
	copy(got, in)
	return
}
