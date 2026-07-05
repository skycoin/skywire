package vpn

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestAppEnvArgs_Empty(t *testing.T) {
	require.Empty(t, AppEnvArgs(DirectRoutesEnvConfig{}))
}

func TestAppEnvArgs_Full(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	cfg := DirectRoutesEnvConfig{
		DmsgDiscovery:   "http://dmsgd",
		TPDiscovery:     "http://tpd",
		RF:              "http://rf",
		UptimeTracker:   "http://ut",
		AddressResolver: "http://ar",
		DmsgServers:     []string{"srv0", "srv1"},
		TPRemoteIPs:     []string{"1.1.1.1"},
		STCPTable:       map[cipher.PubKey]string{pk: "10.0.0.1:7777"},
	}
	envs := AppEnvArgs(cfg)

	require.Equal(t, "http://dmsgd", envs[DmsgDiscAddrEnvKey])
	require.Equal(t, "http://tpd", envs[TPDiscAddrEnvKey])
	require.Equal(t, "http://rf", envs[RFAddrEnvKey])
	require.Equal(t, "http://ut", envs[UptimeTrackerAddrEnvKey])
	require.Equal(t, "http://ar", envs[AddressResolverAddrEnvKey])

	// DmsgServers: count + indexed entries.
	require.Equal(t, "2", envs[DmsgAddrsCountEnvKey])
	require.Equal(t, "srv0", envs[DmsgAddrEnvPrefix+"0"])
	require.Equal(t, "srv1", envs[DmsgAddrEnvPrefix+"1"])

	// TPRemoteIPs: length + indexed entries.
	require.Equal(t, "1", envs[TPRemoteIPsLenEnvKey])
	require.Equal(t, "1.1.1.1", envs[TPRemoteIPsEnvPrefix+"0"])

	// STCP table: length, key-by-index, value-by-key.
	require.Equal(t, "1", envs[STCPTableLenEnvKey])
	require.Equal(t, pk.String(), envs[STCPKeyEnvPrefix+"0"])
	require.Equal(t, "10.0.0.1:7777", envs[STCPValueEnvPrefix+pk.String()])
}

func TestParseIP(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		ip, ok, err := ParseIP("")
		require.NoError(t, err)
		require.False(t, ok)
		require.Nil(t, ip)
	})

	t.Run("bare IP", func(t *testing.T) {
		ip, ok, err := ParseIP("192.168.1.1")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, "192.168.1.1", ip.String())
	})

	t.Run("IP with port", func(t *testing.T) {
		ip, ok, err := ParseIP("192.168.1.1:8080")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, "192.168.1.1", ip.String())
	})

	t.Run("full URL with port", func(t *testing.T) {
		ip, ok, err := ParseIP("http://1.2.3.4:80")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, "1.2.3.4", ip.String())
	})
}

func TestIPFromEnv(t *testing.T) {
	const key = "VPN_TEST_IP_ENV"

	t.Setenv(key, "192.168.1.1")
	ip, ok, err := IPFromEnv(key)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "192.168.1.1", ip.String())

	t.Setenv(key, "")
	ip, ok, err = IPFromEnv(key)
	require.NoError(t, err)
	require.False(t, ok)
	require.Nil(t, ip)
}
