package vpn

import (
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIPFromEnv(t *testing.T) {
	const envKey = "KEY"

	want := net.IPv4(172, 104, 191, 38)

	tests := []struct {
		name   string
		envVal string
	}{
		{
			name:   "URL with port",
			envVal: "tcp://dmsg.server02a4.skywire.skycoin.com:30080",
		},
		{
			name:   "URL without port",
			envVal: "tcp://dmsg.server02a4.skywire.skycoin.com",
		},
		{
			name:   "Domain with port",
			envVal: "dmsg.server02a4.skywire.skycoin.com:30080",
		},
		{
			name:   "Domain without port",
			envVal: "dmsg.server02a4.skywire.skycoin.com",
		},
		{
			name:   "IP with port",
			envVal: "172.104.191.38:30080",
		},
		{
			name:   "IP without port",
			envVal: "172.104.191.38",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			os.Clearenv()

			err := os.Setenv(envKey, tc.envVal)
			require.NoError(t, err)

			ip, ok, err := IPFromEnv(envKey)
			require.NoError(t, err)
			require.True(t, ok)
			require.True(t, ip.Equal(want))
		})
	}
}
