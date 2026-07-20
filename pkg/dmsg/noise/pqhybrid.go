// Package noise pkg/dmsg/noise/pqhybrid.go c1-net-dmsg
//
// Self-contained post-quantum hybrid key-agreement layer (ML-KEM-768) that
// composes with the classical Noise_KK handshake. See
// docs/design/pq-hybrid-noise-handshake.md.
//
// This file is JUST the crypto: ephemeral ML-KEM keygen / encapsulate /
// decapsulate, and the HKDF combiner that mixes the classical Noise session key
// with the ML-KEM shared secret. It deliberately does NOT touch the live
// handshake — wiring the payloads + capability negotiation into the handshake is
// a separate, flag-gated change so this layer can be reviewed and tested in
// isolation. Everything here is Go stdlib (crypto/mlkem + crypto/hkdf), no new
// dependency.
package noise

import (
	"crypto/hkdf"
	"crypto/mlkem"
	"crypto/sha256"
	"fmt"
)

const (
	// pqSharedKeySize is the ML-KEM-768 shared-secret length (bytes).
	pqSharedKeySize = mlkem.SharedKeySize // 32
	// pqPublicKeySize is the wire size of the ML-KEM-768 encapsulation key
	// sent in handshake message 1's payload.
	pqPublicKeySize = mlkem.EncapsulationKeySize768 // 1184
	// pqCiphertextSize is the wire size of the ML-KEM-768 ciphertext sent in
	// handshake message 2's payload.
	pqCiphertextSize = mlkem.CiphertextSize768 // 1088
	// pqHybridInfo domain-separates the hybrid KDF so its output can never
	// collide with any other use of the same inputs.
	pqHybridInfo = "skywire-pq-hybrid-v1"
)

// pqInitiator holds an initiator's ephemeral ML-KEM-768 decapsulation key and
// the serialized public (encapsulation) key to send to the responder. A fresh
// instance per handshake gives the PQ leg forward secrecy, matching the
// ephemeral X25519/secp256k1 `e` in Noise.
type pqInitiator struct {
	dk        *mlkem.DecapsulationKey768
	PublicKey []byte // wire: handshake msg-1 payload (pqPublicKeySize bytes)
}

// newPQInitiator generates a fresh ephemeral ML-KEM-768 keypair.
func newPQInitiator() (*pqInitiator, error) {
	dk, err := mlkem.GenerateKey768()
	if err != nil {
		return nil, fmt.Errorf("pq: ml-kem keygen: %w", err)
	}
	return &pqInitiator{dk: dk, PublicKey: dk.EncapsulationKey().Bytes()}, nil
}

// pqEncapsulate is the responder side: given the initiator's ML-KEM public key
// (from msg-1's payload), produce the ciphertext to return in msg-2's payload
// and the PQ shared secret to mix in.
func pqEncapsulate(initiatorPublicKey []byte) (ciphertext, sharedSecret []byte, err error) {
	if len(initiatorPublicKey) != pqPublicKeySize {
		return nil, nil, fmt.Errorf("pq: bad ml-kem public key size %d (want %d)", len(initiatorPublicKey), pqPublicKeySize)
	}
	ek, err := mlkem.NewEncapsulationKey768(initiatorPublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("pq: parse peer ml-kem key: %w", err)
	}
	sharedSecret, ciphertext = ek.Encapsulate()
	return ciphertext, sharedSecret, nil
}

// decapsulate is the initiator side: recover the PQ shared secret from the
// responder's ciphertext (msg-2's payload). ML-KEM-768 uses implicit rejection,
// so a tampered (but correctly-sized) ciphertext yields a *different* secret
// rather than an error — the handshake then fails at the AEAD tag, which is the
// intended IND-CCA2 behavior. A wrong-sized ciphertext is rejected outright.
func (k *pqInitiator) decapsulate(ciphertext []byte) (sharedSecret []byte, err error) {
	if len(ciphertext) != pqCiphertextSize {
		return nil, fmt.Errorf("pq: bad ml-kem ciphertext size %d (want %d)", len(ciphertext), pqCiphertextSize)
	}
	sharedSecret, err = k.dk.Decapsulate(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("pq: ml-kem decapsulate: %w", err)
	}
	return sharedSecret, nil
}

// combineHybridKey derives the final transport key by mixing the classical
// Noise session key with the ML-KEM shared secret, bound to the handshake
// transcript hash (so the derived key is tied to *this* handshake). Breaking the
// result requires breaking BOTH the classical DH AND ML-KEM-768 — hybrid
// security. Returns keyLen bytes.
func combineHybridKey(classicalKey, pqSharedSecret, transcriptHash []byte, keyLen int) ([]byte, error) { //nolint:unparam // keyLen is the HKDF output length, kept explicit though currently always the 32-byte cipher key
	if len(pqSharedSecret) != pqSharedKeySize {
		return nil, fmt.Errorf("pq: unexpected ml-kem shared-secret size %d (want %d)", len(pqSharedSecret), pqSharedKeySize)
	}
	if len(classicalKey) == 0 {
		return nil, fmt.Errorf("pq: empty classical key")
	}
	// IKM = classical || pq. Both must contribute; concatenation under HKDF's
	// extract step binds them so neither alone determines the output.
	ikm := make([]byte, 0, len(classicalKey)+len(pqSharedSecret))
	ikm = append(ikm, classicalKey...)
	ikm = append(ikm, pqSharedSecret...)
	out, err := hkdf.Key(sha256.New, ikm, transcriptHash, pqHybridInfo, keyLen)
	if err != nil {
		return nil, fmt.Errorf("pq: hkdf: %w", err)
	}
	return out, nil
}
