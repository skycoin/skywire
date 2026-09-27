package proxyenv

import "testing"

func TestFor(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	desk := env(map[string]string{
		"ALL_PROXY": "socks5h://127.0.0.1:4445",
		"NO_PROXY":  "localhost,127.0.0.1,::1,vnet",
	})
	for _, tc := range []struct {
		name   string
		getenv func(string) string
		url    string
		want   string
	}{
		{"ALL_PROXY covers https", desk, "https://ip.skycoin.com/", "socks5h://127.0.0.1:4445"},
		{"ALL_PROXY covers mesh names", desk, "http://skywire.dmsg/", "socks5h://127.0.0.1:4445"},
		{"NO_PROXY exempts loopback", desk, "http://127.0.0.1:8001/api", ""},
		{"NO_PROXY exempts localhost", desk, "http://localhost:4445/", ""},
		{"NO_PROXY exempts vnet and <port>.vnet", desk, "http://8001.vnet/", ""},
		{"NO_PROXY exempts bracketed IPv6", desk, "http://[::1]:80/", ""},
		{"nothing set", env(nil), "https://example.com/", ""},
		{"https_proxy wins over ALL_PROXY", env(map[string]string{
			"https_proxy": "socks5h://127.0.0.1:1081", "ALL_PROXY": "socks5h://127.0.0.1:4445",
		}), "https://example.com/", "socks5h://127.0.0.1:1081"},
		{"HTTPS_PROXY is read", env(map[string]string{"HTTPS_PROXY": "http://p:3128"}), "https://example.com/", "http://p:3128"},
		{"HTTP_PROXY is ignored, as curl does", env(map[string]string{
			"HTTP_PROXY": "http://evil:1", "ALL_PROXY": "socks5h://127.0.0.1:4445",
		}), "http://example.com/", "socks5h://127.0.0.1:4445"},
		{"http_proxy is read for http", env(map[string]string{"http_proxy": "http://p:3128"}), "http://example.com/", "http://p:3128"},
		{"NO_PROXY suffix covers subdomains", env(map[string]string{
			"ALL_PROXY": "socks5h://127.0.0.1:4445", "NO_PROXY": ".example.com",
		}), "https://www.example.com/", ""},
		{"NO_PROXY suffix is label-aligned", env(map[string]string{
			"ALL_PROXY": "socks5h://127.0.0.1:4445", "NO_PROXY": "example.com",
		}), "https://notexample.com/", "socks5h://127.0.0.1:4445"},
		{"NO_PROXY star", env(map[string]string{"ALL_PROXY": "x:1", "no_proxy": "*"}), "https://a.b/", ""},
		{"not a URL", desk, "::nope", ""},
	} {
		if got := For(tc.url, tc.getenv); got != tc.want {
			t.Errorf("%s: For(%q) = %q, want %q", tc.name, tc.url, got, tc.want)
		}
	}
}
