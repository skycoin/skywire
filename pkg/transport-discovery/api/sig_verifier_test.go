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

		pubkey, seckey := cipher.GenerateKeyPair()
		payload := []byte("test payload")

		nonce := httpauth.Nonce(0)
		sig, err := httpauth.Sign(payload, nonce, seckey)
		require.NoError(t, err)

		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		require.NoError(t, err)

		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		require.NoError(t, err)
	})

	t.Run("RejectsInvalidSignature", func(t *testing.T) {
		sv := NewSigVerifier(5 * time.Minute)
		defer sv.ClearCache()

		pubkey, _ := cipher.GenerateKeyPair()
		_, wrongSeckey := cipher.GenerateKeyPair()
		payload := []byte("test payload")

		nonce := httpauth.Nonce(0)
		sig, err := httpauth.Sign(payload, nonce, wrongSeckey)
		require.NoError(t, err)

		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		assert.Error(t, err)

		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		assert.Error(t, err)
	})

	t.Run("CacheExpiration", func(t *testing.T) {
		sv := NewSigVerifier(100 * time.Millisecond)
		defer sv.ClearCache()

		pubkey, seckey := cipher.GenerateKeyPair()
		payload := []byte("test payload")

		nonce := httpauth.Nonce(0)
		sig, err := httpauth.Sign(payload, nonce, seckey)
		require.NoError(t, err)

		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		require.NoError(t, err)

		time.Sleep(150 * time.Millisecond)

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

		err = sv.VerifyPubKeySignedPayload(pubkey, sig1, payload1)
		require.NoError(t, err)

		err = sv.VerifyPubKeySignedPayload(pubkey, sig2, payload2)
		require.NoError(t, err)

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

		err = sv.VerifyPubKeySignedPayload(pubkey, sig, payload)
		require.NoError(t, err)

		sv.ClearCache()

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
