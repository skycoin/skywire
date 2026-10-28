//go:build !(js && wasm) && (!tinygo || linux)

package network

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// The ICE filter and address resolver registration share one list.
func TestICEFilterSharesVirtualList(t *testing.T) {
	for _, n := range []string{"skydns0", "tun0", "wg0", "tailscale0", "utun2", "zt0", "docker0", "virbr0"} {
		require.Falsef(t, keepICEInterface(n), "%s must not carry ICE candidates", n)
	}
	for _, n := range []string{"eth0", "wlan0", "enp3s0", "rmnet_data0"} {
		require.Truef(t, keepICEInterface(n), "%s should carry ICE candidates", n)
	}
	require.False(t, keepICEIP(net.ParseIP("192.0.0.4")), "CLAT address")
	require.True(t, keepICEIP(net.ParseIP("192.168.1.20")))
}
