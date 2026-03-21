package idxdb

import (
	"encoding/binary"
	"os"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
	bolt "go.etcd.io/bbolt"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

var (
	feedsBucket = []byte("f")       // feeds
	metaBucket  = []byte("m")       // meta information
	versionKey  = []byte("version") // encoded version in the meta bucket
)

type driveDB struct {
	b *bolt.DB
}

// NewDriveIdxDB creates data.IdxDB instance that
// keeps its data on drive
func NewDriveIdxDB(fileName string) (idx data.IdxDB, err error) {

	var created bool // true if db file has been created

	_, err = os.Stat(fileName)
	created = os.IsNotExist(err) // set the created var

	var b *bolt.DB

	b, err = bolt.Open(fileName, 0644, &bolt.Options{
		Timeout: time.Millisecond * 500,
	})

	if err != nil {
		return
	}

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

		_, err = tx.CreateBucketIfNotExists(feedsBucket)
		return
	})

	if err != nil {
		b.Close()
		return
	}

	idx = &driveDB{b}
	return
}

// Tx performs ACID-transaction
func (d *driveDB) Tx(txFunc func(feeds data.Feeds) (err error)) (err error) {
	return d.b.Update(func(tx *bolt.Tx) (err error) {
		return txFunc(&driveFeeds{tx.Bucket(feedsBucket)})
	})
}

// Close the DB
func (d *driveDB) Close() (err error) {
	return d.b.Close()
}

type driveFeeds struct {
	bk *bolt.Bucket
}

// Add feed or does nothing if its already exists
func (d *driveFeeds) Add(pk cipher.PubKey) (err error) {
	_, err = d.bk.CreateBucketIfNotExists(pk[:])
	return
}

// Del deletes feed if the feed is empty
func (d *driveFeeds) Del(pk cipher.PubKey) (err error) {

	var fs *bolt.Bucket

	if fs = d.bk.Bucket(pk[:]); fs == nil {

		return data.ErrNoSuchFeed

	}

	return d.bk.DeleteBucket(pk[:])
}

// Iterate over all feeds
func (d *driveFeeds) Iterate(iterateFunc data.IterateFeedsFunc) (err error) {
	var pk cipher.PubKey
	c := d.bk.Cursor()
	// we have to Seek(next) instead of using Next
	// because we allows mutations during the iteration
	for k, _ := c.First(); k != nil; k, _ = c.Seek(pk[:]) {
		copy(pk[:], k)
		if err = iterateFunc(pk); err != nil {
			if err == data.ErrStopIteration {
				err = nil
			}
			return
		}
		incSlice(pk[:])
	}
	return
}

// increment slice
func incSlice(b []byte) {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == 0xff {
			b[i] = 0
			continue // increase next byte
		}
		b[i]++
		return
	}
}

// Has performs presence check
func (d *driveFeeds) Has(pk cipher.PubKey) (ok bool, _ error) {
	ok = d.bk.Bucket(pk[:]) != nil
	return
}

// Heads returns bucket of Heads of given feed
func (d *driveFeeds) Heads(pk cipher.PubKey) (rs data.Heads, err error) {
	var bk = d.bk.Bucket(pk[:])
	if bk == nil {
		return nil, data.ErrNoSuchFeed
	}
	return &driveHeads{bk}, nil
}

func (d *driveFeeds) Len() (length int) {
	return d.bk.Stats().BucketN - 1
}

type driveHeads struct {
	bk *bolt.Bucket
}

func nonceToBytes(nonce uint64) (b []byte) {
	b = make([]byte, 8)
	binary.BigEndian.PutUint64(b, nonce)
	return
}

func nonceFromBytes(b []byte) (nonce uint64) {
	nonce = binary.BigEndian.Uint64(b)
	return
}

func (d *driveHeads) Roots(nonce uint64) (rs data.Roots, err error) {
	var bk *bolt.Bucket
	if bk = d.bk.Bucket(nonceToBytes(nonce)); bk == nil {
		return nil, data.ErrNoSuchHead
	}
	return &driveRoots{bk}, nil
}

func (d *driveHeads) Add(nonce uint64) (rs data.Roots, err error) {
	var bk *bolt.Bucket
	bk, err = d.bk.CreateBucketIfNotExists(nonceToBytes(nonce))
	if err != nil {
		return
	}
	return &driveRoots{bk}, nil
}

// Del head with given nonce
func (d *driveHeads) Del(nonce uint64) (err error) {

	var nonceb = nonceToBytes(nonce)

	if head := d.bk.Bucket(nonceb); head == nil {
		return data.ErrNoSuchHead
	}

	return d.bk.DeleteBucket(nonceb)
}

// Has head with given nonce
func (d *driveHeads) Has(nonce uint64) (ok bool, _ error) {
	ok = d.bk.Bucket(nonceToBytes(nonce)) != nil
	return
}

// Iterate over heads
func (d *driveHeads) Iterate(iterateFunc data.IterateHeadsFunc) (err error) {

	var c = d.bk.Cursor()

	for nonceb, _ := c.First(); nonceb != nil; nonceb, _ = c.Next() {
		if err = iterateFunc(nonceFromBytes(nonceb)); err != nil {
			break
		}
	}

	if err == data.ErrStopIteration {
		err = nil
	}

	return
}

func (d *driveHeads) Len() (length int) {
	return d.bk.Stats().BucketN - 1
}

type driveRoots struct {
	bk *bolt.Bucket
}

// Ascend iterates over all Root objects ascending order
func (d *driveRoots) Ascend(iterateFunc data.IterateRootsFunc) (err error) {

	var (
		seq uint64
		r   = new(data.Root)
		sb  = make([]byte, 8)

		c = d.bk.Cursor()
	)

	for seqb, er := c.First(); seqb != nil; seqb, er = c.Seek(seqb) {

		seq = binary.BigEndian.Uint64(seqb)

		if err = r.Decode(er); err != nil {
			panic(err)
		}

		if err = iterateFunc(r); err != nil {
			if err == data.ErrStopIteration {
				err = nil
			}
			return
		}

		seq++
		binary.BigEndian.PutUint64(sb, seq)
		seqb = sb
	}
	return
}

// Descend iterates over all Root objects descending order
func (d *driveRoots) Descend(iterateFunc data.IterateRootsFunc) (err error) {

	var (
		r = new(data.Root)
		c = d.bk.Cursor()
	)

	for seqb, er := c.Last(); seqb != nil; {

		if err = r.Decode(er); err != nil {
			panic(err)
		}

		if err = iterateFunc(r); err != nil {
			if err == data.ErrStopIteration {
				err = nil
			}
			return
		}

		c.Seek(seqb)        // rewind
		seqb, er = c.Prev() // prev
	}

	return
}

// Set new Root object or does nothing if
// the object already exists
func (d *driveRoots) Set(r *data.Root) (err error) {

	if err = r.Validate(); err != nil {
		return
	}

	var val, seqb []byte

	seqb = utob(r.Seq)

	if val = d.bk.Get(seqb); len(val) == 0 {

		// not found
		r.Access = time.Now().UnixNano()
		r.Create = r.Access

		return d.bk.Put(seqb, r.Encode())
	}

	// found
	var nr = new(data.Root)

	if err = nr.Decode(val); err != nil {
		panic(err)
	}

	r.Create = nr.Create // "reply"
	r.Access = nr.Access // "reply"

	// touch
	nr.Access = time.Now().UnixNano()

	return d.bk.Put(seqb, nr.Encode())
}

// Del deletes Root object by seq
func (d *driveRoots) Del(seq uint64) (err error) {
	return d.bk.Delete(utob(seq))
}

// Get Root object by seq
func (d *driveRoots) Get(seq uint64) (r *data.Root, err error) {

	var (
		seqb = utob(seq)
		val  = d.bk.Get(seqb)
	)

	if len(val) == 0 {
		err = data.ErrNotFound
		return
	}

	r = new(data.Root)

	if err = r.Decode(val); err != nil {
		panic(err)
	}

	var access = r.Access // keep

	r.Access = time.Now().UnixNano()

	if err = d.bk.Put(seqb, r.Encode()); err != nil {
		return
	}

	r.Access = access // set previous access time instead of current

	return
}

// Has performs precense check using seq
func (d *driveRoots) Has(seq uint64) (yep bool, _ error) {
	yep = len(d.bk.Get(utob(seq))) > 0
	return
}

// Len returns amount of Root objects
func (d *driveRoots) Len() int {
	return d.bk.Stats().KeyN
}

func utob(u uint64) (p []byte) {
	p = make([]byte, 8)
	binary.BigEndian.PutUint64(p, u)
	return
}
