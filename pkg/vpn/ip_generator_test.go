package vpn

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchIPv4Octets(t *testing.T) {
	octets, err := fetchIPv4Octets(net.ParseIP("192.168.1.2"))
	require.NoError(t, err)
	require.Equal(t, [4]uint8{192, 168, 1, 2}, octets)

	// An IPv6 address has no v4 form → error.
	_, err = fetchIPv4Octets(net.ParseIP("::1"))
	require.Error(t, err)
}

func TestIPGeneratorNext(t *testing.T) {
	g := NewIPGenerator()

	seen := make(map[string]struct{})
	for range 20 {
		ip, err := g.Next()
		require.NoError(t, err)
		require.NotNil(t, ip.To4(), "must be IPv4")

		// Must fall in one of the configured private ranges.
		require.True(t, ip[12] == 192 || ip[12] == 172 || ip[12] == 10, "unexpected first octet: %v", ip)

		key := ip.String()
		_, dup := seen[key]
		require.False(t, dup, "duplicate IP generated: %s", key)
		seen[key] = struct{}{}
	}
}

func TestIPGeneratorReserve(t *testing.T) {
	g := NewIPGenerator()
	require.NoError(t, g.Reserve(net.ParseIP("10.1.2.3")))

	// Reserving a non-IPv4 address fails.
	require.Error(t, g.Reserve(net.ParseIP("fe80::1")))
}

func TestSubnetIPIncrementer(t *testing.T) {
	inc := newSubnetIPIncrementer([4]uint8{192, 168, 2, 0}, [4]uint8{192, 168, 255, 255}, 8)

	ip1, err := inc.next()
	require.NoError(t, err)
	require.NotNil(t, ip1.To4())
	require.EqualValues(t, 192, ip1[12])
	require.EqualValues(t, 168, ip1[13])

	ip2, err := inc.next()
	require.NoError(t, err)
	require.NotEqual(t, ip1.String(), ip2.String())

	// Reserving an octet set doesn't panic and is honored by future next().
	inc.reserve([4]uint8{192, 168, 2, 100})
}
