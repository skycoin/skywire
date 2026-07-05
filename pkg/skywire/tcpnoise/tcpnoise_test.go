// Package tcpnoise pkg/skywire/tcpnoise/tcpnoise_test.go
package tcpnoise

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// acceptResult carries the outcome of the responder-side handshake back to the
// test goroutine.
type acceptResult struct {
	conn net.Conn
	rPK  cipher.PubKey
	err  error
}

// startResponder spins up a TCP listener that runs Accept on the first inbound
// connection using the supplied server identity. It returns the listener (for
// its address and cleanup) and a channel delivering the Accept result.
func startResponder(t *testing.T, sPK cipher.PubKey, sSK cipher.SecKey) (net.Listener, <-chan acceptResult) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	resCh := make(chan acceptResult, 1)
	go func() {
		raw, aerr := lis.Accept()
		if aerr != nil {
			resCh <- acceptResult{err: aerr}
			return
		}
		conn, rPK, herr := Accept(raw, sPK, sSK)
		resCh <- acceptResult{conn: conn, rPK: rPK, err: herr}
	}()
	return lis, resCh
}

// TestDialAccept_RoundTrip verifies the full initiator/responder handshake and
// that plaintext flows in both directions over the encrypted conn, with the
// peer public keys correctly surfaced on each side.
func TestDialAccept_RoundTrip(t *testing.T) {
	sPK, sSK := cipher.GenerateKeyPair()
	cPK, cSK := cipher.GenerateKeyPair()

	lis, resCh := startResponder(t, sPK, sSK)
	defer func() { _ = lis.Close() }() //nolint

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn, err := Dial(ctx, lis.Addr().String(), cPK, cSK, sPK)
	require.NoError(t, err)
	defer func() { _ = clientConn.Close() }() //nolint

	res := <-resCh
	require.NoError(t, res.err)
	require.NotNil(t, res.conn)
	defer func() { _ = res.conn.Close() }() //nolint

	// Responder learns the initiator's PK from the handshake.
	assert.Equal(t, cPK, res.rPK)
	assert.Equal(t, cPK, res.conn.(*Conn).RemotePK())
	// Initiator has the responder's PK pinned at dial time.
	assert.Equal(t, sPK, clientConn.(*Conn).RemotePK())

	// Embedded net.Conn methods are surfaced.
	assert.NotNil(t, clientConn.LocalAddr())
	assert.NotNil(t, clientConn.RemoteAddr())

	// client -> server
	wantC2S := []byte("hello from client")
	go func() {
		_, werr := clientConn.Write(wantC2S)
		assert.NoError(t, werr)
	}()
	got := make([]byte, len(wantC2S))
	_, rerr := readFull(res.conn, got)
	require.NoError(t, rerr)
	assert.Equal(t, wantC2S, got)

	// server -> client
	wantS2C := []byte("hello from server")
	go func() {
		_, werr := res.conn.Write(wantS2C)
		assert.NoError(t, werr)
	}()
	got2 := make([]byte, len(wantS2C))
	_, rerr = readFull(clientConn, got2)
	require.NoError(t, rerr)
	assert.Equal(t, wantS2C, got2)
}

// readFull reads exactly len(p) bytes, looping over the noise frame boundaries.
func readFull(c net.Conn, p []byte) (int, error) { //nolint
	total := 0
	for total < len(p) {
		n, err := c.Read(p[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// TestDial_BadAddr ensures a failed TCP dial is wrapped and reported without a
// connection leaking back to the caller.
func TestDial_BadAddr(t *testing.T) {
	cPK, cSK := cipher.GenerateKeyPair()
	sPK, _ := cipher.GenerateKeyPair()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Port 0 on the discard host is not connectable.
	conn, err := Dial(ctx, "127.0.0.1:0", cPK, cSK, sPK)
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.Contains(t, err.Error(), "tcpnoise: dial")
}

// TestDial_CanceledContext ensures dial honors a context that is already done.
func TestDial_CanceledContext(t *testing.T) {
	cPK, cSK := cipher.GenerateKeyPair()
	sPK, _ := cipher.GenerateKeyPair()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn, err := Dial(ctx, "127.0.0.1:1", cPK, cSK, sPK)
	require.Error(t, err)
	assert.Nil(t, conn)
}

// TestDial_HandshakeMismatch pins the wrong responder PK on the initiator. The
// XK handshake must fail and Dial must close the underlying conn and report a
// handshake error.
func TestDial_HandshakeMismatch(t *testing.T) {
	sPK, sSK := cipher.GenerateKeyPair()
	cPK, cSK := cipher.GenerateKeyPair()
	wrongPK, _ := cipher.GenerateKeyPair()

	lis, resCh := startResponder(t, sPK, sSK)
	defer func() { _ = lis.Close() }() //nolint

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Dial(ctx, lis.Addr().String(), cPK, cSK, wrongPK)
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.Contains(t, err.Error(), "handshake")

	// Responder side should also fail rather than hang.
	res := <-resCh
	assert.Error(t, res.err)
}

// TestAccept_HandshakeFail feeds Accept a connection whose peer hangs up before
// sending any handshake bytes; Accept must error and close the raw conn.
func TestAccept_HandshakeFail(t *testing.T) {
	sPK, sSK := cipher.GenerateKeyPair()

	c1, c2 := net.Pipe()
	// Peer closes immediately, so the responder's first read hits EOF.
	require.NoError(t, c2.Close())

	conn, rPK, err := Accept(c1, sPK, sSK)
	require.Error(t, err)
	assert.Nil(t, conn)
	assert.Equal(t, cipher.PubKey{}, rPK)
	assert.Contains(t, err.Error(), "tcpnoise: handshake")

	// raw conn must be closed by Accept: further reads fail.
	_, rerr := c1.Read(make([]byte, 1))
	assert.Error(t, rerr)
}

// TestConn_RemotePK confirms the accessor returns the pinned PK.
func TestConn_RemotePK(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	c := &Conn{rPK: pk}
	assert.Equal(t, pk, c.RemotePK())
}
