// Package vpn pkg/vpn/hello_test.go c3-app-vpn
package vpn

import (
	"encoding/json"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// The hellos are read by visors still on the net.IP structs, so both
// encodings must stay byte for byte the same.
type legacyClientHello struct {
	UnavailablePrivateIPs []net.IP `json:"unavailable_private_ips"`
}

type legacyServerHello struct {
	Status     HandshakeStatus `json:"status"`
	TUNIP      net.IP          `json:"tun_ip"`
	TUNGateway net.IP          `json:"tun_gateway"`
}

func TestHelloWireCompat(t *testing.T) {
	t.Run("server hello ok", func(t *testing.T) {
		old, err := json.Marshal(legacyServerHello{Status: HandshakeStatusOK, TUNIP: net.IPv4(192, 168, 2, 12), TUNGateway: net.IPv4(192, 168, 2, 11)})
		require.NoError(t, err)
		cur, err := json.Marshal(ServerHello{Status: HandshakeStatusOK, TUNIP: netip.MustParseAddr("192.168.2.12"), TUNGateway: netip.MustParseAddr("192.168.2.11")})
		require.NoError(t, err)
		require.JSONEq(t, string(old), string(cur))
		require.Equal(t, string(old), string(cur))

		var got ServerHello
		require.NoError(t, json.Unmarshal(old, &got))
		require.Equal(t, netip.MustParseAddr("192.168.2.12"), got.TUNIP)
		require.Equal(t, netip.MustParseAddr("192.168.2.11"), got.TUNGateway)

		var back legacyServerHello
		require.NoError(t, json.Unmarshal(cur, &back))
		require.True(t, back.TUNIP.Equal(net.IPv4(192, 168, 2, 12)))
	})

	t.Run("server hello error", func(t *testing.T) {
		old, err := json.Marshal(legacyServerHello{Status: HandshakeNoFreeIPs})
		require.NoError(t, err)
		cur, err := json.Marshal(ServerHello{Status: HandshakeNoFreeIPs})
		require.NoError(t, err)
		require.Equal(t, string(old), string(cur))

		var got ServerHello
		require.NoError(t, json.Unmarshal(old, &got))
		require.False(t, got.TUNIP.IsValid())
	})

	t.Run("client hello", func(t *testing.T) {
		old, err := json.Marshal(legacyClientHello{UnavailablePrivateIPs: []net.IP{net.IPv4(192, 168, 1, 5), net.ParseIP("10.0.0.1")}})
		require.NoError(t, err)
		cur, err := json.Marshal(ClientHello{UnavailablePrivateIPs: []netip.Addr{netip.MustParseAddr("192.168.1.5"), netip.MustParseAddr("10.0.0.1")}})
		require.NoError(t, err)
		require.Equal(t, string(old), string(cur))

		var got ClientHello
		require.NoError(t, json.Unmarshal(old, &got))
		g := NewIPGenerator()
		for _, ip := range got.UnavailablePrivateIPs {
			require.NoError(t, g.Reserve(ip))
		}
	})
}
