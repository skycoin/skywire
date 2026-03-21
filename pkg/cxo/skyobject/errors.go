package skyobject

import (
	"errors"

	"github.com/skycoin/skycoin/src/cipher"
)

// common errors
var (
	ErrObjectIsTooLarge = errors.New("object is too large (see MaxObjectSize)")
	ErrTerminated       = errors.New("terminated")
	ErrBlankRegistryRef = errors.New("blank registry reference")
)

// ObjectIsTooLargeError represents error that
// occurs when an object exceed max object size
// limit. The error contains hash of the object
type ObjectIsTooLargeError struct {
	hash cipher.SHA256
}

// Hash of the large object
func (o *ObjectIsTooLargeError) Hash() cipher.SHA256 {
	return o.hash
}

// Error implements error interface
func (o *ObjectIsTooLargeError) Error() string {
	return "object is too large: " + o.Hash().Hex()[:7]
}
