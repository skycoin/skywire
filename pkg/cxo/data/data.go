package data

import (
	"errors"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

// common errors
var (
	ErrNotFound      = errors.New("not found")
	ErrStopIteration = errors.New("stop iteration")
	ErrNoSuchFeed    = errors.New("no such feed")
	ErrNoSuchHead    = errors.New("no such head")
	ErrInvalidSize   = errors.New("invalid size of encoded data")
)

// A DB represents joiner of IdxDB and CXDS
type DB struct {
	cxds  CXDS
	idxdb IdxDB
}

// IdxDB of the DB
func (d *DB) IdxDB() IdxDB {
	return d.idxdb
}

// CXDS of the DB
func (d *DB) CXDS() CXDS {
	return d.cxds
}

// Close the DB and all underlying
func (d *DB) Close() (err error) {
	if err = d.cxds.Close(); err != nil {
		d.idxdb.Close() // drop error
	} else {
		err = d.idxdb.Close() // use this error
	}
	return
}

// NewDB creates DB by given CXDS and IdxDB.
// The arguments must not be nil
func NewDB(cxds CXDS, idxdb IdxDB) *DB {
	if cxds == nil {
		panic("missing CXDS")
	}
	if idxdb == nil {
		panic("missing IdxDB")
	}
	return &DB{cxds, idxdb}
}

// A Root represents meta information
// of a saved skyobject.Root
type Root struct {
	Create int64 // received or saved at
	Access int64 // last access time

	Time int64 // timestamp of the Root

	Seq  uint64        // seq number of this Root
	Prev cipher.SHA256 // previous Root or empty if seq == 0

	Hash cipher.SHA256 // hash of the Root
	Sig  cipher.Sig    // signature of the Root
}

// Validate the Root
func (r *Root) Validate() (err error) {
	if r.Seq == 0 {
		if r.Prev != (cipher.SHA256{}) {
			return errors.New("(idxdb.Root.Validate) unexpected Prev hash")
		}
	} else if r.Prev == (cipher.SHA256{}) {
		return errors.New("(idxdb.Root.Validate) missing Prev hash")
	}

	if r.Hash == (cipher.SHA256{}) {
		return errors.New("(idxdb.Root.Validate) empty Hash")
	}

	if r.Sig == (cipher.Sig{}) {
		return errors.New("(idxdb.Root.Validate) empty Sig")
	}
	if r.Time == 0 {
		return errors.New("(idxdb.Root.Validate) zero timestamp")
	}
	return
}

// Encode the Root
func (r *Root) Encode() (p []byte) {
	return encoder.Serialize(r)
}

// Decode given encoded Root to this one
func (r *Root) Decode(p []byte) error {
	_, err := encoder.DeserializeRaw(p, r)

	return err
}
