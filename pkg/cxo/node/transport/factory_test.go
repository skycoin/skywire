package transport

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// recvWithin reads one message from c.GetChanIn within d or fails the test.
func recvWithin(t *testing.T, c *Connection, d time.Duration) []byte {
	t.Helper()
	select {
	case got := <-c.GetChanIn():
		return got
	case <-time.After(d):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

// acceptWithin waits for an accepted server-side connection.
func acceptWithin(t *testing.T, ch <-chan *Connection, d time.Duration) *Connection {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(d):
		t.Fatal("timed out waiting for accepted connection")
		return nil
	}
}

// TestTCPFactoryRoundTrip dials a listening TCP factory, completes the Noise XX
// handshake on both ends, and round-trips a message.
func TestTCPFactoryRoundTrip(t *testing.T) {
	sPK, sSK := cipher.GenerateKeyPair()
	cPK, cSK := cipher.GenerateKeyPair()

	srv := NewTCPFactory(sPK, sSK)
	accepted := make(chan *Connection, 1)
	srv.AcceptedCallback = func(c *Connection) { accepted <- c }
	require.NoError(t, srv.Listen("127.0.0.1:0"))
	defer srv.Close()

	addr := srv.ListenerAddress()
	require.NotEmpty(t, addr)

	cli := NewTCPFactory(cPK, cSK)
	conn, err := cli.Connect(addr)
	require.NoError(t, err)
	defer conn.Close()
	assert.True(t, conn.IsTCP())

	srvConn := acceptWithin(t, accepted, 10*time.Second)
	defer srvConn.Close()

	conn.GetChanOut() <- Frame{Body: []byte("over noise")}
	assert.Equal(t, []byte("over noise"), recvWithin(t, srvConn, 10*time.Second))
}

// TestTCPFactoryConnectError verifies dialing an unreachable address errors.
func TestTCPFactoryConnectError(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	f := NewTCPFactory(pk, sk)
	_, err := f.Connect("127.0.0.1:1")
	assert.Error(t, err)
}

// TestTCPFactoryListenError verifies an invalid bind address errors.
func TestTCPFactoryListenError(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	f := NewTCPFactory(pk, sk)
	assert.Error(t, f.Listen("127.0.0.1:999999"))
}

// TestTCPFactoryListenerAddressUnset verifies the address is empty before Listen.
func TestTCPFactoryListenerAddressUnset(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	assert.Empty(t, NewTCPFactory(pk, sk).ListenerAddress())
}

// TestTCPFactoryCloseIdempotent verifies Close is safe to call twice.
func TestTCPFactoryCloseIdempotent(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	f := NewTCPFactory(pk, sk)
	require.NoError(t, f.Listen("127.0.0.1:0"))
	f.Close()
	f.Close()
}

// TestUDPFactoryRoundTrip dials a listening UDP factory (TCP-backed) and
// round-trips a message.
func TestUDPFactoryRoundTrip(t *testing.T) {
	srv := NewUDPFactory()
	accepted := make(chan *Connection, 1)
	srv.AcceptedCallback = func(c *Connection) { accepted <- c }
	require.NoError(t, srv.Listen("127.0.0.1:0"))
	defer srv.Close()

	addr := srv.listener.Addr().String()

	cli := NewUDPFactory()
	defer cli.Close()
	conn, err := cli.Connect(addr)
	require.NoError(t, err)
	defer conn.Close()
	assert.False(t, conn.IsTCP())

	srvConn := acceptWithin(t, accepted, 5*time.Second)
	defer srvConn.Close()

	conn.GetChanOut() <- Frame{Body: []byte("udp-msg")}
	assert.Equal(t, []byte("udp-msg"), recvWithin(t, srvConn, 5*time.Second))
}

// TestUDPFactoryConnectError verifies dialing an unreachable address errors.
func TestUDPFactoryConnectError(t *testing.T) {
	_, err := NewUDPFactory().Connect("127.0.0.1:1")
	assert.Error(t, err)
}

// TestUDPFactoryListenError verifies an invalid bind address errors.
func TestUDPFactoryListenError(t *testing.T) {
	assert.Error(t, NewUDPFactory().Listen("127.0.0.1:999999"))
}

// TestUDPFactoryCloseIdempotent verifies Close is safe to call twice.
func TestUDPFactoryCloseIdempotent(t *testing.T) {
	f := NewUDPFactory()
	require.NoError(t, f.Listen("127.0.0.1:0"))
	f.Close()
	f.Close()
}
