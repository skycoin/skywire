package data

import (
	"github.com/skycoin/skycoin/src/cipher"
)

// An IterateFeedsFunc represents function for
// iterating over all feeds IdxDB contains
type IterateFeedsFunc func(cipher.PubKey) error

// A Feeds represents bucket of feeds
type Feeds interface {
	// Add feed. Adding a feed twice or
	// more times does nothing
	Add(pk cipher.PubKey) (err error)
	// Del feed with all heads and Root objects
	// unconditionally. If feed doesn't exist
	// then the Del returns ErrNoSuchFeed
	Del(pk cipher.PubKey) (err error)

	// Iterate all feeds. Use ErrStopRange to break
	// it iteration. The Iterate passes any error
	// returned from given function through. Except
	// ErrStopIteration that turns nil. It's possible
	// to mutate the IdxDB inside the Iterate
	Iterate(iterateFunc IterateFeedsFunc) (err error)
	// Has returns true if the IdxDB contains
	// feed with given public key
	Has(pk cipher.PubKey) (ok bool, err error)

	// Heads of feed. It returns ErrNoSuchFeed
	// if given feed doesn't exist
	Heads(pk cipher.PubKey) (hs Heads, err error)

	// Len is number of feeds stroed
	Len() (length int)
}

// An IterateHeadsFunc used to iterate over
// heads of a feed
type IterateHeadsFunc func(nonce uint64) (err error)

// A Heads represents all heads of a feed
type Heads interface {
	// Roots of head with given nonce. If given
	// head doesn't exists then, this method
	// returns ErrNoSuchHead
	Roots(nonce uint64) (rs Roots, err error)
	// Add new head with given nonce.
	// If a head with given nonce already
	// exists, then this method does nothing
	Add(nonce uint64) (rs Roots, err error)
	// Del deletes head with given nonce and
	// all its Root objects. The method returns
	// ErrNoSuchHead if a head with given nonce
	// doesn't exist
	Del(nonce uint64) (err error)
	// Has returns true if a head with given
	// nonce exits in the CXDS
	Has(nonce uint64) (ok bool, err error)
	// Iterate over all heads
	Iterate(iterateFunc IterateHeadsFunc) (err error)

	// Len is number of heads stored
	Len() (length int)
}

// An IterateRootsFunc represents function for
// iterating over all Root objects of a feed
type IterateRootsFunc func(r *Root) (err error)

// A Roots represents bucket of Root objects.
// All Root objects ordered by seq number
// from small to big
type Roots interface {
	// Ascend iterates all Root object ascending order.
	// Use ErrStopIteration to stop iteration. Any error
	// (except the ErrStopIteration) returned by given
	// IterateRootsFunc will be passed through. The
	// Ascend doesn't update access time
	Ascend(iterateFunc IterateRootsFunc) (err error)
	// Descend is the same as the Ascend, but it iterates
	// decending order. Use ErrStopIteration to stop
	// iteration. The Descend doesn't update access time
	Descend(iterateFunc IterateRootsFunc) (err error)

	// Set adds new Root object to the DB. If an
	// object already exists, then the Set touch
	// it updating acess time. In this case, the
	// set changes Access field of the given Root.
	// If Root doesn't exist, then the Set sets
	// Create field too. Thus, the method modifies
	// given Root in any case. E.g. if Root exists
	// then fields Create and Access of given Root
	// will be changed to saved. But Access field
	// of saved Root will be changed to now
	Set(r *Root) (err error)

	// Del Root by seq number. The Del never returns
	// ErrNotFound if Root doesn't exist
	Del(seq uint64) (err error)

	// Get Root by seq number
	Get(seq uint64) (r *Root, err error)

	// Has the Roots Root with given seq?
	Has(seq uint64) (ok bool, err error)

	// Len is number of Root objects stored
	Len() (length int)
}

// An IdxDB repesents database that contains
// meta information: feeds meta information
// about Root objects. There is data/idxdb
// package that implements the IdxDB. The
// IdxDB returns and uses errors ErrNotFound,
// ErrNoSuchFeed, ErrNoSuchHead, and
// ErrStopIteration, and from this package.
type IdxDB interface {
	Tx(func(Feeds) error) error // transaction
	Close() error               // close the IdxDB
}
