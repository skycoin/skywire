package clidmsg

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestParseViaTCP pins the tcp://<pk>@host:port parsing used by the
// direct noise-TCP connection probe (`probe --via tcp://...`).
func TestParseViaTCP(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	good := "tcp://" + pk.Hex() + "@127.0.0.1:8801"

	gotPK, hostPort, err := parseViaTCP(good)
	if err != nil {
		t.Fatalf("parseViaTCP(%q) error: %v", good, err)
	}
	if gotPK != pk {
		t.Fatalf("pk = %s, want %s", gotPK, pk)
	}
	if hostPort != "127.0.0.1:8801" {
		t.Fatalf("hostPort = %q, want 127.0.0.1:8801", hostPort)
	}

	for _, bad := range []string{
		"dmsg://" + pk.Hex() + "@h:1", // wrong scheme
		"tcp://" + pk.Hex(),           // missing @host:port
		"tcp://@127.0.0.1:8801",       // empty pk
		"tcp://nothex@127.0.0.1:8801", // invalid pk
		"tcp://" + pk.Hex() + "@",     // empty host
	} {
		if _, _, err := parseViaTCP(bad); err == nil {
			t.Fatalf("parseViaTCP(%q) expected error, got nil", bad)
		}
	}
}

// TestProbeIdentity verifies that --sk derives the matching public key,
// and that without --sk a fresh ephemeral pair is returned.
func TestProbeIdentity(t *testing.T) {
	defer func() { probeSK = "" }()

	// Provided key: pk must derive from the given sk.
	pk, sk := cipher.GenerateKeyPair()
	probeSK = sk.Hex()
	gotPK, gotSK, err := probeIdentity()
	if err != nil {
		t.Fatalf("probeIdentity() with --sk error: %v", err)
	}
	if gotPK != pk {
		t.Fatalf("derived pk = %s, want %s", gotPK, pk)
	}
	if gotSK != sk {
		t.Fatalf("sk not passed through")
	}

	// Bad --sk → error.
	probeSK = "not-a-hex-key"
	if _, _, err := probeIdentity(); err == nil {
		t.Fatal("probeIdentity() with invalid --sk expected error, got nil")
	}

	// No --sk → ephemeral pair whose pk matches its sk.
	probeSK = ""
	ePK, eSK, err := probeIdentity()
	if err != nil {
		t.Fatalf("probeIdentity() ephemeral error: %v", err)
	}
	wantPK, err := eSK.PubKey()
	if err != nil {
		t.Fatalf("derive ephemeral pk: %v", err)
	}
	if ePK != wantPK {
		t.Fatal("ephemeral pk does not match its sk")
	}
}

// TestParsePortSpec covers the multi-port sweep parser.
func TestParsePortSpec(t *testing.T) {
	got, err := parsePortSpec("22,80,1000-1002,80")
	if err != nil {
		t.Fatalf("parsePortSpec error: %v", err)
	}
	want := []uint16{22, 80, 1000, 1001, 1002} // deduped + sorted
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	for _, bad := range []string{"", "abc", "10-5"} {
		if _, err := parsePortSpec(bad); err == nil {
			t.Fatalf("parsePortSpec(%q) expected error", bad)
		}
	}
}
