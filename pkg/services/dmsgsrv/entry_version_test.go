// Package dmsgsrv pkg/services/dmsgsrv/entry_version_test.go c2-vis-appsvc
package dmsgsrv

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/buildinfo"
)

// TestEntryVersion_MatchesTheBuild: a dmsg server's discovery entry must carry
// the same version string the binary reports, whether the server runs
// standalone or folded into a visor. Six of nine production servers advertised
// the bare "0.0.1" default on 2026-09-13 because the folded path built its
// config from dmsg.DefaultServerConfig() and never set one — so a deployment
// audit could not tell which servers had picked up a fix.
func TestEntryVersion_MatchesTheBuild(t *testing.T) {
	got := EntryVersion()
	require.Equal(t, dmsgEntryVersion(), got, "the exported form must not diverge from the internal one")

	if v := buildinfo.Get().Version; v != "" && v != "unknown" {
		require.Equal(t, v, got, "a real build must advertise its own version")
		require.NotEqual(t, "0.0.1", got)
		return
	}
	// A dev build with no stamped version deliberately reports "", which leaves
	// the entity's own default in place rather than inventing one.
	require.Empty(t, got)
}
