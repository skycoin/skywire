package registry

import (
	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

// Flags of unpacking
type Flags int

// common Flags
const (

	// HashTableIndex is flag that enables hash-table index inside
	// the Refs. The index speeds up accessing by hash. The index
	// is not designed to be used with many elements with the same
	// hash. Because, the big O of some internal operations that
	// uses the index is O(m), where m is number of elements with
	// the same index. Be careful, your Refs can have many elements
	// with blank hash, that will be indexed as allways. Thus, the
	// index can be used with one or few elements per hash for the
	// Refs. The flag will be stored inised the Refs after first
	// access. For example
	//
	//     pack.SetFlag(registry.HashTableIndex)
	//     if _, err := someRefs.Len(pack); err != nil {
	//         // something wrong
	//     }
	//
	//     pack.UnsetFlag(registry.HashTableIndex)
	//     if _, err := someOtherRefs.Len(pack); err != nil {
	//         // something wrong
	//     }
	//
	// After the Len() (or any other call), the someRefs stores
	// flags of the pack inside. And you can unset the flag if
	// you want. Thus, the someRefs uses has-table index, but
	// the someOtherRefs does not
	HashTableIndex Flags = 1 << iota
	// EntireRefs is flag that forces the Refs to be unpacked
	// entirely. By default (without the flag), the Refs uses
	// lazy loading, where all branches loads only if it's
	// necessary. The Refs stores this flag inside like the
	// HashTableIndex flag (see above)
	EntireRefs
	// LazyUpdating flag turns the Refs to don't update every
	// branch every changes. Every branch of the Refs has its
	// own hash and length. And every changes in subtree
	// bobble the chagnes up to the Refs (to the root of the
	// Refs tree). The LazyUpdatingRefs turns of the updating
	// and the updating performed inside Rebuild method of
	// the Refs (e.g. the updating will be perfromed anyway
	// even if a developer doesn't it explicitly).
	// So, if you want Hash field of the Refs to be actual
	// then you need to turn this flag off (just don't set it)
	// or call the Rebuild method to get real value of the
	// field. If you are using this flag, then you have to call
	// (*Refs).Rebuild() after to update hash of the Refs.
	LazyUpdating

	//  /\
	// /||\
	//  ||
	// (developer note) it doesn't update hash field, because
	// it requirs encoding and SHA256 calculating, but it updates
	// length field

)

// A Degree represents degree of the Refs. The Degree represented
// as int, but in database it saved as uint32, thus Degree can't be
// less the 2 and bigger then max possible int32. The Degree is
// configurable and it configured by skyobject package. See
// skyobject.Config for details and skyobject.MerkleDegree constant
type Degree int

// MaxDegree is limit of degree. The Degree should have
// some limit to protect nodes from very-big-degree
// attacks. E.g. a node with bi amount of RAM can generate
// Refs with very big degree and publish it. This way,
// nodes that receive this Refs can drain theirs RAM
const MaxDegree Degree = 1024

// Developer note: the MaxDegree must not be greater than max int32
//
// const (
//	maxUint32 = ^uint32(0)
//	maxInt32  = int32(maxUint32 >> 1)
// )

// Validate the Degree
func (d Degree) Validate() (err error) {
	if d < 2 || d > MaxDegree {
		err = ErrInvalidDegree
	}
	return
}

// A Pack represents ...
type Pack interface {
	Registry() *Registry // related registry

	Get(key cipher.SHA256) (val []byte, err error) // get value by key
	Set(key cipher.SHA256, val []byte) (err error) // set k-v pair
	Add(val []byte) (key cipher.SHA256, err error) // set+calculate hash

	// A Refs will panic if the Pack returns invalid Degree

	Degree() Degree                      // default degree for new Refs
	SetDegree(degree Degree) (err error) // chagne the default degree

	Flags() Flags     // flags of the Pack
	AddFlags(Flags)   // add given Flags to internal (OR)
	ClearFlags(Flags) // clear given Flags from internal (AND NOT)
}

// get by hash from the Pack and deocde to given pointer (obj)
func get(
	pack Pack, //          : pack to get from
	hash cipher.SHA256, // : hash of the object
	obj interface{}, //    : pointer to object
) (
	err error, //          : getting or decoding error
) {

	var val []byte

	if val, err = pack.Get(hash); err != nil {
		return
	}

	_, err = encoder.DeserializeRaw(val, obj)

	return
}
