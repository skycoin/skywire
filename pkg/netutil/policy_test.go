package netutil

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddrPolicyPermits(t *testing.T) {
	ip := net.ParseIP
	cases := []struct {
		name   string
		p      AddrPolicy
		ifname string
		addr   string
		want   bool
	}{
		{"zero allows all", AddrPolicy{}, "eth0", "192.168.1.5", true},
		{"glob deny", AddrPolicy{Deny: []string{"wlan*"}}, "wlan0", "192.168.1.5", false},
		{"glob deny other iface", AddrPolicy{Deny: []string{"wlan*"}}, "eth0", "192.168.1.5", true},
		{"cidr deny", AddrPolicy{Deny: []string{"10.0.0.0/8"}}, "eth0", "10.1.2.3", false},
		{"bare ip deny", AddrPolicy{Deny: []string{"192.168.1.5"}}, "eth0", "192.168.1.5", false},
		{"bare ip deny other", AddrPolicy{Deny: []string{"192.168.1.5"}}, "eth0", "192.168.1.6", true},
		{"v6 cidr deny", AddrPolicy{Deny: []string{"2001:db8::/32"}}, "eth0", "2001:db8::1", false},
		{"allow glob", AddrPolicy{Allow: []string{"eth*"}}, "eth0", "192.168.1.5", true},
		{"allow glob excludes", AddrPolicy{Allow: []string{"eth*"}}, "wlan0", "192.168.1.5", false},
		{"allow cidr", AddrPolicy{Allow: []string{"192.168.0.0/16"}}, "wlan0", "192.168.1.5", true},
		{"allow cidr excludes", AddrPolicy{Allow: []string{"192.168.0.0/16"}}, "wlan0", "10.0.0.1", false},
		{"mixed allow, either matches", AddrPolicy{Allow: []string{"eth*", "10.0.0.0/8"}}, "wlan0", "10.0.0.1", true},
		{"deny wins over allow", AddrPolicy{Allow: []string{"eth*"}, Deny: []string{"192.168.1.0/24"}}, "eth0", "192.168.1.5", false},
		{"deny glob wins over allow cidr", AddrPolicy{Allow: []string{"192.168.0.0/16"}, Deny: []string{"eth0"}}, "eth0", "192.168.1.5", false},
		{"unknown iface, cidr still applies", AddrPolicy{Deny: []string{"10.0.0.0/8"}}, "", "10.0.0.1", false},
		{"unknown iface, glob cannot match", AddrPolicy{Deny: []string{"*"}}, "", "10.0.0.1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, c.p.Permits(c.ifname, ip(c.addr)))
		})
	}
}

func TestAddrPolicyValidate(t *testing.T) {
	for _, ok := range []string{"wlan*", "eth0", "rmnet_data?", "10.0.0.0/8", "192.168.1.5", "fd00::/8", "::1"} {
		require.NoErrorf(t, AddrPolicy{Deny: []string{ok}}.Validate(), "%q", ok)
	}
	for _, bad := range []string{"", " ", "10.0.0.0/33", "eth[", "300.1.1.1/8", "fe80::zz"} {
		require.Errorf(t, AddrPolicy{Allow: []string{bad}}.Validate(), "%q", bad)
	}
}

func TestAddrPolicyNamedVirtual(t *testing.T) {
	p := AddrPolicy{Allow: []string{"wg*", "10.0.0.0/8"}, Deny: []string{"docker*"}}
	require.True(t, p.AllowsNamed("wg0"))
	require.False(t, p.AllowsNamed("tun0"), "a CIDR allow does not name an interface")
	require.True(t, p.DeniesInterface("docker0"))
	require.False(t, p.DeniesInterface("eth0"))
}

func TestAdvertisePolicyInstall(t *testing.T) {
	t.Cleanup(func() { SetAdvertisePolicy(AddrPolicy{}) })
	require.True(t, AdvertisePolicy().IsZero())
	deny := []string{"wlan*"}
	SetAdvertisePolicy(AddrPolicy{Deny: deny})
	deny[0] = "changed"
	require.Equal(t, []string{"wlan*"}, AdvertisePolicy().Deny, "the installed policy is a copy")
	require.True(t, PolicyPermitsIP(net.ParseIP("127.0.0.1")), "loopback is on lo, not wlan")
}

// LocalAddressesDetail reports what a deny rule removed instead of dropping it
// silently, and the two lists partition the unfiltered set.
func TestLocalAddressesDetailPartition(t *testing.T) {
	t.Cleanup(func() { SetAdvertisePolicy(AddrPolicy{}) })
	all, _, err := LocalAddressesDetail()
	require.NoError(t, err)
	if len(all) == 0 {
		t.Skip("no advertisable address on this host")
	}
	SetAdvertisePolicy(AddrPolicy{Deny: []string{all[0]}})
	kept, excluded, err := LocalAddressesDetail()
	require.NoError(t, err)
	require.NotContains(t, kept, all[0])
	require.Len(t, excluded, 1)
	require.Contains(t, excluded[0], all[0]+" (")
	require.Equal(t, len(all), len(kept)+len(excluded))
}
