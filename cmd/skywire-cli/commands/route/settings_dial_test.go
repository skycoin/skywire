// Package cliroute cmd/skywire-cli/commands/route/settings_dial_test.go
package cliroute

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitCommaList(t *testing.T) {
	require.Equal(t, []string{"a", "b"}, splitCommaList("a,b"))
	require.Equal(t, []string{"a", "b"}, splitCommaList(" a , b ,"), "a trailing comma is not an error")
	require.Empty(t, splitCommaList(""))
	require.Empty(t, splitCommaList(",,"))
}

// `route settings dial` is a subcommand of `route settings`, and every knob it
// owns has a flag.
func TestDialSettingsCommandIsRegistered(t *testing.T) {
	var found bool
	for _, c := range settingsCmd.Commands() {
		if c.Name() == "dial" {
			found = true
			break
		}
	}
	require.True(t, found, "`route settings dial` must be registered")

	for _, name := range dialSettingsFlags {
		require.NotNil(t, dialSettingsCmd.Flags().Lookup(name), "missing flag --"+name)
	}
}
