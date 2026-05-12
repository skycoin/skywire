// Package group — cmd/apps/skychat/group/crypto.go: AES-256-GCM
// envelope helpers for ModePrivate groups.
//
// Why GCM (not ChaCha20-Poly1305 or noise): every visor binary
// already pulls in crypto/aes + crypto/cipher for other code paths,
// keeping the surface area unchanged. GCM also gives us AEAD with
// 16-byte tag which is exactly what we want for per-message
// authentication on the feed.
//
// Nonce policy: 12-byte random per message. With a single AES key
// shared across an arbitrary number of senders, that's the
// "single-write key" failure mode AEADs warn about — but each
// message's nonce is independently random, so the probability of
// nonce reuse across the group is 1 - exp(-N²/2¹⁹⁷), i.e.
// negligible at any realistic message volume.
package group

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// Encrypt produces (ciphertext, nonce) for a ModePrivate group.
// Caller is responsible for tagging the resulting Message body with
// Ciphertext + Nonce. Returns an error iff the key length is wrong
// or the underlying RNG fails.
func Encrypt(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	if len(key) != 32 {
		return nil, nil, fmt.Errorf("group crypto: AES key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("group crypto: aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("group crypto: cipher.NewGCM: %w", err)
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("group crypto: nonce rand: %w", err)
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Decrypt is the inverse of Encrypt. Failures (wrong key, mangled
// ciphertext, mangled nonce, tag check) all surface as errors —
// callers should display "[decryption failed]" or similar on the
// feed line rather than the raw error to avoid leaking which kind
// of failure occurred to a passive observer.
func Decrypt(key, ciphertext, nonce []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("group crypto: AES key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("group crypto: aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("group crypto: cipher.NewGCM: %w", err)
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("group crypto: nonce length: got %d want %d", len(nonce), gcm.NonceSize())
	}
	pt, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("group crypto: gcm.Open: %w", err)
	}
	return pt, nil
}
