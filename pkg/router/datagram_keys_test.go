// Package router pkg/router/datagram_keys_test.go tests for the
// faithful-UDP master-key derivation (#2607 stage-4b).
package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
)

// noiseSessionPair runs a real KK handshake and returns both ends.
func noiseSessionPair(t *testing.T) (initiator, responder *noise.Noise, respPK cipher.PubKey) {
	t.Helper()
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()
	nI, err := noise.KKAndSecp256k1(noise.Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	nR, err := noise.KKAndSecp256k1(noise.Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI, Initiator: false})
	require.NoError(t, err)
	msg, err := nI.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nR.ProcessHandshakeMessage(msg))
	msg, err = nR.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nI.ProcessHandshakeMessage(msg))
	return nI, nR, pkR
}

func TestDeriveDatagramMasterKeyBothPeers(t *testing.T) {
	nI, nR, _ := noiseSessionPair(t)
	kI, err := deriveDatagramMasterKey(nI)
	require.NoError(t, err)
	kR, err := deriveDatagramMasterKey(nR)
	require.NoError(t, err)
	assert.Equal(t, kI, kR, "both peers must derive the same master key")
	assert.NotEqual(t, [32]byte{}, kI)

	nI2, _, _ := noiseSessionPair(t)
	k2, err := deriveDatagramMasterKey(nI2)
	require.NoError(t, err)
	assert.NotEqual(t, kI, k2, "distinct sessions must derive distinct master keys")
}

func TestDeriveDatagramMasterKeyBeforeHandshake(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, _ := cipher.GenerateKeyPair()
	n, err := noise.KKAndSecp256k1(noise.Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	_, err = deriveDatagramMasterKey(n)
	require.Error(t, err)
}

// TestDeriveDatagramMasterKeyFeedsCipherRoundTrip: masters derived on
// each end of a real session feed NewDatagramCipher, and the initiator's
// sealed datagram opens on the responder.
func TestDeriveDatagramMasterKeyFeedsCipherRoundTrip(t *testing.T) {
	nI, nR, respPK := noiseSessionPair(t)
	masterI, err := deriveDatagramMasterKey(nI)
	require.NoError(t, err)
	masterR, err := deriveDatagramMasterKey(nR)
	require.NoError(t, err)

	initOut, err := NewDatagramCipher(masterI, RoleInitiator, respPK, nil)
	require.NoError(t, err)
	respIn, err := NewDatagramCipher(masterR, RoleInitiator, respPK, nil)
	require.NoError(t, err)

	const routeID = uint32(7)
	plaintext := []byte("voip-frame")
	sealed, err := initOut.Seal(routeID, plaintext)
	require.NoError(t, err)
	opened, err := respIn.Open(routeID, sealed)
	require.NoError(t, err)
	assert.Equal(t, plaintext, opened)
}
