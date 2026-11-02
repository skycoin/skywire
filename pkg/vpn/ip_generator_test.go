package vpn

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetchIPv4Octets(t *testing.T) {
	octets, err := fetchIPv4Octets(netip.MustParseAddr("192.168.1.2"))
	require.NoError(t, err)
	require.Equal(t, [4]uint8{192, 168, 1, 2}, octets)

	// An IPv6 address has no v4 form → error.
	_, err = fetchIPv4Octets(netip.MustParseAddr("::1"))
	require.Error(t, err)
}

func TestIPGeneratorNext(t *testing.T) {
	g := NewIPGenerator()

	seen := make(map[string]struct{})
	for range 20 {
		ip, err := g.Next()
		require.NoError(t, err)
		require.True(t, ip.Is4(), "must be IPv4")
		first := ip.As4()[0]

		// Must fall in one of the configured private ranges.
		require.True(t, first == 192 || first == 172 || first == 10, "unexpected first octet: %v", ip)

		key := ip.String()
		_, dup := seen[key]
		require.False(t, dup, "duplicate IP generated: %s", key)
		seen[key] = struct{}{}
	}
}

func TestIPGeneratorReserve(t *testing.T) {
	g := NewIPGenerator()
	require.NoError(t, g.Reserve(netip.MustParseAddr("10.1.2.3")))

	// Reserving a non-IPv4 address fails.
	require.Error(t, g.Reserve(netip.MustParseAddr("fe80::1")))
}

func TestSubnetIPIncrementer(t *testing.T) {
	inc := newSubnetIPIncrementer([4]uint8{192, 168, 2, 0}, [4]uint8{192, 168, 255, 255}, 8)

	ip1, err := inc.next()
	require.NoError(t, err)
	require.True(t, ip1.Is4())
	require.EqualValues(t, 192, ip1.As4()[0])
	require.EqualValues(t, 168, ip1.As4()[1])

	ip2, err := inc.next()
	require.NoError(t, err)
	require.NotEqual(t, ip1.String(), ip2.String())

	// Reserving an octet set doesn't panic and is honored by future next().
	inc.reserve([4]uint8{192, 168, 2, 100})
}
