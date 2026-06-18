package visor

import (
	"net"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestSelfDialAsStampsPK verifies the Part-1 self-loopback-authenticated
// mechanism: SelfDialAs stamps the server-end conn's RemoteAddr so it
// stringifies to "<pk>:<port>" — exactly the form the landing-page whitelist
// extracts the caller PK from via net.SplitHostPort. Plain SelfDial leaves the
// pipe address-less (renders anonymous), which is the behavior the toggle
// switches between.
func TestSelfDialAsStampsPK(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	reg := NewServiceRegistry()

	gotAddr := make(chan net.Addr, 1)
	reg.Register(80, "test", func(conn net.Conn) {
		gotAddr <- conn.RemoteAddr()
		_ = conn.Close() //nolint:errcheck
	})

	cli, err := reg.SelfDialAs(80, pk)
	if err != nil {
		t.Fatalf("SelfDialAs: %v", err)
	}
	defer cli.Close() //nolint:errcheck

	addr := <-gotAddr
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		t.Fatalf("server-end RemoteAddr %q not host:port: %v", addr.String(), err)
	}
	if host != pk.Hex() {
		t.Errorf("stamped host = %q, want visor PK %q", host, pk.Hex())
	}
}

// TestSelfDialAnonymous confirms the un-stamped self-dial does NOT report a PK
// host — so disabling self_loopback_authenticated renders the self view as an
// anonymous caller.
func TestSelfDialAnonymous(t *testing.T) {
	reg := NewServiceRegistry()
	gotAddr := make(chan net.Addr, 1)
	reg.Register(80, "test", func(conn net.Conn) {
		gotAddr <- conn.RemoteAddr()
		_ = conn.Close() //nolint:errcheck
	})

	cli, err := reg.SelfDial(80)
	if err != nil {
		t.Fatalf("SelfDial: %v", err)
	}
	defer cli.Close() //nolint:errcheck

	addr := <-gotAddr
	var pk cipher.PubKey
	if host, _, err := net.SplitHostPort(addr.String()); err == nil {
		if perr := pk.Set(host); perr == nil {
			t.Errorf("plain SelfDial RemoteAddr %q parsed as a PK; expected address-less pipe", addr.String())
		}
	}
}
