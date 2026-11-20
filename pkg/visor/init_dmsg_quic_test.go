//go:build !mobile

// The folded dmsg server is desktop-only (init_dmsg_server.go), its QUIC
// address helper with it.

package visor

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDmsgQUICAdvertisedAddr(t *testing.T) {
	udp := &net.UDPAddr{IP: net.IPv4zero, Port: 30082}
	require.Equal(t, "45.79.124.73:30082", dmsgQUICAdvertisedAddr("45.79.124.73:30082", udp))
	require.Equal(t, "[2001:db8::1]:30082", dmsgQUICAdvertisedAddr("[2001:db8::1]:30082", udp))
	require.Empty(t, dmsgQUICAdvertisedAddr("", udp), "no public host, nothing to advertise")
	require.Empty(t, dmsgQUICAdvertisedAddr(":30082", udp), "a bare port names no host")
	require.Empty(t, dmsgQUICAdvertisedAddr("45.79.124.73:30082", &net.TCPAddr{Port: 30082}))
}
