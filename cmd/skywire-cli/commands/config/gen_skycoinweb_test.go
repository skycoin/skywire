package cliconfig

import (
	"strings"
	"testing"
)

func TestSkycoinWebFlags(t *testing.T) {
	saved := []any{skycoinWebAddr, skycoinWebNodeURLs, skycoinWebWalletDir, enableDmsgWeb, enableSkynetWeb, isTestEnv}
	t.Cleanup(func() {
		skycoinWebAddr = saved[0].(string)
		skycoinWebNodeURLs = saved[1].(string)
		skycoinWebWalletDir = saved[2].(string)
		enableDmsgWeb = saved[3].(bool)
		enableSkynetWeb = saved[4].(bool)
		isTestEnv = saved[5].(bool)
	})
	skycoinWebNodeURLs, isTestEnv = "", false
	skycoinWebAddr, skycoinWebWalletDir = skycoinWebNoPort, "/opt/skywire/wallets"
	enableDmsgWeb, enableSkynetWeb = true, true

	internal := strings.Join(skycoinWebFlags(false), " ")
	for _, want := range []string{"--no-listen", "--wallet-dir /opt/skywire/wallets", "--socks5-proxy socks5://127.0.0.1:4445", ".dmsg:6420"} {
		if !strings.Contains(internal, want) {
			t.Errorf("internal flags %q lack %q", internal, want)
		}
	}
	if strings.Contains(internal, "--btc-electrum-url") {
		t.Errorf("skycoin-web has its own electrum default, got %q", internal)
	}
	if external := strings.Join(skycoinWebFlags(true), " "); !strings.Contains(external, "--host 127.0.0.1 --port 8002") || strings.Contains(external, "--no-listen") {
		t.Errorf("an external process needs a port, got %q", external)
	}

	skycoinWebAddr = "127.0.0.1:9000"
	if got := strings.Join(skycoinWebFlags(false), " "); !strings.Contains(got, "--host 127.0.0.1 --port 9000") {
		t.Errorf("a configured address should open that port, got %q", got)
	}
}

func TestSetFlag(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"--no-listen"}, "--no-listen --btc-electrum-url none"},
		{[]string{"--btc-electrum-url", "default", "--no-listen"}, "--btc-electrum-url none --no-listen"},
		{[]string{"--btc-electrum-url=default"}, "--btc-electrum-url=none"},
	} {
		if got := strings.Join(setFlag(tc.in, "--btc-electrum-url", "none"), " "); got != tc.want {
			t.Errorf("setFlag(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
