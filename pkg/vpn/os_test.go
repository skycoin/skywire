package vpn

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCIDR(t *testing.T) {
	ip, netmask, err := parseCIDR("192.168.1.5/24")
	require.NoError(t, err)
	require.Equal(t, "192.168.1.5", ip)
	require.Equal(t, "255.255.255.0", netmask)

	ip, netmask, err = parseCIDR("10.0.0.1/29")
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", ip)
	require.Equal(t, "255.255.255.248", netmask)

	// Malformed CIDR → error.
	_, _, err = parseCIDR("not-a-cidr")
	require.Error(t, err)
}
