// Package commands cmd/apps/skychat/commands/cxo_tcp_test.go
//
// Unit coverage for the CXO-over-TCP peer-spec parser.
package commands

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestParseCXOPeer(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	// parseCXOPeer only trims the optional tcp:// prefix, so both the
	// prefixed and bare forms are valid.
	for _, spec := range []string{
		"tcp://" + pk.Hex() + "@1.2.3.4:8802",
		pk.Hex() + "@1.2.3.4:8802",
	} {
		gotPK, addr, err := parseCXOPeer(spec)
		if err != nil {
			t.Fatalf("valid %q: %v", spec, err)
		}
		if gotPK != pk || addr != "1.2.3.4:8802" {
			t.Errorf("%q -> pk=%s addr=%q, want %s / 1.2.3.4:8802", spec, gotPK.Hex(), addr, pk.Hex())
		}
	}

	bad := []string{
		"tcp://" + pk.Hex(),       // missing @
		"tcp://@1.2.3.4:8802",     // empty feed pk
		"tcp://zzzz@1.2.3.4:8802", // bad pk hex
		"tcp://" + pk.Hex() + "@", // missing host:port
	}
	for _, b := range bad {
		if _, _, err := parseCXOPeer(b); err == nil {
			t.Errorf("spec %q should error", b)
		}
	}
}
