//go:build !no_ci
// +build !no_ci

package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
)

func TestSigVerifier(t *testing.T) {
	t.Run("CachesSuccessfulVerification", func(t *testing.T) {
		sv := NewSigVerifier(5 * time.Minute)
		defer sv.ClearCache()

		// Generate test keypair and payload
		pubkey, seckey := cipher.GenerateKeyPair()
		payload := []byte("test payload")

		// Sign the payload using httpauth
		nonce := httpauth.Nonce(0)
		sig, err := httpauth.Sign(payload, nonce, seckey)
		require.NoError(t, err)

		// First verification - should hit crypto
		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		require.NoError(t, err)

		// Second verification - should hit cache
		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		require.NoError(t, err)
	})

	t.Run("RejectsInvalidSignature", func(t *testing.T) {
		sv := NewSigVerifier(5 * time.Minute)
		defer sv.ClearCache()

		pubkey, _ := cipher.GenerateKeyPair()
		_, wrongSeckey := cipher.GenerateKeyPair()
		payload := []byte("test payload")

		// Sign with wrong key
		nonce := httpauth.Nonce(0)
		sig, err := httpauth.Sign(payload, nonce, wrongSeckey)
		require.NoError(t, err)

		// Should fail verification
		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		assert.Error(t, err)

		// Should still fail on second attempt (errors not cached)
		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		assert.Error(t, err)
	})

	t.Run("CacheExpiration", func(t *testing.T) {
		sv := NewSigVerifier(100 * time.Millisecond) // Very short TTL
		defer sv.ClearCache()

		pubkey, seckey := cipher.GenerateKeyPair()
		payload := []byte("test payload")

		nonce := httpauth.Nonce(0)
		sig, err := httpauth.Sign(payload, nonce, seckey)
		require.NoError(t, err)

		// First verification
		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		require.NoError(t, err)

		// Wait for expiration
		time.Sleep(150 * time.Millisecond)

		// Should still work but might not be cached
		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		require.NoError(t, err)
	})

	t.Run("DifferentPayloadsDifferentCache", func(t *testing.T) {
		sv := NewSigVerifier(5 * time.Minute)
		defer sv.ClearCache()

		pubkey, seckey := cipher.GenerateKeyPair()
		payload1 := []byte("payload one")
		payload2 := []byte("payload two")

		nonce := httpauth.Nonce(0)
		sig1, err := httpauth.Sign(payload1, nonce, seckey)
		require.NoError(t, err)

		sig2, err := httpauth.Sign(payload2, nonce, seckey)
		require.NoError(t, err)

		// Verify both
		err = sv.VerifyPubKeySignedPayload(pubkey, sig1, payload1)
		require.NoError(t, err)

		err = sv.VerifyPubKeySignedPayload(pubkey, sig2, payload2)
		require.NoError(t, err)

		// Wrong sig for payload should fail
		err = sv.VerifyPubKeySignedPayload(pubkey, sig1, payload2)
		assert.Error(t, err)
	})

	t.Run("ClearCache", func(t *testing.T) {
		sv := NewSigVerifier(5 * time.Minute)

		pubkey, seckey := cipher.GenerateKeyPair()
		payload := []byte("test payload")

		nonce := httpauth.Nonce(0)
		sig, err := httpauth.Sign(payload, nonce, seckey)
		require.NoError(t, err)

		// Verify and cache
		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		require.NoError(t, err)

		// Clear cache
		sv.ClearCache()

		// Should still verify successfully (will use crypto again)
		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		require.NoError(t, err)
	})
}

func BenchmarkSigVerifierCached(b *testing.B) {
	sv := NewSigVerifier(5 * time.Minute)
	defer sv.ClearCache()

	pubkey, seckey := cipher.GenerateKeyPair()
	payload := []byte("benchmark payload")

	nonce := httpauth.Nonce(0)
	sig, _ := httpauth.Sign(payload, nonce, seckey) //nolint

	// Prime the cache
	_ = sv.VerifyPubKeySignedPayload(pubkey, sig, payload) //nolint

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sv.VerifyPubKeySignedPayload(pubkey, sig, payload) //nolint
	}
}

func BenchmarkSigVerifierUncached(b *testing.B) {
	pubkey, seckey := cipher.GenerateKeyPair()
	payload := []byte("benchmark payload")

	nonce := httpauth.Nonce(0)
	sig, _ := httpauth.Sign(payload, nonce, seckey) //nolint

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cipher.VerifyPubKeySignedPayload(pubkey, sig, payload) //nolint
	}
}
