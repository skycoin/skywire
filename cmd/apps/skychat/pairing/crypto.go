// Package pairing — cmd/apps/skychat/pairing/crypto.go: end-to-end
// body encryption for chat-pair feeds.
//
// Threat model: the pair feed lives at a publicly-known DMSG port
// under the publisher's PK, and any peer who learns the publisher
// PK + port can attempt to subscribe. The publisher-side allowlist
// (CXO TreeStore SubscriberAllowlist, added in #2378) gates read
// access to the feed at the connection layer. This file adds a
// belt-and-suspenders layer at the message-content layer:
//
//   - Each Pair derives a 32-byte symmetric key from
//     ECDH(my_sk, peer_pk). Both sides compute the same key from
//     ECDH(my_pk, peer_sk) and ECDH(peer_pk, my_sk) — that's the
//     symmetry property of ECDH.
//   - Every leaf published into the pair feed is sealed with
//     ChaCha20-Poly1305 AEAD, with a fresh random 12-byte nonce
//     prepended to the ciphertext: [nonce | ct+tag].
//
// What is encrypted:
//   - The Message JSON body. The leaf bytes stored in CXO are
//     ciphertext; subscribers decrypt before json.Unmarshal.
//
// What is NOT encrypted:
//   - The path: msgs/<unix-nanos>/<seq>. Timestamp + ordering are
//     metadata; an observer who already breached the allowlist would
//     learn the timing pattern of messages but not their content.
//
// secp256k1 + ChaCha20-Poly1305: the skywire identity uses
// secp256k1 keys, not Curve25519, so we can't use NaCl box directly.
// skycoin/cipher.ECDH does the secp256k1 scalar-mult and SHA256s the
// raw shared point — that yields a 32-byte key suitable as the
// ChaCha20-Poly1305 key directly. No HKDF needed because we have a
// single fixed use (this pair) per derived key.
package pairing

import (
	"crypto/rand"
	"errors"
	"fmt"

	skycipher "github.com/skycoin/skycoin/src/cipher"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/skycoin/skywire/pkg/cipher"
)

// pairKey is the symmetric key used for one chat pair.
type pairKey []byte

// derivePairKey returns the ECDH-derived symmetric key for the
// (mySK, peerPK) pair. The result is identical when computed by the
// peer with their own SK and our PK.
func derivePairKey(mySK cipher.SecKey, peerPK cipher.PubKey) (pairKey, error) {
	key, err := skycipher.ECDH(skycipher.PubKey(peerPK), skycipher.SecKey(mySK))
	if err != nil {
		return nil, fmt.Errorf("pairing: derive ECDH key: %w", err)
	}
	if len(key) != chacha20poly1305.KeySize {
		// skycoin/cipher.ECDH returns SHA256 → 32 bytes; this branch
		// only triggers if the upstream changed the digest length.
		return nil, fmt.Errorf("pairing: ECDH key length %d != %d",
			len(key), chacha20poly1305.KeySize)
	}
	return key, nil
}

// sealMessage AEAD-encrypts plaintext under key, prepending a fresh
// random nonce. Output layout: [12-byte nonce | ChaCha20-Poly1305
// ciphertext + 16-byte tag].
func sealMessage(key pairKey, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("pairing: aead init: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("pairing: nonce: %w", err)
	}
	// Allocate output as [nonce | ciphertext+tag] in one slice so
	// the caller can publish it directly without further copies.
	out := make([]byte, len(nonce), len(nonce)+len(plaintext)+aead.Overhead())
	copy(out, nonce)
	out = aead.Seal(out, nonce, plaintext, nil)
	return out, nil
}

// errCipherShort is returned when an inbound leaf is shorter than
// the nonce + tag overhead. Indicates either a malformed message or
// a plaintext leaf from a pre-encryption peer (which we no longer
// accept).
var errCipherShort = errors.New("pairing: ciphertext too short")

// openMessage AEAD-decrypts a [nonce | ct+tag] blob produced by
// sealMessage. Returns an error on tag mismatch (tampered ciphertext
// or wrong key) so callers can drop the leaf rather than surface
// garbage to the user.
func openMessage(key pairKey, sealed []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("pairing: aead init: %w", err)
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, errCipherShort
	}
	nonce := sealed[:aead.NonceSize()]
	ct := sealed[aead.NonceSize():]
	pt, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("pairing: open: %w", err)
	}
	return pt, nil
}
