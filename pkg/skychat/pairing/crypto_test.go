package pairing

import (
	"bytes"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestDerivePairKeySymmetric(t *testing.T) {
	pkA, skA := cipher.GenerateKeyPair()
	pkB, skB := cipher.GenerateKeyPair()

	keyAB, err := derivePairKey(skA, pkB)
	if err != nil {
		t.Fatalf("A→B derive: %v", err)
	}
	keyBA, err := derivePairKey(skB, pkA)
	if err != nil {
		t.Fatalf("B→A derive: %v", err)
	}
	if !bytes.Equal(keyAB, keyBA) {
		t.Fatal("ECDH key must be symmetric: derive(skA, pkB) == derive(skB, pkA)")
	}
}

func TestDerivePairKeyDifferentPairs(t *testing.T) {
	_, skA := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	pkC, _ := cipher.GenerateKeyPair()

	keyAB, err := derivePairKey(skA, pkB)
	if err != nil {
		t.Fatal(err)
	}
	keyAC, err := derivePairKey(skA, pkC)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(keyAB, keyAC) {
		t.Fatal("different pairs must produce different keys")
	}
}

func TestSealOpenRoundtrip(t *testing.T) {
	pkA, skA := cipher.GenerateKeyPair()
	pkB, skB := cipher.GenerateKeyPair()
	_ = pkA

	keyAB, err := derivePairKey(skA, pkB)
	if err != nil {
		t.Fatal(err)
	}
	keyBA, err := derivePairKey(skB, pkA)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte(`{"text":"hello bob","ts":"2026-04-29T00:00:00Z"}`)

	sealed, err := sealMessage(keyAB, plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("ciphertext must not contain plaintext bytes")
	}

	opened, err := openMessage(keyBA, sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("roundtrip mismatch: got %q want %q", opened, plaintext)
	}
}

func TestOpenWithWrongKeyFails(t *testing.T) {
	pkA, skA := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	_, skC := cipher.GenerateKeyPair()
	_ = pkA

	keyAB, err := derivePairKey(skA, pkB)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := derivePairKey(skC, pkB)
	if err != nil {
		t.Fatal(err)
	}

	sealed, err := sealMessage(keyAB, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openMessage(wrong, sealed); err == nil {
		t.Fatal("opening with wrong key must fail")
	}
}

func TestOpenTamperedFails(t *testing.T) {
	_, skA := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	key, err := derivePairKey(skA, pkB)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealMessage(key, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	// Flip one bit in the ciphertext (after the nonce). AEAD must reject.
	tampered := append([]byte{}, sealed...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := openMessage(key, tampered); err == nil {
		t.Fatal("opening tampered ciphertext must fail")
	}
}

func TestOpenShortInputFails(t *testing.T) {
	_, skA := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	key, err := derivePairKey(skA, pkB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openMessage(key, []byte{1, 2, 3}); err == nil {
		t.Fatal("opening too-short input must fail")
	}
}

func TestSealNonceUnique(t *testing.T) {
	// Sealing the same plaintext twice with the same key must produce
	// different ciphertexts because the nonce is randomized per call.
	_, skA := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	key, err := derivePairKey(skA, pkB)
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("identical message")

	a, err := sealMessage(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sealMessage(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext must differ (nonce reuse would break AEAD security)")
	}
}
