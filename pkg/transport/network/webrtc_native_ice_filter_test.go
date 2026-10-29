//go:build !(js && wasm) && (!tinygo || linux)

package network

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/netutil"
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

// The advertise policy reaches ICE: a denied interface gathers nothing, an
// allow glob brings back a virtual one, and a denied range drops its address.
func TestICEFilterFollowsPolicy(t *testing.T) {
	t.Cleanup(func() { netutil.SetAdvertisePolicy(netutil.AddrPolicy{}) })
	netutil.SetAdvertisePolicy(netutil.AddrPolicy{Allow: []string{"wg*", "eth*"}, Deny: []string{"eth1", "192.0.2.0/24"}})
	require.True(t, keepICEInterface("eth0"))
	require.False(t, keepICEInterface("eth1"), "denied by name")
	require.True(t, keepICEInterface("wg0"), "a virtual interface the allow list names")
	require.False(t, keepICEInterface("tun0"), "still virtual")
	require.False(t, keepICEIP(net.ParseIP("192.0.2.7")), "denied range")
}
