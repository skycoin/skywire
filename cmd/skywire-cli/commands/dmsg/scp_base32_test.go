package clidmsg

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestSplitPKPathTakesABase32Key(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	got, path, ok := splitPKPath(pk.DNSLabel() + ":/etc/hostname")
	if !ok || got != pk || path != "/etc/hostname" {
		t.Fatalf("splitPKPath = %s, %q, %v; want %s, /etc/hostname, true", got.Hex(), path, ok, pk.Hex())
	}
}
