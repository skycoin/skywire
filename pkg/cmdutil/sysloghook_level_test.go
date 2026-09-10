// Package cmdutil pkg/cmdutil/sysloghook_level_test.go c0-com-util
package cmdutil

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// The syslog-aware parser must agree with pkg/logging: an unrecognized or
// empty level is an error and yields info, never debug.
func TestLevelFromStringFallsBackToInfo(t *testing.T) {
	for _, s := range []string{"", "inof", "DEBUGG"} {
		lvl, _, err := LevelFromString(s)
		require.Errorf(t, err, "level %q must not parse", s)
		require.ErrorIs(t, err, ErrInvalidLogString)
		require.Equalf(t, logrus.InfoLevel, lvl, "level %q must fall back to info", s)
		require.Containsf(t, err.Error(), s, "error must name the offending string %q", s)
	}

	for _, s := range []string{"debug", "info", "warn", "error", "fatal", "panic", "trace"} {
		lvl, _, err := LevelFromString(s)
		require.NoErrorf(t, err, "level %q must parse", s)
		want, err := logging.LevelFromString(s)
		require.NoError(t, err)
		require.Equalf(t, want, lvl, "level %q must match pkg/logging", s)
	}
}

// The default for --syslog-lvl is info. It used to be debug, which meant an
// operator who never touched the flag ran the service verbose.
func TestServiceFlagsDefaultLogLevelIsInfo(t *testing.T) {
	var sf ServiceFlags
	sf.Init(&cobra.Command{}, "test_tag", "")
	require.Equal(t, "info", sf.LogLevel)
	require.NoError(t, sf.Check())
}
