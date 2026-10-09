// Package transport pkg/transport/signed_list.go c2-net-transport
package transport

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"

	"github.com/skycoin/skywire/pkg/cipher"
)

// ErrSignedListSig is returned when a transport list's signature does not
// match its owner's key.
var ErrSignedListSig = errors.New("transport list: signature does not match its owner")

// SignedList is one visor's own transport list, signed by that visor. A
// neighbor can cache and pass it on, and a reader can still check it.
type SignedList struct {
	PK      cipher.PubKey  `json:"pk"`
	At      int64          `json:"at"` // unix seconds when it was built
	Entries []CompactEntry `json:"entries"`
	Sig     cipher.Sig     `json:"sig"`
}

// NewSignedList builds and signs pk's list from its transport entries.
func NewSignedList(pk cipher.PubKey, sk cipher.SecKey, at int64, entries []*Entry) (*SignedList, error) {
	l := &SignedList{PK: pk, At: at, Entries: make([]CompactEntry, 0, len(entries))}
	for _, e := range entries {
		if e != nil {
			l.Entries = append(l.Entries, e.ToCompact(pk))
		}
	}
	sort.Slice(l.Entries, func(i, j int) bool {
		a, b := l.Entries[i], l.Entries[j]
		if a.Remote != b.Remote {
			return bytes.Compare(a.Remote[:], b.Remote[:]) < 0
		}
		return a.Type < b.Type
	})
	sig, err := cipher.SignPayload(l.hash(true), sk)
	if err != nil {
		return nil, err
	}
	l.Sig = sig
	return l, nil
}

// hash covers the owner, the entries in order and, with withAt, the build time.
func (l *SignedList) hash(withAt bool) []byte {
	buf := make([]byte, 0, 33+8+len(l.Entries)*48)
	buf = append(buf, l.PK[:]...)
	if withAt {
		buf = binary.BigEndian.AppendUint64(buf, uint64(l.At)) //nolint:gosec // unix seconds
	}
	for _, e := range l.Entries {
		buf = append(buf, e.Remote[:]...)
		buf = binary.AppendUvarint(buf, uint64(len(e.Type)))
		buf = append(buf, e.Type...)
		buf = binary.AppendUvarint(buf, uint64(len(e.Label)))
		buf = append(buf, e.Label...)
	}
	h := cipher.SumSHA256(buf)
	return h[:]
}

// Verify checks the signature against the list's own PK.
func (l *SignedList) Verify() error {
	if err := cipher.VerifyPubKeySignedPayload(l.PK, l.Sig, l.hash(true)); err != nil {
		return ErrSignedListSig
	}
	return nil
}

// Version names the transport set. It changes only when the set does, so a
// reader holding the same version has nothing to fetch.
func (l *SignedList) Version() string { return hex.EncodeToString(l.hash(false)[:16]) }

// Transports reconstructs the full entries.
func (l *SignedList) Transports() []*Entry {
	out := make([]*Entry, 0, len(l.Entries))
	for _, ce := range l.Entries {
		out = append(out, EntryFromCompact(l.PK, ce))
	}
	return out
}
