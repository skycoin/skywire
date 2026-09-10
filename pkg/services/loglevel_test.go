// Package services pkg/services/loglevel_test.go c2-vis-appsvc
package services

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// restoreLogging puts the process-global logger back where the rest of the
// suite expects it.
func restoreLogging(t *testing.T) *bytes.Buffer {
	t.Helper()
	lvl := logging.GetLevel()
	hostedBefore := hosted.Load()
	t.Cleanup(func() {
		logging.SetOutputTo(os.Stderr)
		logging.SetLevel(lvl)
		hosted.Store(hostedBefore)
	})
	var buf bytes.Buffer
	logging.SetOutputTo(&buf)
	return &buf
}

// A misspelled or empty log_level must land on info, not debug, and must say
// so — the failure mode that put a production service into debug silently.
func TestNewLoggerBadLevelWarnsAndUsesInfo(t *testing.T) {
	for _, bad := range []string{"inof", "DEBUGG", "verbose"} {
		buf := restoreLogging(t)
		setHosted(1)
		logging.SetLevel(logrus.DebugLevel)

		logger := NewLogger("tag_"+bad[:4], bad)
		require.Equal(t, logrus.InfoLevel, logger.Level(), "bad level %q must fall back to info", bad)
		require.Equal(t, logrus.InfoLevel, logging.GetLevel())

		out := buf.String()
		require.Contains(t, out, bad, "the warning must name the offending string")
		require.Contains(t, out, "info", "the warning must name the level actually used")
		require.Contains(t, strings.ToLower(out), "warn", "the complaint must be logged at warn")
	}

	// Empty means "not configured": leave the level alone rather than
	// pretending the operator asked for something.
	buf := restoreLogging(t)
	setHosted(1)
	logging.SetLevel(logrus.WarnLevel)
	require.Equal(t, logrus.WarnLevel, NewLogger("empty_level", "").Level())
	require.Empty(t, buf.String())
}

// An operator explicitly asking for debug still gets debug.
func TestNewLoggerExplicitDebugUnchanged(t *testing.T) {
	restoreLogging(t)
	setHosted(1)
	logging.SetLevel(logrus.InfoLevel)

	logger := NewLogger("noisy_service", "debug")
	require.Equal(t, logrus.DebugLevel, logger.Level())
	require.Equal(t, logrus.DebugLevel, logging.GetLevel(),
		"a single-service binary still sets the process-global level, so library packages follow")
}

// Two services hosted in one process keep independent levels: neither the
// order they are built in nor one block set to "debug" decides what the other
// service emits.
func TestNewLoggerTwoServicesKeepDifferentLevels(t *testing.T) {
	buf := restoreLogging(t)
	setHosted(2)
	logging.SetLevel(logrus.WarnLevel)

	noisy := NewLogger("service_noisy", "debug")
	quiet := NewLogger("service_quiet", "error")

	require.Equal(t, logrus.DebugLevel, noisy.Level())
	require.Equal(t, logrus.ErrorLevel, quiet.Level())
	require.Equal(t, logrus.WarnLevel, logging.GetLevel(),
		"a hosted service must not move the process-global level")

	buf.Reset()
	noisy.Debug("noisy-debug")
	quiet.Debug("quiet-debug")
	quiet.Error("quiet-error")

	out := buf.String()
	require.Contains(t, out, "noisy-debug")
	require.NotContains(t, out, "quiet-debug", "one block asking for debug must not make the other verbose")
	require.Contains(t, out, "quiet-error")

	// Building them the other way round gives the same answer.
	quiet2 := NewLogger("service_quiet2", "error")
	require.Equal(t, logrus.DebugLevel, noisy.Level())
	require.Equal(t, logrus.ErrorLevel, quiet2.Level())
}

// The shared (library) level the supervisor picks is the quietest any block
// asked for, and does not depend on block order.
func TestSharedLogLevel(t *testing.T) {
	restoreLogging(t)
	log := logging.MustGetLogger("test-supervisor")

	file, err := ParseFile([]byte(`{"services":[
		{"type":"noop","name":"a","log_level":"debug"},
		{"type":"noop","name":"b","log_level":"error"},
		{"type":"noop","name":"c"}
	]}`))
	require.NoError(t, err)
	lvl, ok := sharedLogLevel(log, file)
	require.True(t, ok)
	require.Equal(t, logrus.ErrorLevel, lvl)

	reversed := File{Services: []Block{file.Services[2], file.Services[1], file.Services[0]}}
	lvl, ok = sharedLogLevel(log, reversed)
	require.True(t, ok)
	require.Equal(t, logrus.ErrorLevel, lvl)

	// No block configured a level: leave the global alone.
	none, err := ParseFile([]byte(`{"services":[{"type":"noop","name":"a"}]}`))
	require.NoError(t, err)
	_, ok = sharedLogLevel(log, none)
	require.False(t, ok)

	// A misspelled level counts as info rather than dragging the process
	// to debug.
	bad, err := ParseFile([]byte(`{"services":[
		{"type":"noop","name":"a","log_level":"inof"},
		{"type":"noop","name":"b","log_level":"trace"}
	]}`))
	require.NoError(t, err)
	lvl, ok = sharedLogLevel(log, bad)
	require.True(t, ok)
	require.Equal(t, logrus.InfoLevel, lvl)
}

// Block.UnmarshalJSON must surface log_level without disturbing Raw.
func TestBlockCapturesLogLevel(t *testing.T) {
	file, err := ParseFile([]byte(`{"services":[{"type":"noop","name":"n","log_level":"warn","extra":1}]}`))
	require.NoError(t, err)
	require.Equal(t, "warn", file.Services[0].LogLevel)
	require.Contains(t, string(file.Services[0].Raw), `"extra"`)
}
