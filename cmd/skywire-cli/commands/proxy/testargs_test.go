// Package skysocksc cmd/skywire-cli/commands/proxy/testargs_test.go c4-vis-cli
package skysocksc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestArgsToCustomSetting pins the round trip `proxy test` relies on to put an
// operator's proxy configuration back after borrowing the default client: the
// three token forms a visor config holds — the "app <name>" subcommand pair,
// "--flag <value>" string pairs, and bare "--flag" booleans — must all survive
// as the map DoCustomSetting takes.
func TestArgsToCustomSetting(t *testing.T) {
	got := argsToCustomSetting([]string{
		"app", "skysocks-client",
		"--srv", "02328671a5c84852bba18478fa63d8193f86c2ddbfd00273f78f183b022c664af7",
		"--addr", "127.0.0.1:1095",
		"--tunnels", "3",
		"--reconnect",
		"--direct",
	})
	assert.Equal(t, map[string]any{
		"app":         "skysocks-client",
		"--srv":       "02328671a5c84852bba18478fa63d8193f86c2ddbfd00273f78f183b022c664af7",
		"--addr":      "127.0.0.1:1095",
		"--tunnels":   "3",
		"--reconnect": true,
		"--direct":    true,
	}, got)
}

// TestArgsToCustomSettingLegacyBoolForm covers configs still carrying the old
// single-dash "-flag=value" boolean form.
func TestArgsToCustomSettingLegacyBoolForm(t *testing.T) {
	got := argsToCustomSetting([]string{"--srv", "KEY", "-reconnect=true", "-direct=false"})
	assert.Equal(t, map[string]any{
		"--srv":      "KEY",
		"-reconnect": true,
		"-direct":    false,
	}, got)
}
