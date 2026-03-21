package skyobject

import (
	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// pack for feed preview

// A Getter represents interface used
// to get an object if it doesn't exist
// in DB. The Getter used by the Preview.
// The Getter _must_ care about hash-value
// compliance
type Getter interface {
	Get(key cipher.SHA256) (val []byte, err error)
}

// A Preview implements registry.Pack for
// feeds preview. The Preview get object
// of a Root from database if possible,
// otherwise it request this objects using
// provided getter. The Preview used by
// the node package for feeds preview
type Preview struct {
	m map[cipher.SHA256][]byte // hash -> value (TODO: keep in memory?)
	g Getter                   // get from remote peer
	r *registry.Root           // root for Preview
	c *Container               // back reference to access DB and get Registry //nolint:unused

	*Pack // with Registry
}

// Root of the Preview
func (p *Preview) Root() (r *registry.Root) {
	return p.r
}

// Get from DB or from remote peer
func (p *Preview) Get(key cipher.SHA256) (val []byte, err error) {

	// check out map first
	var ok bool
	if val, ok = p.m[key]; ok {
		return // alrady received
	}

	if val, err = p.Pack.Get(key); err != nil && err != data.ErrNotFound {
		return // db failure
	}

	// not found

	if val, err = p.g.Get(key); err == nil {
		p.m[key] = val // save in the map
	}

	return
}

// Preview creates Preview using given
// Getter and registry.Root. It returns
// error if the Preview method can't
// obtain related Registry using Container
// or Getter. The Preview method can blocks
// calling Get from given Getter. The Preview
// method used by node package for feeds
// preview
func (c *Container) Preview(
	r *registry.Root, // : root to preview
	g Getter, //         : getter to get objects from remote peer
) (
	pack *Preview, //    : pack for previewing
	err error, //        : error
) {

	pack = new(Preview)

	pack.r = r
	pack.g = g
	pack.m = make(map[cipher.SHA256][]byte)

	var reg *registry.Registry
	if reg, err = c.Registry(r.Reg); err != nil {

		if err != data.ErrNotFound {
			return // DB failure
		}

		// not found, let's get it using the Getter

		var val []byte
		if val, err = g.Get(cipher.SHA256(r.Reg)); err != nil {
			return // can't receive
		}

		if reg, err = registry.DecodeRegistry(val); err != nil {
			return // invalid data received
		}

		// got it, let's continue

	}

	pack.Pack = c.getPack(reg)

	return

}
