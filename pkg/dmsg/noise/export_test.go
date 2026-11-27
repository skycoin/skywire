// Package noise pkg/dmsg/noise/export_test.go c1-net-dmsg
package noise

import (
	"crypto/sha256"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/hkdf"

	"github.com/skycoin/skywire/pkg/cipher"
)

func kkPair(t *testing.T) (nI, nR *Noise, pkI, pkR cipher.PubKey, msg1, msg2 []byte) {
	t.Helper()
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()
	nI, err := KKAndSecp256k1(Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	nR, err = KKAndSecp256k1(Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI, Initiator: false})
	require.NoError(t, err)
	msg1, err = nI.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nR.ProcessHandshakeMessage(msg1))
	msg2, err = nR.MakeHandshakeMessage()
	require.NoError(t, err)
	require.NoError(t, nI.ProcessHandshakeMessage(msg2))
	return nI, nR, pkI, pkR, msg1, msg2
}

// observerHandshakeHash replays the Noise MixHash chain from public data only
// (Noise spec section 5.2 and 7): protocol name, empty prologue, both static
// keys, and the two handshake messages as seen on the wire.
func observerHandshakeHash(pkI, pkR cipher.PubKey, msg1, msg2 []byte) []byte {
	h := sha256.Sum256([]byte("Noise_KK_Secp256k1_ChaChaPoly_SHA256"))
	mix := func(b []byte) { h = sha256.Sum256(append(h[:], b...)) }
	const dhLen = 33
	mix(nil)
	mix(pkI[:])
	mix(pkR[:])
	mix(msg1[:dhLen])
	mix(msg1[dhLen:])
	mix(msg2[:dhLen])
	mix(msg2[dhLen:])
	return h[:]
}

// TestChannelBindingIsPublic pins why ChannelBinding must never be used as a
// key: anyone who saw the handshake can compute it.
func TestChannelBindingIsPublic(t *testing.T) {
	nI, nR, pkI, pkR, msg1, msg2 := kkPair(t)
	require.Equal(t, nI.ChannelBinding(), nR.ChannelBinding())
	require.Equal(t, nI.ChannelBinding(), observerHandshakeHash(pkI, pkR, msg1, msg2))
}

func TestExportKey(t *testing.T) {
	nI, nR, pkI, pkR, msg1, msg2 := kkPair(t)
	kI, err := nI.ExportKey("label-a")
	require.NoError(t, err)
	kR, err := nR.ExportKey("label-a")
	require.NoError(t, err)
	require.Equal(t, kI, kR, "both peers export the same key")
	require.NotEqual(t, [32]byte{}, kI)

	kB, err := nI.ExportKey("label-b")
	require.NoError(t, err)
	require.NotEqual(t, kI, kB, "labels separate keys")

	// An observer has the handshake hash and the label, but not the key.
	public := observerHandshakeHash(pkI, pkR, msg1, msg2)
	h := hkdf.New(sha256.New, public, nil, []byte("label-a"))
	var guess [32]byte
	_, err = io.ReadFull(h, guess[:])
	require.NoError(t, err)
	require.NotEqual(t, guess, kI)

	nI2, _, _, _, _, _ := kkPair(t)
	k2, err := nI2.ExportKey("label-a")
	require.NoError(t, err)
	require.NotEqual(t, kI, k2, "sessions get distinct keys")
}

func TestExportKeyBeforeHandshake(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, _ := cipher.GenerateKeyPair()
	n, err := KKAndSecp256k1(Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	_, err = n.ExportKey("label")
	require.ErrorIs(t, err, errExportBeforeHandshake)
	_, err = n.MakeHandshakeMessage()
	require.NoError(t, err)
	_, err = n.ExportKey("label")
	require.ErrorIs(t, err, errExportBeforeHandshake)
}

func TestConnExportKey(t *testing.T) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()
	nI, err := KKAndSecp256k1(Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	require.NoError(t, err)
	nR, err := KKAndSecp256k1(Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI, Initiator: false})
	require.NoError(t, err)
	a, b := net.Pipe()
	defer a.Close() //nolint:errcheck
	defer b.Close() //nolint:errcheck

	connR := make(chan *Conn, 1)
	go func() {
		c, err := WrapConn(b, nR, 5*time.Second)
		assert.NoError(t, err)
		connR <- c
	}()
	cI, err := WrapConn(a, nI, 5*time.Second)
	require.NoError(t, err)
	cR := <-connR
	require.NotNil(t, cR)

	kI, err := cI.ExportKey("datagram")
	require.NoError(t, err)
	kR, err := cR.ExportKey("datagram")
	require.NoError(t, err)
	require.Equal(t, kI, kR)
}
