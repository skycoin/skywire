// Package visor pkg/visor/temp_loglevel_test.go c3-vis-core
package visor

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func newTempLogVisor(t *testing.T, level logrus.Level) *Visor {
	t.Helper()
	conf := &visorconfig.V1{Common: &visorconfig.Common{}, LogLevel: level.String()}
	ml := logging.NewMasterLogger()
	ml.SetLevel(level)
	conf.SetLogger(ml)
	return &Visor{conf: conf, log: ml.PackageLogger("visor")}
}

func TestTempLogLevelEndsByItself(t *testing.T) {
	v := newTempLogVisor(t, logrus.InfoLevel)
	ml := v.conf.MasterLogger()

	st, err := v.SetTempLogLevel(logrus.DebugLevel, 50*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, "debug", st.Level)
	require.Equal(t, "info", st.Base)
	require.NotNil(t, st.Until)
	require.Equal(t, logrus.DebugLevel, ml.GetLevel())

	require.Eventually(t, func() bool { return ml.GetLevel() == logrus.InfoLevel },
		2*time.Second, 10*time.Millisecond)
	st = v.LogLevelStatus()
	require.Equal(t, "info", st.Level)
	require.Nil(t, st.Until)
	// Never saved: the config still says what it said.
	require.Equal(t, "info", v.conf.LogLevel)
}

func TestTempLogLevelClear(t *testing.T) {
	v := newTempLogVisor(t, logrus.WarnLevel)
	_, err := v.SetTempLogLevel(logrus.TraceLevel, time.Hour)
	require.NoError(t, err)

	st := v.ClearTempLogLevel()
	require.Equal(t, "warning", st.Level)
	require.Nil(t, st.Until)
	// Clearing with nothing set is a no-op.
	require.Equal(t, "warning", v.ClearTempLogLevel().Level)
}

// A second set replaces the level and ttl but keeps the base from before the
// first one, and the first set's timer does nothing when it fires.
func TestTempLogLevelSetAgainKeepsBase(t *testing.T) {
	v := newTempLogVisor(t, logrus.InfoLevel)
	ml := v.conf.MasterLogger()

	_, err := v.SetTempLogLevel(logrus.DebugLevel, 30*time.Millisecond)
	require.NoError(t, err)
	st, err := v.SetTempLogLevel(logrus.TraceLevel, time.Hour)
	require.NoError(t, err)
	require.Equal(t, "info", st.Base)

	time.Sleep(100 * time.Millisecond)
	require.Equal(t, logrus.TraceLevel, ml.GetLevel())

	require.Equal(t, "info", v.ClearTempLogLevel().Level)
}

// A level set some other way while a temporary one is in force (log_level
// from the hypervisor) is not undone when the temporary one ends.
func TestTempLogLevelKeepsLevelSetMeanwhile(t *testing.T) {
	v := newTempLogVisor(t, logrus.InfoLevel)
	ml := v.conf.MasterLogger()

	_, err := v.SetTempLogLevel(logrus.DebugLevel, time.Hour)
	require.NoError(t, err)
	ml.SetLevel(logrus.WarnLevel)

	require.Equal(t, "warning", v.ClearTempLogLevel().Level)
}

func TestTempLogLevelRejectsTTL(t *testing.T) {
	v := newTempLogVisor(t, logrus.InfoLevel)
	for _, ttl := range []time.Duration{0, -time.Second, maxTempLogLevelTTL + time.Second} {
		_, err := v.SetTempLogLevel(logrus.DebugLevel, ttl)
		require.Errorf(t, err, "ttl %s", ttl)
	}
	require.Equal(t, logrus.InfoLevel, v.conf.MasterLogger().GetLevel())
	require.Nil(t, v.LogLevelStatus().Until)
}
