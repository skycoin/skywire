package skyobject

import (
	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// A Pack implements registry.Pack interface
type Pack struct {
	reg   *registry.Registry
	c     *Container
	deg   registry.Degree
	flags registry.Flags
}

// Registry returns related registry
func (p *Pack) Registry() (reg *registry.Registry) {
	return p.reg
}

// Get value by hash
func (p *Pack) Get(key cipher.SHA256) (val []byte, err error) {
	val, _, err = p.c.Get(key, 0)
	return
}

// Set key-value pair
func (p *Pack) Set(key cipher.SHA256, val []byte) (err error) {

	if len(val) > p.c.conf.MaxObjectSize {
		return &ObjectIsTooLargeError{key}
	}

	_, err = p.c.Set(key, val, 1)
	return
}

// Add is Set that calculates hash inside
func (p *Pack) Add(val []byte) (key cipher.SHA256, err error) {
	key = cipher.SumSHA256(val)
	err = p.Set(key, val)
	return
}

// Degree of the Pack
func (p *Pack) Degree() registry.Degree {
	return p.deg
}

// SetDegree to the Pack
func (p *Pack) SetDegree(degree registry.Degree) (err error) {
	if err = degree.Validate(); err == nil {
		p.deg = degree
	}
	return
}

// Flags of the Pack
func (p *Pack) Flags() registry.Flags {
	return p.flags
}

// AddFlags adds given flags to flags of the Pack (|)
func (p *Pack) AddFlags(flags registry.Flags) {
	p.flags |= flags
}

// ClearFlags isclears given flags from flags of the Pack (&^)
func (p *Pack) ClearFlags(flags registry.Flags) {
	p.flags &^= flags
}

// Pack returns Pack that obtains values from DB. The
// Pack implements Add and Set method, but using of the
// methods creates objects in DB that never be removed.
//
// Use the Pack as read-only to avoid ownerless objects
// in DB.
//
// To create objects updating (or creating) a Root see
// Unpack and Save methods of the Container.
//
// The Pack can be used for Root.Tree and similar methods
// that doesn't change anything in DB.
//
// If given Registry is nil, then the Pack method obtains
// registry from DB
func (c *Container) Pack(
	r *registry.Root,
	reg *registry.Registry,
) (
	p *Pack,
	err error,
) {

	if reg == nil {
		if reg, err = c.Registry(r.Reg); err != nil {
			return
		}
	}

	p = c.getPack(reg)
	return
}
