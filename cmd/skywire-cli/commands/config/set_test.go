package cliconfig

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor"
)

func TestParseSetArgs(t *testing.T) {
	got, err := parseSetArgs([]string{
		"is_public=true",
		"log_level=debug",
		"transport.transport_port=7777",
		`launcher.apps[skysocks].auto_start=false`,
		`routing.transport_preference=["stcpr","dmsg"]`,
		`launcher.apps[skysocks].args=`,
	})
	require.NoError(t, err)
	require.Equal(t, map[string]json.RawMessage{
		"is_public":                          json.RawMessage(`true`),
		"log_level":                          json.RawMessage(`"debug"`),
		"transport.transport_port":           json.RawMessage(`7777`),
		"launcher.apps[skysocks].auto_start": json.RawMessage(`false`),
		"routing.transport_preference":       json.RawMessage(`["stcpr","dmsg"]`),
		// An empty value is not valid JSON, so it becomes the empty string.
		"launcher.apps[skysocks].args": json.RawMessage(`""`),
	}, got)
}

func TestParseSetArgsErrors(t *testing.T) {
	for _, tc := range []struct{ arg, want string }{
		{"is_public", "expected <dotted.path>=<value>"},
		{"=true", "expected <dotted.path>=<value>"},
		{"   =true", "empty path"},
	} {
		_, err := parseSetArgs([]string{tc.arg})
		require.Error(t, err, tc.arg)
		require.Contains(t, err.Error(), tc.want, tc.arg)
	}
	_, err := parseSetArgs([]string{"is_public=true", "is_public=false"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "given twice")
}

// TestLiveFieldHelp: the command help renders the live table from pkg/visor, so
// the documented list is the implemented list.
func TestLiveFieldHelp(t *testing.T) {
	help := liveFieldHelp()
	require.NotEmpty(t, help)
	for _, f := range visor.LiveConfigFields() {
		require.Contains(t, help, f.Path)
		require.Contains(t, help, f.Desc)
	}
	require.Contains(t, setCmd.Long, "is_public")
	require.Contains(t, setCmd.Long, "restart-required")
}
