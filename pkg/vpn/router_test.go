package vpn

import (
	"net"
	"strings"
	"testing"
)

func mustCIDR(t *testing.T, s string) (net.IP, *net.IPNet) {
	t.Helper()
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return ip, n
}

func TestRouterConfigValidate(t *testing.T) {
	gw, subnet := mustCIDR(t, "192.168.42.1/24")
	base := func() RouterConfig {
		return RouterConfig{LANInterface: "eth1", Gateway: gw, Subnet: subnet}
	}

	tests := []struct {
		name    string
		mutate  func(c *RouterConfig)
		wantErr bool
	}{
		{"valid ethernet", func(_ *RouterConfig) {}, false},
		{"no LAN interface", func(c *RouterConfig) { c.LANInterface = "" }, true},
		{"no gateway", func(c *RouterConfig) { c.Gateway = nil }, true},
		{"no subnet", func(c *RouterConfig) { c.Subnet = nil }, true},
		{"gateway outside subnet", func(c *RouterConfig) { c.Gateway = net.ParseIP("10.0.0.1") }, true},
		{"dhcp start outside subnet", func(c *RouterConfig) { c.DHCPStart = net.ParseIP("10.0.0.5") }, true},
		{"valid wifi wpa2", func(c *RouterConfig) {
			c.WiFi = &WiFiConfig{SSID: "sky", Passphrase: "supersecret", Band: "2.4"}
		}, false},
		{"wifi no ssid", func(c *RouterConfig) { c.WiFi = &WiFiConfig{Passphrase: "supersecret"} }, true},
		{"wifi empty passphrase not allowed", func(c *RouterConfig) { c.WiFi = &WiFiConfig{SSID: "sky"} }, true},
		{"wifi open allowed", func(c *RouterConfig) { c.WiFi = &WiFiConfig{SSID: "sky", AllowOpen: true} }, false},
		{"wifi passphrase too short", func(c *RouterConfig) { c.WiFi = &WiFiConfig{SSID: "sky", Passphrase: "short"} }, true},
		{"wifi bad band", func(c *RouterConfig) {
			c.WiFi = &WiFiConfig{SSID: "sky", Passphrase: "supersecret", Band: "6"}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := base()
			tt.mutate(&c)
			err := c.withDefaults().validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultsFromSubnet(t *testing.T) {
	gw, subnet := mustCIDR(t, "192.168.42.1/24")
	c := RouterConfig{LANInterface: "eth1", Gateway: gw, Subnet: subnet}.withDefaults()
	if got := c.DHCPStart.String(); got != "192.168.42.10" {
		t.Errorf("DHCPStart = %s, want 192.168.42.10", got)
	}
	if got := c.DHCPEnd.String(); got != "192.168.42.254" {
		t.Errorf("DHCPEnd = %s, want 192.168.42.254", got)
	}
	if got := c.DNS.String(); got != "192.168.42.1" {
		t.Errorf("DNS defaulted to %s, want the gateway 192.168.42.1", got)
	}
	if c.LeaseTime != defaultLeaseTime {
		t.Errorf("LeaseTime = %q, want %q", c.LeaseTime, defaultLeaseTime)
	}
}

func TestDnsmasqConf(t *testing.T) {
	gw, subnet := mustCIDR(t, "192.168.42.1/24")
	c := RouterConfig{LANInterface: "wlan0", Gateway: gw, Subnet: subnet}
	conf := c.DnsmasqConf()
	for _, want := range []string{
		"interface=wlan0",
		"dhcp-range=192.168.42.10,192.168.42.254,12h",
		"dhcp-option=option:router,192.168.42.1",
		"dhcp-option=option:dns-server,192.168.42.1",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("dnsmasq.conf missing %q\n---\n%s", want, conf)
		}
	}
}

func TestHostapdConf(t *testing.T) {
	w := WiFiConfig{SSID: "SkywireVPN", Passphrase: "supersecret", Band: "5", Channel: 0, CountryCode: "DE"}
	conf := w.HostapdConf("wlan0")
	for _, want := range []string{
		"interface=wlan0",
		"driver=nl80211",
		"ssid=SkywireVPN",
		"country_code=DE",
		"hw_mode=a",  // 5 GHz
		"channel=36", // default for 5 GHz
		"wpa=2",
		"wpa_passphrase=supersecret",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("hostapd.conf missing %q\n---\n%s", want, conf)
		}
	}

	// 2.4 GHz open network: hw_mode g, default channel 6, no WPA block.
	open := WiFiConfig{SSID: "Open", Band: "2.4", AllowOpen: true}
	oc := open.HostapdConf("wlan0")
	if !strings.Contains(oc, "hw_mode=g") || !strings.Contains(oc, "channel=6") {
		t.Errorf("open 2.4GHz conf wrong hw_mode/channel\n%s", oc)
	}
	if strings.Contains(oc, "wpa=") {
		t.Errorf("open network should have no wpa block\n%s", oc)
	}
}

func TestNthHost(t *testing.T) {
	_, subnet := mustCIDR(t, "192.168.42.0/24")
	if got := nthHost(subnet, 10); got == nil || got.String() != "192.168.42.10" {
		t.Errorf("nthHost(.../24, 10) = %v, want 192.168.42.10", got)
	}
	// overflow past the /24 returns nil
	if got := nthHost(subnet, 300); got != nil {
		t.Errorf("nthHost(.../24, 300) = %v, want nil (overflow)", got)
	}
}
