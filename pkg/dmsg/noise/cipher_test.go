package noise

import (
	"bytes"
	"math"
	"testing"

	"github.com/flynn/noise"
)

func TestCipherChaChaPolyMatchesFlynn(t *testing.T) {
	var k [32]byte
	for i := range k {
		k[i] = byte(i)
	}
	ours, theirs := cipherChaChaPoly.Cipher(k), noise.CipherChaChaPoly.Cipher(k)
	if cipherChaChaPoly.CipherName() != noise.CipherChaChaPoly.CipherName() {
		t.Fatalf("name %q, want %q", cipherChaChaPoly.CipherName(), noise.CipherChaChaPoly.CipherName())
	}
	ad, msg := []byte("ad"), []byte("frame payload")
	for _, n := range []uint64{0, 1, 1 << 32, math.MaxUint64} {
		a := ours.Encrypt(nil, n, ad, msg)
		if b := theirs.Encrypt(nil, n, ad, msg); !bytes.Equal(a, b) {
			t.Fatalf("nonce %d: ciphertext differs", n)
		}
		for _, c := range []noise.Cipher{ours, theirs} {
			p, err := c.Decrypt(nil, n, ad, a)
			if err != nil || !bytes.Equal(p, msg) {
				t.Fatalf("nonce %d: decrypt %q, %v", n, p, err)
			}
		}
		if _, err := ours.Decrypt(nil, n+1, ad, a); err == nil {
			t.Fatalf("nonce %d: opened under the wrong nonce", n)
		}
	}
}
