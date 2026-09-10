//go:build !tinygo

// Package network pkg/transport/network/tcpdemux_dmsg_test.go c2-net-transport
package network

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport/network/handshake"
)

// speak dials the demux and writes what a protocol opens with, holding the
// connection open long enough for the demux to route it.
func speak(t *testing.T, addr string, open []byte) {
	t.Helper()
	go func() {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return
		}
		defer c.Close()      //nolint:errcheck
		_, _ = c.Write(open) //nolint:errcheck
		time.Sleep(500 * time.Millisecond)
	}()
}

// wantOn accepts on the listener the protocol is supposed to land on and checks
// the sniffed opening bytes were replayed intact. A connection routed anywhere
// else shows up here as an accept timeout.
func wantOn(t *testing.T, lis net.Listener, open []byte, what string) {
	t.Helper()
	c, err := acceptWithTimeout(t, lis)
	require.NoError(t, err, "%s did not land on its own listener", what)
	defer c.Close() //nolint:errcheck
	buf := make([]byte, len(open))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err, "%s: reading the replayed opening bytes", what)
	require.Equal(t, open, buf, "%s: the sniffed bytes must be replayed intact", what)
}

// dmsgFrame is what a dmsg session opens with: a 2-byte big-endian length
// followed by the noise handshake message. It has no fixed prefix, which is why
// dmsg takes the catch-all and stcpr is the one matched positively.
func dmsgFrame(n int) []byte {
	b := make([]byte, 2+n)
	binary.BigEndian.PutUint16(b, uint16(n)) //nolint:gosec // test sizes are small
	for i := 2; i < len(b); i++ {
		b[i] = byte(i)
	}
	return b
}

// With the dmsg branch installed each protocol reaches its own listener: stcpr
// by its literal handshake message, WS by the HTTP method, dmsg by everything
// else.
func TestTCPDemux_RoutesDmsgAlongsideStcprAndWS(t *testing.T) {
	master, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	d := newTCPDemux(master, true)
	defer d.Close() //nolint:errcheck
	require.NotNil(t, d.DMSG(), "the dmsg branch must exist when asked for")
	addr := master.Addr().String()

	stcpr := []byte(handshake.Message)
	speak(t, addr, stcpr)
	wantOn(t, d.STCPR(), stcpr, "stcpr")

	ws := []byte("GET /dmsg HTTP/1.1\r\nHost: x\r\n\r\n")
	speak(t, addr, ws)
	wantOn(t, d.WS(), ws, "ws")

	small := dmsgFrame(48)
	speak(t, addr, small)
	wantOn(t, d.DMSG(), small, "dmsg")

	// The post-quantum first message is over a KiB, so its length prefix has a
	// non-zero high byte. It must route the same way.
	big := dmsgFrame(1216)
	speak(t, addr, big)
	wantOn(t, d.DMSG(), big, "dmsg (post-quantum sized)")
}

// Without the dmsg branch nothing changes: there is no dmsg listener and every
// non-HTTP opener still falls through to stcpr, as it did before.
func TestTCPDemux_WithoutDmsgKeepsStcprCatchAll(t *testing.T) {
	master, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	d := newTCPDemux(master, false)
	defer d.Close() //nolint:errcheck
	require.Nil(t, d.DMSG())
	addr := master.Addr().String()

	stcpr := []byte(handshake.Message)
	speak(t, addr, stcpr)
	wantOn(t, d.STCPR(), stcpr, "stcpr")

	other := dmsgFrame(48)
	speak(t, addr, other)
	wantOn(t, d.STCPR(), other, "an unrecognised opener with dmsg not sharing")
}

// The shared listener is handed out only when the factory asked for it, and it
// reports the master port, so a server that advertises what its listener
// resolves to advertises the shared transport port.
func TestClientFactory_DmsgSharedListener(t *testing.T) {
	var off ClientFactory
	require.NoError(t, off.EnableDefaultTCPDemux(0))
	defer off.CloseUnifiedTCP() //nolint:errcheck
	require.Nil(t, off.DmsgSharedListener())

	on := ClientFactory{ShareTCPWithDmsgServer: true}
	require.NoError(t, on.EnableDefaultTCPDemux(0))
	defer on.CloseUnifiedTCP() //nolint:errcheck
	shared := on.DmsgSharedListener()
	require.NotNil(t, shared)
	require.Equal(t, on.stcprSharedListener.Addr().String(), shared.Addr().String(),
		"the dmsg branch listens on the same port as stcpr")
}
