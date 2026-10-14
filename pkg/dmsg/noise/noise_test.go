// Package noise pkg/noise/noise_test.go
package noise

import (
	"fmt"
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
	require.Nil(t, nI.pqInit, "the initiator drops its ephemeral ML-KEM key once the handshake is done")

	ct := nI.EncryptUnsafe([]byte("post-quantum"))
	pt, err := nR.DecryptUnsafe(ct)
	require.NoError(t, err)
	require.Equal(t, "post-quantum", string(pt))
	ct = nR.EncryptUnsafe([]byte("hybrid"))
	pt, err = nI.DecryptUnsafe(ct)
	require.NoError(t, err)
	require.Equal(t, "hybrid", string(pt))
}

// TestClassicalHandshakeRefused pins that post-quantum is required: a peer
// that does not take part in the ML-KEM exchange, as pre-v1.3.80 code did or
// as a MITM stripping the payload would, is refused in either role.
func TestClassicalHandshakeRefused(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()

	// A new initiator and an old responder that ignores the offer.
	nI, err := KKAndSecp256k1(Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	nR, err := KKAndSecp256k1(Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI, Initiator: false})
	require.NoError(t, err)
	msg1, err := nI.MakeHandshakeMessage()
	require.NoError(t, err)
	_, _, _, err = nR.hs.ReadMessage(nil, msg1)
	require.NoError(t, err)
	msg2, _, _, err := nR.hs.WriteMessage(nil, nil)
	require.NoError(t, err)
	require.ErrorIs(t, nI.ProcessHandshakeMessage(msg2), errClassicalOnly)
	require.False(t, nI.PQActive())
	require.Nil(t, nI.pqInit, "the ephemeral ML-KEM key is dropped on a refusal too")

	// An old initiator that offers nothing, and a new responder.
	nI, err = KKAndSecp256k1(Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	nR, err = KKAndSecp256k1(Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI, Initiator: false})
	require.NoError(t, err)
	msg1, _, _, err = nI.hs.WriteMessage(nil, nil)
	require.NoError(t, err)
	require.NoError(t, nR.ProcessHandshakeMessage(msg1))
	_, err = nR.MakeHandshakeMessage()
	require.ErrorIs(t, err, errClassicalOnly)
	require.False(t, nR.PQActive())
}

// TestSealOpenWithNonce validates the per-frame AEAD primitive used by the mux
// inverse-multiplexer: after a KK handshake, SealWithNonce/OpenWithNonce round-
// trip in BOTH directions, decrypt correctly OUT OF ORDER (each frame carries
// its own explicit nonce, so there is no stateful cipher to desync), never touch
// the stream encNonce/decNonce counters, and reject a tampered frame.
func TestSealOpenWithNonce(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()

	nI, err := New(HandshakeKK, Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	nR, err := New(HandshakeKK, Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI, Initiator: false})
	require.NoError(t, err)

	msg, err := nI.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nR.ProcessHandshakeMessage(msg))
	msg, err = nR.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nI.ProcessHandshakeMessage(msg))
	require.True(t, nI.HandshakeFinished() && nR.HandshakeFinished())

	// Snapshot the stream nonce counters; per-frame seal/open must NOT move them.
	encN, decN := nI.GetEncNonce(), nR.GetDecNonce()

	// Seal a batch of frames on the initiator, then open them on the responder
	// in SHUFFLED order — the whole point of per-frame noise.
	type frame struct {
		seq uint64
		pt  []byte
		ct  []byte
	}
	frames := make([]frame, 0, 64)
	for i := 0; i < 64; i++ {
		pt := []byte(fmt.Sprintf("frame-payload-%d-xyzzy", i))
		ct := nI.SealWithNonce(uint64(i), pt)
		require.NotNil(t, ct)
		frames = append(frames, frame{seq: uint64(i), pt: pt, ct: ct})
	}
	// Deterministic shuffle (no rand dependency): reverse + interleave.
	for i, j := 0, len(frames)-1; i < j; i, j = i+1, j-1 {
		frames[i], frames[j] = frames[j], frames[i]
	}
	for _, f := range frames {
		got, err := nR.OpenWithNonce(f.seq, f.ct)
		require.NoError(t, err, "open seq %d", f.seq)
		require.Equal(t, f.pt, got)
	}

	// Reverse direction: responder seals, initiator opens.
	ct := nR.SealWithNonce(7, []byte("reverse-dir"))
	got, err := nI.OpenWithNonce(7, ct)
	require.NoError(t, err)
	require.Equal(t, []byte("reverse-dir"), got)

	// Wrong nonce and tampered tag must fail (not silently accept).
	_, err = nR.OpenWithNonce(9999, frames[0].ct)
	require.Error(t, err, "opening with the wrong nonce must fail")
	bad := append([]byte(nil), frames[0].ct...)
	bad[len(bad)-1] ^= 0xff
	_, err = nR.OpenWithNonce(frames[0].seq, bad)
	require.Error(t, err, "tampered ciphertext must fail")

	// Stream counters untouched.
	require.Equal(t, encN, nI.GetEncNonce(), "SealWithNonce must not advance encNonce")
	require.Equal(t, decN, nR.GetDecNonce(), "OpenWithNonce must not advance decNonce")
}

// TestWriterTurn validates the handshake-turn accessor used by the mux per-frame
// integration to avoid calling MakeHandshakeMessage out of turn (the bug that
// broke live per-frame route groups: a responder writing msg2 before reading
// msg1). Initiator writes first (msg1) then reads; responder reads (msg1) then
// writes (msg2); neither may write when it is not their turn.
func TestWriterTurn(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()
	nI, err := New(HandshakeKK, Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	nR, err := New(HandshakeKK, Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI, Initiator: false})
	require.NoError(t, err)

	// Start of KK: initiator writes msg1, responder must NOT write yet.
	require.True(t, nI.WriterTurn(), "initiator writes msg1 first")
	require.False(t, nR.WriterTurn(), "responder must read msg1 before writing")

	msg1, err := nI.MakeHandshakeMessage()
	require.NoError(t, err)
	// After writing msg1 the initiator reads next; the responder still owes a read.
	require.False(t, nI.WriterTurn(), "initiator reads msg2 next")
	require.False(t, nR.WriterTurn(), "responder still must read msg1")

	require.NoError(t, nR.ProcessHandshakeMessage(msg1))
	// Now it IS the responder's turn to write msg2.
	require.True(t, nR.WriterTurn(), "responder writes msg2 after reading msg1")

	msg2, err := nR.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nI.ProcessHandshakeMessage(msg2))

	// Handshake complete on both sides; neither writes further.
	require.True(t, nI.HandshakeFinished() && nR.HandshakeFinished())
	require.False(t, nI.WriterTurn())
	require.False(t, nR.WriterTurn())
}
