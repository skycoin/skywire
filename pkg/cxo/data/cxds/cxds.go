// Package cxds implements cxo/data.CXDS
// interface. E.g. the package provides data
// store for CXO. The data store is key value
// store, in which the key is SHA256 hash of
// value. And every value has references
// counter (rc). The counter set outside the
// CXDS. The rc is number references to an
// object. The CXDS never remove objects
// with zero-rc.
package cxds

import (
	"encoding/binary"
	"errors"

	"github.com/skycoin/skycoin/src/cipher"
)

// Version of the CXDS API and data representation
const Version int = 2 // previous is 1

// comon errors
var (
	ErrEmptyValue = errors.New("empty value")

	ErrWrongValueLength = errors.New("wrong value length")

	ErrMissingMetaInfo = errors.New("missing meta information")

	ErrMissingVersion = errors.New("missing version in meta")
	ErrOldVersion     = errors.New("db file of old version")      // cxodbfix
	ErrNewVersion     = errors.New("db file newer then this CXO") // go get
)

func getRefsCount(val []byte) (rc uint32) {
	rc = binary.BigEndian.Uint32(val)
	return
}

func setRefsCount(val []byte, rc uint32) {
	binary.BigEndian.PutUint32(val, rc)
	return //nolint:staticcheck
}

func getHash(val []byte) (key cipher.SHA256) { //nolint:unused
	return cipher.SumSHA256(val)
}

// version

func versionBytes() []byte {
	return encodeUint32(uint32(Version))
}

func encodeUint32(u uint32) (ub []byte) { //nolint:unparam
	ub = make([]byte, 4)
	binary.BigEndian.PutUint32(ub, uint32(Version))
	return
}

func decodeUint32(ub []byte) uint32 {
	return binary.BigEndian.Uint32(ub)
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
