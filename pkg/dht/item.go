package dht

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// MaxValueSize is the maximum size of a mutable item value (BEP44 limit).
const MaxValueSize = 1000

// MaxSaltSize is the maximum size of a salt for namespacing.
const MaxSaltSize = 200

// Errors for mutable items.
var (
	ErrValueTooLarge   = errors.New("dht: value exceeds max size")
	ErrSaltTooLarge    = errors.New("dht: salt exceeds max size")
	ErrInvalidSig      = errors.New("dht: invalid signature")
	ErrSeqNotMonotonic = errors.New("dht: sequence number must increase")
)

// MutableItem is a signed, updateable record stored in the DHT.
// It follows BEP44 semantics with secp256k1 keys instead of ed25519.
type MutableItem struct {
	K    cipher.PubKey `json:"k"`              // publisher's secp256k1 pubkey
	Seq  uint64        `json:"seq"`            // monotonically increasing sequence number
	Sig  cipher.Sig    `json:"sig"`            // secp256k1 signature over signPayload
	V    []byte        `json:"v"`              // value (max 1000 bytes)
	Salt []byte        `json:"salt,omitempty"` // optional namespace (max 200 bytes)
}

// Target returns the DHT key where this item should be stored:
// SHA256(pubkey || salt). This determines which nodes are responsible
// for the item via XOR distance.
func (item MutableItem) Target() NodeID {
	h := sha256.New()
	h.Write(item.K[:])
	h.Write(item.Salt)
	var id NodeID
	copy(id[:], h.Sum(nil))
	return id
}

// signPayload returns the canonical bytes that are signed/verified:
// seq (8 bytes BE) || len(V) (4 bytes BE) || V || len(Salt) (4 bytes BE) || Salt
// Length prefixes prevent ambiguity between different V/Salt splits
// that would otherwise produce identical payloads.
func (item *MutableItem) signPayload() []byte {
	buf := make([]byte, 8+4+len(item.V)+4+len(item.Salt))
	binary.BigEndian.PutUint64(buf[:8], item.Seq)
	binary.BigEndian.PutUint32(buf[8:12], uint32(len(item.V))) //nolint:gosec
	copy(buf[12:], item.V)
	off := 12 + len(item.V)
	binary.BigEndian.PutUint32(buf[off:off+4], uint32(len(item.Salt))) //nolint:gosec
	copy(buf[off+4:], item.Salt)
	return buf
}

// Sign signs the item with the given secret key.
func (item *MutableItem) Sign(sk cipher.SecKey) error {
	sig, err := cipher.SignPayload(item.signPayload(), sk)
	if err != nil {
		return fmt.Errorf("dht: sign failed: %w", err)
	}
	item.Sig = sig
	return nil
}

// Verify checks that the signature is valid for the item's pubkey, seq, value, and salt.
func (item *MutableItem) Verify() error {
	if len(item.V) > MaxValueSize {
		return ErrValueTooLarge
	}
	if len(item.Salt) > MaxSaltSize {
		return ErrSaltTooLarge
	}
	if err := cipher.VerifyPubKeySignedPayload(item.K, item.Sig, item.signPayload()); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSig, err)
	}
	return nil
}
