// Package router pkg/router/datagram_keys_test.go: tests for the
// faithful-UDP master-key derivation (#2607 stage-4b).
package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestDeriveDatagramMasterKeyDeterministic verifies both peers derive
// the SAME master key from the SAME channel binding — the property the
// whole keyless-second-handshake design relies on.
func TestDeriveDatagramMasterKeyDeterministic(t *testing.T) {
	cb := []byte("a-noise-channel-binding-32-bytes!!") // stand-in transcript hash
	k1, err := deriveDatagramMasterKey(cb)
	require.NoError(t, err)
	k2, err := deriveDatagramMasterKey(cb)
	require.NoError(t, err)
	assert.Equal(t, k1, k2, "same channel binding must derive the same master key")
	assert.NotEqual(t, [32]byte{}, k1, "derived key must not be all-zero")
}

// TestDeriveDatagramMasterKeyDistinctPerSession verifies different
// sessions (different bindings) derive different keys.
func TestDeriveDatagramMasterKeyDistinctPerSession(t *testing.T) {
	k1, err := deriveDatagramMasterKey([]byte("session-one-binding"))
	require.NoError(t, err)
	k2, err := deriveDatagramMasterKey([]byte("session-two-binding"))
	require.NoError(t, err)
	assert.NotEqual(t, k1, k2, "distinct sessions must derive distinct master keys")
}

// TestDeriveDatagramMasterKeyEmptyRejected guards the handshake-not-
// finished footgun.
func TestDeriveDatagramMasterKeyEmptyRejected(t *testing.T) {
	_, err := deriveDatagramMasterKey(nil)
	require.ErrorIs(t, err, errEmptyChannelBinding)
	_, err = deriveDatagramMasterKey([]byte{})
	require.ErrorIs(t, err, errEmptyChannelBinding)
}

// TestDeriveDatagramMasterKeyFeedsCipherRoundTrip is the end-to-end
// property that matters: a master key derived from a shared binding,
// fed to NewDatagramCipher on each side with opposite roles, produces
// ciphers that interoperate — initiator seals, responder opens.
func TestDeriveDatagramMasterKeyFeedsCipherRoundTrip(t *testing.T) {
	cb := []byte("shared-noise-session-transcript-hash")
	master, err := deriveDatagramMasterKey(cb)
	require.NoError(t, err)

	// The receiver in this exchange is the responder; its PK is the AAD
	// binding on both the initiator's out-cipher and the responder's
	// in-cipher (AAD binds to the *receiver*).
	respPK, _ := cipher.GenerateKeyPair()

	// Initiator's outbound cipher (init-direction subkey, AAD=receiver).
	initOut, err := NewDatagramCipher(master, RoleInitiator, respPK, nil)
	require.NoError(t, err)
	// Responder's inbound cipher opens what the initiator sealed: same
	// init-direction subkey, same AAD binding PK (the receiver).
	respIn, err := NewDatagramCipher(master, RoleInitiator, respPK, nil)
	require.NoError(t, err)

	const routeID = uint32(7)
	plaintext := []byte("voip-frame")
	sealed, err := initOut.Seal(routeID, plaintext)
	require.NoError(t, err)
	opened, err := respIn.Open(routeID, sealed)
	require.NoError(t, err)
	assert.Equal(t, plaintext, opened)
}
