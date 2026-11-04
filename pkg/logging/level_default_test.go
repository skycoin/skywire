// Package logging pkg/logging/level_default_test.go c0-com-log
package logging

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// An unrecognized or empty level must never mean debug: an unattended service
// whose config says "log_level": "" (or "inof") would otherwise run verbose in
// production, and the caller that logs the error and carries on would never say
// so. Info is the fallback, and the error names the offending string.
func TestLevelFromStringFallsBackToInfo(t *testing.T) {
	for _, s := range []string{"", "inof", "DEBUGG", "verbose", " debug"} {
		lvl, err := LevelFromString(s)
		require.Errorf(t, err, "level %q must not parse", s)
		require.Equalf(t, InfoLevel, lvl, "level %q must fall back to info", s)
		require.Containsf(t, err.Error(), s, "error must name the offending string %q", s)
	}

	// An operator who explicitly asks for debug still gets debug.
	lvl, err := LevelFromString("debug")
	require.NoError(t, err)
	require.Equal(t, DebugLevel, lvl)
}

// A scoped logger carries its own level: raising or lowering it leaves the
// global master logger, and any other scoped logger, untouched.
func TestScopedLoggerLevelIsIndependent(t *testing.T) {
	origOut, origLevel := log.Out, log.GetLevel()
	t.Cleanup(func() { log.Out = origOut; log.SetLevel(origLevel) })

	var buf bytes.Buffer
	SetOutputTo(&buf)
	SetLevel(InfoLevel)

	noisy := ScopedLogger("noisy", DebugLevel)
	quiet := ScopedLogger("quiet", ErrorLevel)

	require.Equal(t, DebugLevel, noisy.Level())
	require.Equal(t, ErrorLevel, quiet.Level())
	require.Equal(t, InfoLevel, GetLevel(), "the global level must not move")

	noisy.Debug("noisy-debug")
	quiet.Debug("quiet-debug")
	quiet.Error("quiet-error")
	MustGetLogger("global").Debug("global-debug")

	out := buf.String()
	require.Contains(t, out, "noisy-debug")
	require.NotContains(t, out, "quiet-debug")
	require.Contains(t, out, "quiet-error")
	require.NotContains(t, out, "global-debug")
}

// A scoped logger still writes where everything else writes.
func TestScopedLoggerSharesOutputAndHooks(t *testing.T) {
	origOut, origLevel := log.Out, log.GetLevel()
	t.Cleanup(func() { log.Out = origOut; log.SetLevel(origLevel) })

	var buf bytes.Buffer
	SetOutputTo(&buf)
	c := &capture{}
	AddHook(c)

	ScopedLogger("scoped", WarnLevel).Warn("hook-me")
	require.Contains(t, buf.String(), "hook-me")
	require.Len(t, c.entries, 1)
	require.Equal(t, "hook-me", c.entries[0].Message)
}
