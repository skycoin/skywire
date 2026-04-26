// Package dht implements a Kademlia DHT with BEP44 mutable data semantics.
//
// Node IDs are 256-bit values derived from SHA256(secp256k1_pubkey).
// Distance is XOR. Items are mutable signed records keyed by
// SHA256(pubkey || salt), with monotonic sequence numbers for updates.
package dht

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/skycoin/skywire/pkg/cipher"
)

// IDSize is the byte length of a NodeID (256 bits).
const IDSize = 32

// NodeID is a 256-bit identifier in the DHT address space.
type NodeID [IDSize]byte

// NodeIDFromPubKey derives a NodeID from a secp256k1 public key.
func NodeIDFromPubKey(pk cipher.PubKey) NodeID {
	return NodeID(sha256.Sum256(pk[:]))
}

// String returns the hex representation of the NodeID.
func (id NodeID) String() string {
	return hex.EncodeToString(id[:])
}

// IsZero returns true if the NodeID is all zeros.
func (id NodeID) IsZero() bool {
	return id == NodeID{}
}

// Distance returns the XOR distance between two NodeIDs.
func Distance(a, b NodeID) NodeID {
	var d NodeID
	for i := range d {
		d[i] = a[i] ^ b[i]
	}
	return d
}

// Less returns true if a < b when interpreted as big-endian unsigned integers.
func Less(a, b NodeID) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// CommonPrefixLen returns the number of leading zero bits in XOR(a, b).
// This determines which k-bucket a peer belongs to relative to a given node.
// Range: 0 (maximally distant) to 255 (adjacent in ID space).
// Returns 256 if a == b.
func CommonPrefixLen(a, b NodeID) int {
	d := Distance(a, b)
	for i, b := range d {
		if b == 0 {
			continue
		}
		// Count leading zeros in this byte
		for bit := 7; bit >= 0; bit-- {
			if b&(1<<uint(bit)) != 0 {
				return i*8 + (7 - bit)
			}
		}
	}
	return IDSize * 8
}
