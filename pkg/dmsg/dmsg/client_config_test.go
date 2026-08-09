package dmsg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A client Config built as a literal — which is how almost every call site in
// the tree builds one — must still come out of Ensure with the CLIENT
// republish cadence, not EntityCommon.init's zero-value fallback.
//
// This is a regression guard with real history: eleven of the fifteen client
// call sites set only MinSessions, so they inherited DefaultUpdateInterval
// (1 min, the SERVER's cadence) and re-published their discovery entry five
// times more often than intended — a signed GET+PUT to dmsg-discovery over a
// fresh dmsg stream, every minute, on every visor in the fleet. Nothing
// asserted the interval, which is exactly why it went unnoticed.
func TestConfigEnsureDefaultsUpdateIntervalForLiterals(t *testing.T) {
	conf := &Config{MinSessions: 1}
	conf.Ensure()
	require.Equal(t, DefaultUpdateInterval*5, conf.UpdateInterval,
		"a Config literal must default to the client cadence, not the 1-minute server one")
}

// An explicit interval is the caller's to choose and Ensure must not overwrite
// it — including a deliberately short one.
func TestConfigEnsureKeepsExplicitUpdateInterval(t *testing.T) {
	conf := &Config{MinSessions: 1, UpdateInterval: DefaultUpdateInterval}
	conf.Ensure()
	require.Equal(t, DefaultUpdateInterval, conf.UpdateInterval)
}

// DefaultConfig and an Ensure'd literal must not disagree about the cadence —
// the two paths drifting apart is what produced the bug above.
func TestConfigEnsureAgreesWithDefaultConfig(t *testing.T) {
	literal := &Config{}
	literal.Ensure()
	require.Equal(t, DefaultConfig().UpdateInterval, literal.UpdateInterval)
}
