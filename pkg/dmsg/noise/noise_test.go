// Package noise pkg/noise/noise_test.go
package noise

import (
	"log"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestMain(m *testing.M) {
	loggingLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
	if ok {
		lvl, err := logging.LevelFromString(loggingLevel)
		if err != nil {
			log.Fatal(err)
		}
		logging.SetLevel(lvl)
	} else {
		logging.Disable()
	}

	os.Exit(m.Run())
}

func TestKKAndSecp256k1(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()

	confI := Config{
		LocalPK:   pkI,
		LocalSK:   skI,
		RemotePK:  pkR,
		Initiator: true,
	}

	confR := Config{
		LocalPK:   pkR,
		LocalSK:   skR,
		RemotePK:  pkI,
		Initiator: false,
	}

	nI, err := KKAndSecp256k1(confI)
	require.NoError(t, err)

	nR, err := KKAndSecp256k1(confR)
	require.NoError(t, err)

	// -> e, es
	msg, err := nI.MakeHandshakeMessage()
	require.NoError(t, err)
	require.Error(t, nR.ProcessHandshakeMessage(append(msg, 1)))
	require.NoError(t, nR.ProcessHandshakeMessage(msg))

	// <- e, ee
	msg, err = nR.MakeHandshakeMessage()
	require.NoError(t, err)
	require.Error(t, nI.ProcessHandshakeMessage(append(msg, 1)))
	require.NoError(t, nI.ProcessHandshakeMessage(msg))

	require.True(t, nI.HandshakeFinished())
	require.True(t, nR.HandshakeFinished())

	encrypted := nI.EncryptUnsafe([]byte("foo"))
	decrypted, err := nR.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("foo"), decrypted)

	encrypted = nR.EncryptUnsafe([]byte("bar"))
	decrypted, err = nI.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("bar"), decrypted)

	encrypted = nI.EncryptUnsafe([]byte("baz"))
	decrypted, err = nR.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("baz"), decrypted)
}

func TestXKAndSecp256k1(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()

	confI := Config{
		LocalPK:   pkI,
		LocalSK:   skI,
		RemotePK:  pkR,
		Initiator: true,
	}

	confR := Config{
		LocalPK:   pkR,
		LocalSK:   skR,
		Initiator: false,
	}

	nI, err := XKAndSecp256k1(confI)
	require.NoError(t, err)

	nR, err := XKAndSecp256k1(confR)
	require.NoError(t, err)

	// -> e, es
	msg, err := nI.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nR.ProcessHandshakeMessage(msg))

	// <- e, ee
	msg, err = nR.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nI.ProcessHandshakeMessage(msg))

	// -> s, se
	msg, err = nI.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nR.ProcessHandshakeMessage(msg))

	require.True(t, nI.HandshakeFinished())
	require.True(t, nR.HandshakeFinished())

	encrypted := nI.EncryptUnsafe([]byte("foo"))
	decrypted, err := nR.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("foo"), decrypted)

	encrypted = nR.EncryptUnsafe([]byte("bar"))
	decrypted, err = nI.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("bar"), decrypted)

	encrypted = nI.EncryptUnsafe([]byte("baz"))
	decrypted, err = nR.DecryptUnsafe(encrypted)
	require.NoError(t, err)
	assert.Equal(t, []byte("baz"), decrypted)
}

// TestPQHybridHandshake drives a full KK handshake between two PQ-aware
// instances and asserts the post-quantum hybrid was negotiated on BOTH ends
// (not a silent classical fallback) and that the resulting hybrid keys agree —
// proven by a successful bidirectional transport round trip.
func TestPQHybridHandshake(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()
	nI, err := KKAndSecp256k1(Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	nR, err := KKAndSecp256k1(Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI, Initiator: false})
	require.NoError(t, err)

	msg1, err := nI.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nR.ProcessHandshakeMessage(msg1))
	msg2, err := nR.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nI.ProcessHandshakeMessage(msg2))

	require.True(t, nI.HandshakeFinished())
	require.True(t, nR.HandshakeFinished())
	require.True(t, nI.PQActive(), "initiator must negotiate PQ with a PQ-aware peer")
	require.True(t, nR.PQActive(), "responder must negotiate PQ with a PQ-aware peer")

	ct := nI.EncryptUnsafe([]byte("post-quantum"))
	pt, err := nR.DecryptUnsafe(ct)
	require.NoError(t, err)
	require.Equal(t, "post-quantum", string(pt))
	ct = nR.EncryptUnsafe([]byte("hybrid"))
	pt, err = nI.DecryptUnsafe(ct)
	require.NoError(t, err)
	require.Equal(t, "hybrid", string(pt))
}

// TestPQHandshakeFallbackToClassical proves reverse-compatibility: a new PQ-aware
// initiator vs a simulated OLD responder (one that drives its handshake with nil
// payloads, as pre-PQ code did, ignoring the PQ offer) transparently falls back
// to a classical handshake and still communicates — no flag, no failed attempt.
func TestPQHandshakeFallbackToClassical(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()
	nI, err := KKAndSecp256k1(Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	nR, err := KKAndSecp256k1(Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI, Initiator: false})
	require.NoError(t, err)

	// New initiator offers PQ (ML-KEM pubkey in msg1's payload).
	msg1, err := nI.MakeHandshakeMessage()
	require.NoError(t, err)

	// Simulate an OLD responder: read msg1 IGNORING the payload, and reply with a
	// nil payload (the pre-PQ behavior). Drive the raw flynn handshake directly.
	_, _, _, err = nR.hs.ReadMessage(nil, msg1)
	require.NoError(t, err)
	msg2, cs0, cs1, err := nR.hs.WriteMessage(nil, nil)
	require.NoError(t, err)
	nR.dec, nR.enc = cs0, cs1 // mirror MakeHandshakeMessage's final assignment

	// Initiator sees an empty payload → classical fallback, no PQ.
	require.NoError(t, nI.ProcessHandshakeMessage(msg2))
	require.False(t, nI.PQActive(), "initiator must fall back to classical with an old peer")

	// Classical transport still works in both directions.
	ct := nI.EncryptUnsafe([]byte("compat"))
	pt, err := nR.DecryptUnsafe(ct)
	require.NoError(t, err)
	require.Equal(t, "compat", string(pt))
	ct = nR.EncryptUnsafe([]byte("classical"))
	pt, err = nI.DecryptUnsafe(ct)
	require.NoError(t, err)
	require.Equal(t, "classical", string(pt))
}
