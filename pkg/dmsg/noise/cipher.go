// Package noise pkg/dmsg/noise/cipher.go c1-net-dmsg
package noise

import (
	"crypto/cipher"
	"encoding/binary"
	"sync"

	"github.com/flynn/noise"
	"golang.org/x/crypto/chacha20poly1305"
)

// cipherChaChaPoly is noise.CipherChaChaPoly with pooled nonces. flynn/noise
// builds every nonce on the heap, which is two allocations per frame.
var cipherChaChaPoly noise.CipherFunc = chachaPolyFunc{}

type chachaPolyFunc struct{}

func (chachaPolyFunc) Cipher(k [32]byte) noise.Cipher {
	c, err := chacha20poly1305.New(k[:])
	if err != nil {
		panic(err)
	}
	return chachaPoly{c}
}

func (chachaPolyFunc) CipherName() string { return noise.CipherChaChaPoly.CipherName() }

type chachaPoly struct{ aead cipher.AEAD }

// Only bytes 4 to 11 are ever written, so pooled nonces keep their zero prefix.
var noncePool = sync.Pool{New: func() any { return new([chacha20poly1305.NonceSize]byte) }}

func (c chachaPoly) Encrypt(out []byte, n uint64, ad, plaintext []byte) []byte {
	nonce := noncePool.Get().(*[chacha20poly1305.NonceSize]byte)
	binary.LittleEndian.PutUint64(nonce[4:], n)
	out = c.aead.Seal(out, nonce[:], plaintext, ad)
	noncePool.Put(nonce)
	return out
}

func (c chachaPoly) Decrypt(out []byte, n uint64, ad, ciphertext []byte) ([]byte, error) {
	nonce := noncePool.Get().(*[chacha20poly1305.NonceSize]byte)
	binary.LittleEndian.PutUint64(nonce[4:], n)
	out, err := c.aead.Open(out, nonce[:], ciphertext, ad)
	noncePool.Put(nonce)
	return out, err
}
