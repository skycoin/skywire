// Package config cmd/dmsg/dmsg-server/commands/config/gen_test.go
//
// Covers the `dmsg server config gen` command's logic: generating a default
// dmsg-server config and flushing it to the requested output path, including
// the --testenv discovery override. The parent `commands` package (root.go) is
// pure cobra wiring with no logic to test.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsgserver"
)

// resetGenGlobals clears the command's flag-backed globals between runs.
func resetGenGlobals(t *testing.T) {
	t.Helper()
	output, testEnv = "", false
	t.Cleanup(func() { output, testEnv = "", false })
}

// TestGenConfigDefault verifies the gen command writes a parseable default
// config (prod discovery) to the requested output path.
func TestGenConfigDefault(t *testing.T) {
	resetGenGlobals(t)
	output = filepath.Join(t.TempDir(), "dmsg-config.json")

	genConfigCmd.Run(genConfigCmd, nil)

	require.FileExists(t, output)
	raw, err := os.ReadFile(output)
	require.NoError(t, err)

	var cfg dmsgserver.Config
	require.NoError(t, json.Unmarshal(raw, &cfg))
	assert.NotEmpty(t, cfg.Discovery, "default config should set a discovery URL")
}

// TestGenConfigTestEnv verifies the --testenv flag selects the test deployment
// discovery URL.
func TestGenConfigTestEnv(t *testing.T) {
	resetGenGlobals(t)
	output = filepath.Join(t.TempDir(), "dmsg-config.json")
	testEnv = true

	genConfigCmd.Run(genConfigCmd, nil)

	raw, err := os.ReadFile(output)
	require.NoError(t, err)

	var cfg dmsgserver.Config
	require.NoError(t, json.Unmarshal(raw, &cfg))
	assert.Equal(t, dmsgserver.DefaultDiscoverURLTest, cfg.Discovery)
}
