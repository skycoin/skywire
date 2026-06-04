package commands

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestParseViaTCP pins the tcp://<pk>@host:port parsing used by the
// standalone probe's --via direct-connection mode.
func TestParseViaTCP(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	gotPK, hostPort, err := parseViaTCP("tcp://" + pk.Hex() + "@127.0.0.1:8801")
	if err != nil {
		t.Fatalf("parseViaTCP error: %v", err)
	}
	if gotPK != pk {
		t.Fatalf("pk = %s, want %s", gotPK, pk)
	}
	if hostPort != "127.0.0.1:8801" {
		t.Fatalf("hostPort = %q, want 127.0.0.1:8801", hostPort)
	}

	for _, bad := range []string{
		"dmsg://" + pk.Hex() + "@h:1",
		"tcp://" + pk.Hex(),
		"tcp://@127.0.0.1:8801",
		"tcp://nothex@127.0.0.1:8801",
	} {
		if _, _, err := parseViaTCP(bad); err == nil {
			t.Fatalf("parseViaTCP(%q) expected error", bad)
		}
	}
}
