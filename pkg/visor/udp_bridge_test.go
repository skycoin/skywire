// Package visor pkg/visor/udp_bridge_test.go: unit tests for the
// UDPBridge type. Stage 4 of #2607.
//
// Tests wire a real net.UDPConn (loopback) as the "local" side and
// a net.Pipe-shaped mock PacketConn as the "skynet" side. End-to-
// end the bridge should pump datagrams bidirectionally between
// them. The skynet side intentionally uses a mock rather than a
// real DatagramRouteGroup; that integration arrives in Stage 5
// where the app API plumbing constructs DatagramRouteGroups from
// a real route setup. Stage 4's scope is the bridge's correctness
// as a generic net.PacketConn pump.

package visor

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pipePacketConn is a minimal in-memory net.PacketConn pair used
// as the "skynet" side of the bridge in tests. Two endpoints
// connected by buffered channels; either side ReadFrom blocks
// until the other WriteTo's. Close shuts both directions.
type pipePacketConn struct {
	mu      sync.Mutex
	closed  bool
	closeCh chan struct{}

	in  chan []byte // datagrams headed toward THIS endpoint
	out chan []byte // datagrams flowing OUT to the peer

	localAddr  net.Addr
	remoteAddr net.Addr

	readDeadline  time.Time
}

func newPipePacketConnPair() (a, b *pipePacketConn) {
	aToB := make(chan []byte, 32)
	bToA := make(chan []byte, 32)
	addrA := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9001}
	addrB := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9002}
	a = &pipePacketConn{
		closeCh:    make(chan struct{}),
		in:         bToA,
		out:        aToB,
		localAddr:  addrA,
		remoteAddr: addrB,
	}
	b = &pipePacketConn{
		closeCh:    make(chan struct{}),
		in:         aToB,
		out:        bToA,
		localAddr:  addrB,
		remoteAddr: addrA,
	}
	return
}

func (c *pipePacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case data, ok := <-c.in:
		if !ok {
			return 0, c.remoteAddr, net.ErrClosed
		}
		n := copy(p, data)
		return n, c.remoteAddr, nil
	case <-c.closeCh:
		return 0, c.remoteAddr, net.ErrClosed
	}
}

func (c *pipePacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	c.mu.Unlock()
	cp := make([]byte, len(p))
	copy(cp, p)
	select {
	case c.out <- cp:
		return len(p), nil
	case <-c.closeCh:
		return 0, net.ErrClosed
	}
}

func (c *pipePacketConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.closeCh)
	c.mu.Unlock()
	return nil
}

func (c *pipePacketConn) LocalAddr() net.Addr                { return c.localAddr }
func (c *pipePacketConn) SetDeadline(t time.Time) error      { return nil }
func (c *pipePacketConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *pipePacketConn) SetWriteDeadline(t time.Time) error { return nil }

// newTestUDPSocket binds a UDP socket on 127.0.0.1 with an
// ephemeral port and returns it. Caller cleans up.
func newTestUDPSocket(t *testing.T) *net.UDPConn {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	c, err := net.ListenUDP("udp", addr)
	require.NoError(t, err)
	return c
}

func TestUDPBridgeLocalToSkynet(t *testing.T) {
	// Bind a real UDP socket as the "local" end of the bridge; a
	// piped PacketConn as the "skynet" end. Send a datagram FROM
	// an external UDP sender, expect the bridge to relay it to
	// the skynet end.
	localSocket := newTestUDPSocket(t)
	defer localSocket.Close() //nolint:errcheck

	skynetA, skynetB := newPipePacketConnPair()
	bridge := NewUDPBridge(localSocket, skynetA, UDPBridgeConfig{})
	bridge.Start()
	defer bridge.Close() //nolint:errcheck

	// External UDP sender — dials the bridge's local UDP socket.
	sender, err := net.DialUDP("udp", nil, localSocket.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
	defer sender.Close() //nolint:errcheck

	payload := []byte("local-to-skynet")
	_, err = sender.Write(payload)
	require.NoError(t, err)

	// Skynet B (peer side of the pipe) should receive the
	// datagram via ReadFrom.
	buf := make([]byte, 1500)
	done := make(chan struct{})
	var (
		n   int
		rerr error
	)
	go func() {
		n, _, rerr = skynetB.ReadFrom(buf)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for skynet ReadFrom")
	}
	require.NoError(t, rerr)
	assert.Equal(t, payload, buf[:n])
}

func TestUDPBridgeSkynetToLocal(t *testing.T) {
	// Reverse direction. The bridge's local socket needs to know
	// where to deliver — it learns this from the first inbound
	// localToSkynet datagram. So this test first sends a local→
	// skynet primer, then sends skynet→local and asserts the
	// datagram reaches the original sender's UDP address.
	localSocket := newTestUDPSocket(t)
	defer localSocket.Close() //nolint:errcheck

	skynetA, skynetB := newPipePacketConnPair()
	bridge := NewUDPBridge(localSocket, skynetA, UDPBridgeConfig{})
	bridge.Start()
	defer bridge.Close() //nolint:errcheck

	sender, err := net.DialUDP("udp", nil, localSocket.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
	defer sender.Close() //nolint:errcheck

	// Primer: register the local peer.
	_, err = sender.Write([]byte("primer"))
	require.NoError(t, err)

	// Drain the primer on the skynet side.
	primerBuf := make([]byte, 1500)
	if err := skynetB.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		// pipePacketConn ignores deadlines; the read should
		// resolve quickly anyway.
		_ = err
	}
	_, _, err = skynetB.ReadFrom(primerBuf)
	require.NoError(t, err)

	// Now send skynet→local.
	payload := []byte("skynet-to-local")
	_, err = skynetB.WriteTo(payload, nil)
	require.NoError(t, err)

	// The original sender's socket should see the datagram.
	if err := sender.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 1500)
	n, err := sender.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, payload, buf[:n])
}

func TestUDPBridgeCloseTearsDownBothEnds(t *testing.T) {
	localSocket := newTestUDPSocket(t)
	skynetA, skynetB := newPipePacketConnPair()
	defer skynetB.Close() //nolint:errcheck

	bridge := NewUDPBridge(localSocket, skynetA, UDPBridgeConfig{})
	bridge.Start()

	require.NoError(t, bridge.Close())

	// Wait for both pumps to exit.
	doneCh := make(chan struct{})
	go func() {
		bridge.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("bridge pumps did not exit after Close")
	}

	// Both underlying conns should be closed.
	_, err := localSocket.Write([]byte("x"))
	require.Error(t, err, "local socket should be closed")

	_, err = skynetA.WriteTo([]byte("x"), nil)
	require.Error(t, err, "skynet conn should be closed")
}

func TestUDPBridgeCloseIsIdempotent(t *testing.T) {
	localSocket := newTestUDPSocket(t)
	skynetA, _ := newPipePacketConnPair()
	bridge := NewUDPBridge(localSocket, skynetA, UDPBridgeConfig{})
	bridge.Start()
	require.NoError(t, bridge.Close())
	require.NoError(t, bridge.Close())
	require.NoError(t, bridge.Close())
}
