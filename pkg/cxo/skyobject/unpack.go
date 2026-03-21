package skyobject

import (
	"errors"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

type unpackItem struct {
	inc     int  // saved times
	dec     int  // used times
	created bool // created
}

// An Unpack implements registry.Pack
// and used to change or cerate a Root
type Unpack struct {
	m     map[cipher.SHA256]*unpackItem // hash -> rc
	c     *Container                    // Set method
	*Pack                               // other methods
	sk    cipher.SecKey                 // owner
}

func (u *Unpack) reset() {
	for _, ui := range u.m {
		ui.dec = 0
	}
}

// Set value
func (u *Unpack) Set(key cipher.SHA256, val []byte) (err error) {

	var rc int
	if rc, err = u.c.Set(key, val, 1); err != nil {
		return
	}

	var ui, ok = u.m[key]

	if ok == false {
		ui = new(unpackItem)
		u.m[key] = ui
	}

	ui.inc++
	ui.created = (rc == 1)

	return
}

// Add value
func (u *Unpack) Add(val []byte) (key cipher.SHA256, err error) {

	key = cipher.SumSHA256(val)
	err = u.Set(key, val) // use Set of the Unpack
	return

}

// Unpack creates Unpack using given registry. Use
// the Unapck to modify a Root object and to save
// cahnges after.
func (c *Container) Unpack(
	sk cipher.SecKey,
	reg *registry.Registry,
) (
	up *Unpack,
	err error,
) {

	if reg == nil {
		err = errors.New("Registry is nil")
		return
	}

	if err = sk.Verify(); err != nil {
		return
	}

	up = &Unpack{
		sk:   sk,
		c:    c,
		m:    make(map[cipher.SHA256]*unpackItem),
		Pack: c.getPack(reg),
	}

	c.AddRegistryToCache(reg) // cache

	return

}

// Save cahnges of given Root updating seq number and
// timestamp of the Root. The Root should have correct
// Pub, and Nonce fields. The Seq field will be set
// to next inside the Save. The Save also set Hash and
// Prev fields of the Root, and signs the Root
func (c *Container) Save(up *Unpack, r *registry.Root) (err error) {

	// save the Root recursive

	if r.Pub == (cipher.PubKey{}) {
		return errors.New("blank Pub field of the Root")
	}

	if r.Nonce == 0 {
		return errors.New("zero Nonce field of the Root")
	}

	// check out Registry

	if rr := up.Registry().Reference(); r.Reg == (registry.RegistryRef{}) {

		r.Reg = rr // set

	} else if r.Reg != rr {

		if len(r.Refs) != 0 {
			return errors.New("can't change Registry of non-blank Root")
		}

		r.Reg = rr // set this if the Root is empty

	}

	// walk the Root first

	defer func() {
		if err != nil {
			up.reset()
		}
	}()

	for _, dr := range r.Refs {

		err = dr.Walk(up, func(
			hash cipher.SHA256, // :
			_ int, //              :
		) (
			deepper bool, //       :
			err error, //          :
		) {

			if hash == (cipher.SHA256{}) {
				return
			}

			// go deepper only if the object was created

			var ui, ok = up.m[hash]

			if ok == false {
				// this object was not created, then it already
				// exists in the CXDS, and we have to increment
				// rc of the object

				if _, err = c.Inc(hash, 1); err != nil {
					return
				}
				up.m[hash] = &unpackItem{inc: 1, dec: 1} // for Close

				return // false, nil
			}

			// here we reduce the ui.inc; if end-user saves an
			// object many times (or the object saved by Refs
			// modifications, for example if it's hash of
			// node of the Refs), then the inc will be greater
			// then one
			//
			// ui.inc - times saved
			// ui.dec - times used
			//
			// at the end of the Save we call c.Inc(key, ui.dec - ui.inc)
			// for every value (if the difference is not zero) to make
			// values in CXDS actual

			ui.dec++ // used
			deepper = ui.created
			return

		})

		if err != nil {
			return
		}

	}

	// ok, let's save the Root

	// check out Index first (has feed)
	if c.HasFeed(r.Pub) == false {
		return data.ErrNoSuchFeed
	}

	// save into Index and IdxDB
	var val []byte

	if val, err = c.Index.saveRoot(up, r); err != nil {
		return
	}

	// save the Root in CXDS

	if err = up.Set(r.Hash, val); err != nil {
		return
	}

	// save registry

	if err = up.Set(cipher.SHA256(r.Reg), up.Registry().Encode()); err != nil {
		return
	}

	// make rc of related objects actual

	for key, ui := range up.m {

		if key == r.Hash || key == cipher.SHA256(r.Reg) {
			ui.dec++
		}

		var inc = ui.dec - ui.inc

		if inc < 0 {
			// decrement
			if _, err = c.Inc(key, inc); err != nil {
				return // leave the CXDS broken if an error occurred
			}
		}

		delete(up.m, key) // saved

	}

	return
}

func (i *Index) saveRoot(
	up *Unpack,
	r *registry.Root,
) (
	val []byte,
	err error,
) {

	i.mx.Lock()
	defer i.mx.Unlock()

	// val []byte --> encoded Root
	var dr = new(data.Root)

	err = i.c.db.IdxDB().Tx(func(fs data.Feeds) (err error) { //nolint:errcheck
		var hs data.Heads
		if hs, err = fs.Heads(r.Pub); err != nil {
			return // no such feed
		}
		var roots data.Roots
		if roots, err = hs.Add(r.Nonce); err != nil {
			return
		}

		var (
			lastSeq  uint64
			lastHash cipher.SHA256
		)

		// get last
		err = roots.Descend(func(dr *data.Root) (err error) {
			lastSeq = dr.Seq
			lastHash = dr.Hash
			return data.ErrStopIteration // enough
		})

		if err != nil {
			return
		}

		if lastHash != (cipher.SHA256{}) {
			r.Seq = lastSeq + 1
			r.Prev = lastHash
		}

		// else -> 0 and blank

		r.Time = time.Now().UnixNano()

		// hash of the Root

		val = r.Encode()
		r.Hash = cipher.SumSHA256(val)
		r.IsFull = true

		// sign

		if r.Sig, err = cipher.SignHash(r.Hash, up.sk); err != nil {
			return err
		}

		dr.Seq = r.Seq
		dr.Prev = r.Prev
		dr.Hash = r.Hash
		dr.Sig = r.Sig
		dr.Time = r.Time

		return roots.Set(dr) // save

	})

	if err != nil {
		return
	}

	i.addSavedRoot(r, dr)
	return
}

/*

TODO (kostyarin): the NewRoot is just usablility trick

// NewRoot creates new blank Root object
func (c *Container) NewRoot(
	feed cipher.PubKey,
	nonce uint64,
	reg *registry.Registry,
) (
	r *registry.Root,
	up *Unpack,
	err error,
) {

	//

	return

}

*/

// Close the Unpack, rejecting all saved objects that
// will not be used
func (u *Unpack) Close() (err error) {
	for key, ui := range u.m {
		if ui.inc > 0 {
			if _, err = u.c.Inc(key, -ui.inc); err != nil {
				return
			}
		}
	}
	u.m = nil
	return
}
