package vpn

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

// pumpPackets copies one-IP-packet-per-Read from src into dst until src
// closes — exactly what the VPN serve loop's io.Copy does between the TUN and
// the route-group conn, minus the conn.
func pumpPackets(t *testing.T, dst, src *netstackTUN) {
	t.Helper()
	go func() {
		buf := make([]byte, TUNMTU+4)
		for {
			n, err := src.Read(buf)
			if err != nil {
				return
			}
			if _, err := dst.Write(buf[:n]); err != nil {
				return
			}
		}
	}()
}

// TestNetstackTUNLoopback proves the whole js VPN data plane shape with pure
// Go: a client-side netstack dials THROUGH its TUNDevice seam, the packets
// cross a simulated tunnel (two pumps, as the serve loop would carry them to
// and from the vpn-server), and a peer stack's listener answers. One TCP
// round-trip through this path exercises address configuration, the
// one-packet-per-read contract, inbound injection, and gonet dialing — the
// pieces a browser vpn-client stands on.
func TestNetstackTUNLoopback(t *testing.T) {
	clientTUN, err := newNetstackTUN()
	require.NoError(t, err)
	defer clientTUN.Close() //nolint:errcheck,gosec
	require.NoError(t, clientTUN.configure("10.8.0.2/29"))

	serverTUN, err := newNetstackTUN()
	require.NoError(t, err)
	defer serverTUN.Close() //nolint:errcheck,gosec
	require.NoError(t, serverTUN.configure("10.8.0.1/29"))

	// The "tunnel": client's outbound packets land in the server stack and
	// vice versa.
	pumpPackets(t, serverTUN, clientTUN)
	pumpPackets(t, clientTUN, serverTUN)

	// Echo listener inside the server stack.
	lis, err := gonet.ListenTCP(serverTUN.stack, tcpip.FullAddress{
		NIC:  netstackNICID,
		Addr: tcpip.AddrFrom4([4]byte{10, 8, 0, 1}),
		Port: 8080,
	}, ipv4.ProtocolNumber)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck,gosec
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		_, _ = io.Copy(conn, conn) //nolint:errcheck
		_ = conn.Close()           //nolint:errcheck
	}()

	// Dial through the client device exactly as NetstackDial would.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := clientTUN.dial(ctx, "tcp", "10.8.0.1:8080")
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck,gosec

	msg := []byte("through the netstack tunnel")
	_, err = conn.Write(msg)
	require.NoError(t, err)
	got := make([]byte, len(msg))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	_, err = io.ReadFull(conn, got)
	require.NoError(t, err)
	require.Equal(t, msg, got)

	// A closed device unblocks Read with EOF (the serve loop's exit path).
	require.NoError(t, clientTUN.Close())
	buf := make([]byte, 16)
	_, err = clientTUN.Read(buf)
	require.ErrorIs(t, err, io.EOF)
}
