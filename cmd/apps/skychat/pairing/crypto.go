// Package pairing cmd/apps/skychat/pairing/crypto.go c4-app-chat
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
//
// # The static key is now the FALLBACK, not the norm
//
// derivePairKey's output is a pure function of two long-term identity
// keys, which means it never changes and can be recomputed forever — so
// leaking a visor's identity secret retroactively opens every DM it ever
// exchanged. epoch.go / ratchet.go replace it with short-lived epoch keys
// whose secret halves are destroyed on rotation.
//
// The static key survives for exactly two jobs, both compatibility:
//
//   - Messages already on a feed, published before this visor (or the
//     peer) had a ratchet. Those leaves stay in the CXO tree forever and
//     must keep opening.
//   - A peer running a build without ratchet support, which never
//     announces and never sends an epoch envelope.
//
// sealEnvelope tags every new message with the epoch it used, and
// openEnvelope routes a tagged blob to the ring and an untagged one to
// the static key. A pair falls back only when it genuinely has no epoch;
// it never downgrades once one exists.
package pairing

import (
	"crypto/rand"
	"encoding/json"
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

// envelopeMagic prefixes every epoch-sealed leaf.
//
// The pre-epoch leaf format is a bare [nonce | ct+tag] blob whose first
// bytes are a fresh random nonce, so there is no version field to branch
// on and no way to add one without breaking the leaves already published.
// A four-byte magic solves that from the outside: a legacy blob starts
// with these exact bytes only 1 in 2^32 times, and even then the JSON
// decode behind it fails and the reader falls back — so the worst case is
// a dropped leaf at a rate far below the network's own loss.
var envelopeMagic = [4]byte{'S', 'K', 'P', '1'}

// messageEnvelope is the epoch-tagged wire form of a sealed message,
// written as envelopeMagic followed by this JSON.
//
// The epoch ID is in the CLEAR, and it has to be: the receiver uses it to
// pick a key, and it cannot decrypt anything to find out which. What it
// leaks is which conversation-epoch a leaf belongs to — i.e. a coarse
// grouping of messages already grouped by being on the same feed under
// the same publisher. It carries no information about the key, since the
// ID is a hash of two public keys.
type messageEnvelope struct {
	// Epoch identifies the (my ratchet, peer ratchet) pair whose key
	// sealed Body.
	Epoch string `json:"e"`

	// Body is [12-byte nonce | ChaCha20-Poly1305 ct+tag], the same
	// layout sealMessage has always produced.
	Body []byte `json:"b"`
}

// sealEnvelope seals plaintext under an epoch key and tags the result
// with that epoch's ID.
func sealEnvelope(id EpochID, key pairKey, plaintext []byte) ([]byte, error) {
	body, err := sealMessage(key, plaintext)
	if err != nil {
		return nil, err
	}
	blob, err := json.Marshal(messageEnvelope{Epoch: id.String(), Body: body})
	if err != nil {
		return nil, fmt.Errorf("pairing: envelope: marshal: %w", err)
	}
	out := make([]byte, 0, len(envelopeMagic)+len(blob))
	out = append(out, envelopeMagic[:]...)
	return append(out, blob...), nil
}

// parseEnvelope splits a leaf into its epoch tag and sealed body.
// Returns ok=false for a legacy (untagged) leaf, which the caller opens
// with the static pair key instead.
func parseEnvelope(leaf []byte) (EpochID, []byte, bool) {
	if len(leaf) < len(envelopeMagic) || [4]byte(leaf[:4]) != envelopeMagic {
		return EpochID{}, nil, false
	}
	var env messageEnvelope
	if err := json.Unmarshal(leaf[len(envelopeMagic):], &env); err != nil {
		return EpochID{}, nil, false
	}
	id, err := parseEpochID(env.Epoch)
	if err != nil {
		return EpochID{}, nil, false
	}
	return id, env.Body, true
}

// parseEpochID decodes the hex form EpochID.String produces.
func parseEpochID(s string) (EpochID, error) {
	var id EpochID
	if len(s) != EpochIDLen*2 {
		return id, fmt.Errorf("pairing: epoch id %q: want %d hex chars", s, EpochIDLen*2)
	}
	for i := 0; i < EpochIDLen; i++ {
		hi, err := hexNibble(s[i*2])
		if err != nil {
			return EpochID{}, err
		}
		lo, err := hexNibble(s[i*2+1])
		if err != nil {
			return EpochID{}, err
		}
		id[i] = hi<<4 | lo
	}
	return id, nil
}

func hexNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, fmt.Errorf("pairing: epoch id: invalid hex byte %q", string(c))
}
