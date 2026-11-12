package netutil

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOnNetworks(t *testing.T) {
	var nets []*net.IPNet
	for _, c := range []string{"192.168.1.133/24", "fd00:add5:7735:e100::1/64"} {
		_, n, err := net.ParseCIDR(c)
		require.NoError(t, err)
		nets = append(nets, n)
	}
	hosts := []string{"192.168.1.117", "192.168.2.5", "10.0.0.7", "94.24.86.242", "fd00:add5:7735:e100:6444:bfff:fe3c:3ba2", "not an ip"}
	require.Equal(t, []string{"192.168.1.117", "fd00:add5:7735:e100:6444:bfff:fe3c:3ba2"}, OnNetworks(hosts, nets))
	require.Empty(t, OnNetworks(hosts, nil))
}
