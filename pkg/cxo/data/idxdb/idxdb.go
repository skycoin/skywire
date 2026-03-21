package idxdb

import (
	"encoding/binary"
	"errors"
)

// Version of the IdxDB API and data representation
const Version = 2 // previous is 0

// common errors
var (
	ErrInvalidSize = errors.New("invalid size of encoded object")

	ErrMissingMetaInfo = errors.New("missing meta information")
	ErrMissingVersion  = errors.New("missing version in meta")
	ErrOldVersion      = errors.New("db file of old version")      // cxodbfix
	ErrNewVersion      = errors.New("db file newer then this CXO") // go get
)

// version

func versionBytes() (vb []byte) {
	vb = make([]byte, 4)
	binary.BigEndian.PutUint32(vb, uint32(Version))
	return
}
