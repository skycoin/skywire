// Package visorconfig pkg/visor/visorconfig/config_compat_test.go
// — pins the legacy "dmsgpty" → V1.Pty migration.
package visorconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

// TestPtyUnmarshalPreservesPresetCommon: the loader pre-populates
// V1.Common before calling json.Unmarshal (see ReadRaw in read.go).
// V1.UnmarshalJSON must NOT wipe Common — visor.run panics on the
// first conf.MasterLogger() call if it does. Regression test for
// the "var alias v1Alias" zero-init mistake.
func TestPtyUnmarshalPreservesPresetCommon(t *testing.T) {
	sentinel := &Common{Version: "preset-sentinel"}
	v := V1{Common: sentinel}
	if err := json.Unmarshal([]byte(`{"pty": {"dmsg_port": 22}}`), &v); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if v.Common != sentinel {
		t.Fatalf("preset Common was overwritten by UnmarshalJSON")
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

// TestV1JSONMirrorsEveryField fails when V1 gains a JSON field that v1JSON,
// the unmarshal mirror, lacks. Such a field is dropped on every load.
func TestV1JSONMirrorsEveryField(t *testing.T) {
	tags := func(typ reflect.Type) map[string]bool {
		out := map[string]bool{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if f.Anonymous || name == "" || name == "-" {
				continue
			}
			out[name] = true
		}
		return out
	}
	mirror := tags(reflect.TypeOf(v1JSON{}))
	for name := range tags(reflect.TypeOf(V1{})) {
		require.True(t, mirror[name], "v1JSON lacks %q, so loading a config drops it", name)
	}
}

func TestHostBridgeSurvivesLoad(t *testing.T) {
	var v V1
	require.NoError(t, json.Unmarshal([]byte(`{"host_bridge":{"url":"ws://127.0.0.1:3438/t","forwards":[{"host":"127.0.0.1:1080","local":"127.0.0.1:1080"}]}}`), &v))
	require.NotNil(t, v.HostBridge)
	require.Equal(t, "ws://127.0.0.1:3438/t", v.HostBridge.URL)
	require.Equal(t, []HostBridgeForwardConfig{{Host: "127.0.0.1:1080", Local: "127.0.0.1:1080"}}, v.HostBridge.Forwards)
}
