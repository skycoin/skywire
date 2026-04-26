package noise

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// newHandshakedPair creates an initiator/responder Noise pair that have
// completed the KK handshake and are ready for encrypt/decrypt.
func newHandshakedPair(t *testing.T) (initiator, responder *Noise) {
	t.Helper()

	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()

	var err error
	initiator, err = KKAndSecp256k1(Config{
		LocalPK:   pkI,
		LocalSK:   skI,
		RemotePK:  pkR,
		Initiator: true,
	})
	require.NoError(t, err)

	responder, err = KKAndSecp256k1(Config{
		LocalPK:   pkR,
		LocalSK:   skR,
		RemotePK:  pkI,
		Initiator: false,
	})
	require.NoError(t, err)

	// -> e, es
	msg, err := initiator.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, responder.ProcessHandshakeMessage(msg))

	// <- e, ee
	msg, err = responder.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, initiator.ProcessHandshakeMessage(msg))

	require.True(t, initiator.HandshakeFinished())
	require.True(t, responder.HandshakeFinished())

	return initiator, responder
}

func TestDecryptWithNonceMap_ReplayPrevention(t *testing.T) {
	nI, nR := newHandshakedPair(t)

	nm := make(NonceMap)
	ciphertext := nI.EncryptUnsafe([]byte("secret"))

	// First decryption should succeed.
	plaintext, err := nR.DecryptWithNonceMap(nm, ciphertext)
	require.NoError(t, err)
	assert.Equal(t, []byte("secret"), plaintext)

	// Replaying the same ciphertext should fail with "repeated" error.
	_, err = nR.DecryptWithNonceMap(nm, ciphertext)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repeated")
}

func TestDecryptWithNonceMap_OutOfOrder(t *testing.T) {
	nI, nR := newHandshakedPair(t)

	nm := make(NonceMap)

	ct1 := nI.EncryptUnsafe([]byte("msg1"))
	ct2 := nI.EncryptUnsafe([]byte("msg2"))
	ct3 := nI.EncryptUnsafe([]byte("msg3"))

	// Decrypt out of order: 3, 1, 2.
	pt, err := nR.DecryptWithNonceMap(nm, ct3)
	require.NoError(t, err)
	assert.Equal(t, []byte("msg3"), pt)

	pt, err = nR.DecryptWithNonceMap(nm, ct1)
	require.NoError(t, err)
	assert.Equal(t, []byte("msg1"), pt)

	pt, err = nR.DecryptWithNonceMap(nm, ct2)
	require.NoError(t, err)
	assert.Equal(t, []byte("msg2"), pt)
}

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	nI, nR := newHandshakedPair(t)

	messages := []string{"hello", "world", "a]b[c{d}e"}
	for _, msg := range messages {
		ct := nI.EncryptUnsafe([]byte(msg))
		pt, err := nR.DecryptUnsafe(ct)
		require.NoError(t, err)
		assert.Equal(t, []byte(msg), pt)
	}

	// Also test responder -> initiator direction.
	ct := nR.EncryptUnsafe([]byte("reverse"))
	pt, err := nI.DecryptUnsafe(ct)
	require.NoError(t, err)
	assert.Equal(t, []byte("reverse"), pt)
}

func TestEncryptDecrypt_LargePayload(t *testing.T) {
	nI, nR := newHandshakedPair(t)

	// 64 KiB payload.
	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}

	ct := nI.EncryptUnsafe(payload)
	pt, err := nR.DecryptUnsafe(ct)
	require.NoError(t, err)
	assert.Equal(t, payload, pt)
}
