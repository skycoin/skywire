package got

import (
	"testing"
)

func TestParseProxyAddr(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		wantAddr       string
		wantResolve    bool
		wantErrContain string
	}{
		{"bare host:port treated as socks5h (back-compat)", "127.0.0.1:4445", "127.0.0.1:4445", false, ""},
		{"socks5h scheme — proxy resolves", "socks5h://127.0.0.1:4445", "127.0.0.1:4445", false, ""},
		{"socks5 scheme — client resolves", "socks5://127.0.0.1:4445", "127.0.0.1:4445", true, ""},
		{"scheme case-insensitive", "SOCKS5H://127.0.0.1:4445", "127.0.0.1:4445", false, ""},
		{"hostname proxy ok", "socks5h://proxy.example.com:1080", "proxy.example.com:1080", false, ""},
		{"missing host", "socks5h://", "", false, "no host"},
		{"http scheme rejected", "http://127.0.0.1:8080", "", false, "unsupported proxy scheme"},
		{"unparseable", "socks5h://[::bad", "", false, "parse proxy address"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, resolve, err := parseProxyAddr(c.in)
			if c.wantErrContain != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil (addr=%q resolve=%v)", c.wantErrContain, addr, resolve)
				}
				if !contains(err.Error(), c.wantErrContain) {
					t.Fatalf("want error containing %q, got %q", c.wantErrContain, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if addr != c.wantAddr {
				t.Errorf("addr: want %q, got %q", c.wantAddr, addr)
			}
			if resolve != c.wantResolve {
				t.Errorf("resolveLocally: want %v, got %v", c.wantResolve, resolve)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
