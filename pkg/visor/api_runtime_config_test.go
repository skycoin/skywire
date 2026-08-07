package visor

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestRedactTopLevelSK(t *testing.T) {
	in := []byte("{\n  \"version\": \"v1.0.0\",\n  \"sk\": \"deadbeef\",\n  \"pk\": \"00\",\n  \"dmsg\": {\n    \"sk\": \"nested-untouched\"\n  }\n}")
	out := string(redactTopLevelSK(in))
	require.NotContains(t, out, "deadbeef")                  // top-level sk line removed
	require.Contains(t, out, "\"version\": \"v1.0.0\"")      // sibling fields intact
	require.Contains(t, out, "\"pk\": \"00\"")               // PK kept
	require.Contains(t, out, "\"sk\": \"nested-untouched\"") // deeper-indented sk left alone
}

// TestSetRuntimeConfig_SKRedactionRoundTrip verifies the secret key never leaves
// the process via GetRuntimeConfig, that an edit round-tripped from that redacted
// view preserves the real key on disk, and that editing the PK is rejected.
func TestSetRuntimeConfig_SKRedactionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skywire-config.json")

	_, sk := cipher.GenerateKeyPair()
	common, err := visorconfig.NewCommon(nil, path, &sk)
	require.NoError(t, err)
	conf := &visorconfig.V1{Common: common}

	initial, err := json.MarshalIndent(conf, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, initial, 0o600))

	v := &Visor{conf: conf}

	// GET: SK redacted, PK kept.
	got, err := v.GetRuntimeConfig()
	require.NoError(t, err)
	require.NotContains(t, string(got), sk.Hex(), "real SK must not be exposed")
	require.NotContains(t, string(got), "\"sk\":", "SK line must be removed, not decodable-blank")
	require.Contains(t, string(got), conf.PK.Hex(), "PK must be present")

	// SET the redacted view back (blank sk, unchanged pk): real SK preserved on disk.
	require.NoError(t, v.SetRuntimeConfig(got))
	onDisk, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	require.Contains(t, string(onDisk), sk.Hex(), "real SK must be re-injected on save")

	// SET with a changed PK is rejected.
	_, otherSK := cipher.GenerateKeyPair()
	otherCommon, err := visorconfig.NewCommon(nil, path, &otherSK)
	require.NoError(t, err)
	tampered := bytes.Replace(got, []byte(conf.PK.Hex()), []byte(otherCommon.PK.Hex()), 1)
	err = v.SetRuntimeConfig(tampered)
	require.Error(t, err)
	require.Contains(t, err.Error(), "public key is not editable")
}
