// Package dmsgsrv pkg/services/dmsgsrv/wstls_test.go c2-vis-appsvc
package dmsgsrv

import (
	"net"
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
)

// TestServeWSTLS_SkipsWithoutAHostOrAddress: the built-in TLS front is
// meaningless without a host to get a certificate for, so it must decline
// rather than bind a listener that can never complete a handshake.
func TestServeWSTLS_SkipsWithoutAHostOrAddress(t *testing.T) {
	log := logging.MustGetLogger("wstls_test")
	for _, tc := range []struct{ name, addr, cache, host string }{
		{"no address", "", "cache", "a.example.com"},
		{"no host", "127.0.0.1:0", "cache", ""},
		{"no cache dir", "127.0.0.1:0", "", "a.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if lis := ServeWSTLS(log, nil, tc.addr, tc.cache, tc.host, "wss://a.example.com/dmsg"); lis != nil {
				_ = lis.Close() //nolint:errcheck
				t.Fatal("bound a listener that cannot serve a certificate")
			}
		})
	}
}

// TestServeWSTLS_UnbindableAddressIsNotFatal: a host whose :443 already
// belongs to a reverse proxy must keep running with that external front,
// not lose its dmsg server. The nil return is what tells the caller there
// is nothing to close.
func TestServeWSTLS_UnbindableAddressIsNotFatal(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer occupied.Close() //nolint:errcheck

	log := logging.MustGetLogger("wstls_test")
	if lis := ServeWSTLS(log, nil, occupied.Addr().String(), t.TempDir(), "a.example.com", "wss://a.example.com/dmsg"); lis != nil {
		_ = lis.Close() //nolint:errcheck
		t.Fatal("bound an address another process already owns")
	}
}
