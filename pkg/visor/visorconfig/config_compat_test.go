// Package visorconfig pkg/visor/visorconfig/config_compat_test.go
// — pins the legacy "dmsgpty" → V1.Pty migration.
package visorconfig

import (
	"encoding/json"
	"testing"
)

// TestPtyLegacyKeyAccepted: an operator config.json carrying the
// pre-rename `"dmsgpty"` key still populates V1.Pty after the
// rename. Verifies the UnmarshalJSON fallback path in
// config_compat.go.
func TestPtyLegacyKeyAccepted(t *testing.T) {
	const raw = `{"dmsgpty": {"dmsg_port": 22, "cli_network": "unix", "cli_address": "/tmp/dmsgpty.sock", "whitelist": [], "ssh_listen": ":2022"}}`
	var v V1
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if v.Pty == nil {
		t.Fatal("V1.Pty was nil; legacy dmsgpty key did not populate it")
	}
	if v.Pty.DmsgPort != 22 {
		t.Errorf("Pty.DmsgPort = %d, want 22", v.Pty.DmsgPort)
	}
	if v.Pty.SshListen != ":2022" {
		t.Errorf("Pty.SshListen = %q, want %q", v.Pty.SshListen, ":2022")
	}
}

// TestPtyCanonicalKeyWins: when both "pty" and "dmsgpty" appear,
// the canonical "pty" key wins. Defensive — shouldn't happen in
// practice (operators have one or the other) but pin the behavior.
func TestPtyCanonicalKeyWins(t *testing.T) {
	const raw = `{"pty": {"dmsg_port": 22, "ssh_listen": ":canonical"}, "dmsgpty": {"dmsg_port": 99, "ssh_listen": ":legacy"}}`
	var v V1
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if v.Pty == nil {
		t.Fatal("V1.Pty was nil")
	}
	if v.Pty.SshListen != ":canonical" {
		t.Errorf("canonical key didn't win: SshListen = %q, want :canonical", v.Pty.SshListen)
	}
}

// TestPtyMarshalsAsPty: round-trip — set V1.Pty, marshal, confirm
// the JSON output uses the canonical "pty" key (not "dmsgpty").
func TestPtyMarshalsAsPty(t *testing.T) {
	v := V1{Pty: &Pty{DmsgPort: 22, SshListen: ":2022"}}
	data, err := json.Marshal(&v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	str := string(data)
	if !contains(str, `"pty":`) {
		t.Errorf("expected canonical \"pty\" key in output, got: %s", str)
	}
	if contains(str, `"dmsgpty":`) {
		t.Errorf("legacy \"dmsgpty\" key leaked into marshal output: %s", str)
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
