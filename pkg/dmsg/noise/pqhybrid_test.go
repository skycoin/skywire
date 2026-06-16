package noise

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPQHybridAgreement is the end-to-end happy path: initiator and responder
// independently derive the same ML-KEM shared secret, and feeding it through the
// combiner with a shared classical key + transcript yields the same final key on
// both ends.
func TestPQHybridAgreement(t *testing.T) {
	ini, err := newPQInitiator()
	require.NoError(t, err)

	ct, ssResp, err := pqEncapsulate(ini.PublicKey)
	require.NoError(t, err)

	ssInit, err := ini.decapsulate(ct)
	require.NoError(t, err)

	require.Equal(t, ssResp, ssInit, "ML-KEM shared secret must agree on both ends")

	classical := make([]byte, 32)
	_, _ = rand.Read(classical)
	transcript := make([]byte, 32)
	_, _ = rand.Read(transcript)

	kInit, err := combineHybridKey(classical, ssInit, transcript, 32)
	require.NoError(t, err)
	kResp, err := combineHybridKey(classical, ssResp, transcript, 32)
	require.NoError(t, err)
	require.Equal(t, kInit, kResp, "hybrid final key must agree on both ends")
}

// TestPQWireSizes pins the FIPS-203 ML-KEM-768 wire sizes we advertise.
func TestPQWireSizes(t *testing.T) {
	ini, err := newPQInitiator()
	require.NoError(t, err)
	assert.Len(t, ini.PublicKey, pqPublicKeySize, "encapsulation key must be 1184 B")

	ct, ss, err := pqEncapsulate(ini.PublicKey)
	require.NoError(t, err)
	assert.Len(t, ct, pqCiphertextSize, "ciphertext must be 1088 B")
	assert.Len(t, ss, pqSharedKeySize, "shared secret must be 32 B")
}

// TestCombineMixesBothInputs is the hybrid-security property: the final key must
// change if EITHER the classical key, the PQ secret, or the transcript changes —
// i.e. neither secret alone determines the output.
func TestCombineMixesBothInputs(t *testing.T) {
	classical := bytes.Repeat([]byte{0xA1}, 32)
	pq := bytes.Repeat([]byte{0xB2}, pqSharedKeySize)
	transcript := bytes.Repeat([]byte{0xC3}, 32)

	base, err := combineHybridKey(classical, pq, transcript, 32)
	require.NoError(t, err)

	classical2 := bytes.Repeat([]byte{0xA2}, 32)
	kc, err := combineHybridKey(classical2, pq, transcript, 32)
	require.NoError(t, err)
	assert.NotEqual(t, base, kc, "changing the classical key must change the output")

	pq2 := bytes.Repeat([]byte{0xB3}, pqSharedKeySize)
	kp, err := combineHybridKey(classical, pq2, transcript, 32)
	require.NoError(t, err)
	assert.NotEqual(t, base, kp, "changing the PQ secret must change the output")

	transcript2 := bytes.Repeat([]byte{0xC4}, 32)
	kt, err := combineHybridKey(classical, pq, transcript2, 32)
	require.NoError(t, err)
	assert.NotEqual(t, base, kt, "changing the transcript must change the output")

	// Deterministic for identical inputs.
	again, err := combineHybridKey(classical, pq, transcript, 32)
	require.NoError(t, err)
	assert.Equal(t, base, again, "combiner must be deterministic")
}

// TestPQRejectsBadSizes guards the wire-parse boundaries.
func TestPQRejectsBadSizes(t *testing.T) {
	ini, err := newPQInitiator()
	require.NoError(t, err)

	_, _, err = pqEncapsulate(make([]byte, pqPublicKeySize-1))
	require.Error(t, err, "short public key must be rejected")

	_, err = ini.decapsulate(make([]byte, pqCiphertextSize-1))
	require.Error(t, err, "short ciphertext must be rejected")

	_, err = combineHybridKey(make([]byte, 32), make([]byte, pqSharedKeySize-1), nil, 32)
	require.Error(t, err, "wrong PQ-secret size must be rejected")
}

// TestPQTamperedCiphertextDiverges documents ML-KEM implicit rejection: a
// tampered (correctly-sized) ciphertext decapsulates to a DIFFERENT secret
// rather than erroring, so the hybrid key won't match and the handshake fails at
// the AEAD tag — the intended IND-CCA2 behavior.
func TestPQTamperedCiphertextDiverges(t *testing.T) {
	ini, err := newPQInitiator()
	require.NoError(t, err)
	ct, ssResp, err := pqEncapsulate(ini.PublicKey)
	require.NoError(t, err)

	tampered := bytes.Clone(ct)
	tampered[0] ^= 0xFF
	ssBad, err := ini.decapsulate(tampered)
	require.NoError(t, err, "implicit rejection: still returns a (pseudo-random) secret")
	assert.NotEqual(t, ssResp, ssBad, "tampered ciphertext must not recover the real secret")
}
