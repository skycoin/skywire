// Package logging pkg/logging/hook_levels_test.go c0-com-log
package logging

import (
	"bytes"
	"io"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// countingFormatter wraps a formatter and counts how many times it ran, so a
// test can assert the SECOND format pass a capture hook performs is actually
// skipped rather than merely producing no output.
type countingFormatter struct {
	inner logrus.Formatter
	n     int
}

func (c *countingFormatter) Format(e *logrus.Entry) ([]byte, error) {
	c.n++
	return c.inner.Format(e)
}

func newCountingWriteHook(w io.Writer, level logrus.Level) (*WriteHook, *countingFormatter) {
	h := NewWriteHookLevel(w, level)
	cf := &countingFormatter{inner: h.formatter}
	h.formatter = cf
	return h, cf
}

// The default WriteHook must not re-format debug entries: that second pass was
// ~10 MB/min of formatting on a production dmsg-server at debug level, poured
// into a 256 KiB ring that could only hold ~1.5s of it.
func TestWriteHookSkipsDebugByDefault(t *testing.T) {
	require.Equal(t, logrus.InfoLevel, DefaultHookLevel)

	var buf bytes.Buffer
	h := NewWriteHook(&buf)
	require.NotContains(t, h.Levels(), logrus.DebugLevel)
	require.NotContains(t, h.Levels(), logrus.TraceLevel)
	require.Contains(t, h.Levels(), logrus.InfoLevel)
	require.Contains(t, h.Levels(), logrus.ErrorLevel)

	h, cf := newCountingWriteHook(&buf, HookLevel(DefaultHookLevel))
	l := logrus.New()
	l.SetLevel(logrus.TraceLevel)
	l.SetOutput(io.Discard)
	l.AddHook(h)

	l.Trace("hot-trace")
	l.Debug("hot-debug")
	require.Zero(t, cf.n, "debug/trace must never reach the hook's formatter")

	l.Info("kept-info")
	l.Error("kept-error")
	require.Equal(t, 2, cf.n)

	out := buf.String()
	require.Contains(t, out, "kept-info")
	require.Contains(t, out, "kept-error")
	require.NotContains(t, out, "hot-debug")
	require.NotContains(t, out, "hot-trace")
}

// An operator debugging a service must still be able to put debug lines back
// into the ring the /debug/log endpoint serves.
func TestWriteHookDebugOptIn(t *testing.T) {
	var buf bytes.Buffer
	h, cf := newCountingWriteHook(&buf, logrus.DebugLevel)
	l := logrus.New()
	l.SetLevel(logrus.DebugLevel)
	l.SetOutput(io.Discard)
	l.AddHook(h)

	l.Debug("wanted-debug")
	require.Equal(t, 1, cf.n)
	require.Contains(t, buf.String(), "wanted-debug")
}

func TestHookLevelEnvOverride(t *testing.T) {
	require.Equal(t, logrus.InfoLevel, HookLevel(logrus.InfoLevel))

	t.Setenv(HookLevelEnv, "debug")
	require.Equal(t, logrus.DebugLevel, HookLevel(logrus.InfoLevel))

	// The env var reaches the constructor, so an operator gets debug in the
	// ring without a code change.
	var buf bytes.Buffer
	require.Contains(t, NewWriteHook(&buf).Levels(), logrus.DebugLevel)

	// Garbage falls back to the caller's default rather than silently
	// widening or breaking /debug/log.
	t.Setenv(HookLevelEnv, "not-a-level")
	require.Equal(t, logrus.InfoLevel, HookLevel(logrus.InfoLevel))
}

func TestLevelsUpTo(t *testing.T) {
	require.Equal(t, []logrus.Level{logrus.PanicLevel}, LevelsUpTo(logrus.PanicLevel))
	require.Equal(t, logrus.AllLevels, LevelsUpTo(logrus.TraceLevel))
	// Out-of-range clamps rather than panicking on the slice bound.
	require.Equal(t, logrus.AllLevels, LevelsUpTo(logrus.Level(99)))
}

// With nothing subscribed and the ring off, Fire must return before doing any
// field resolution or allocation — it runs on every entry the process emits.
func TestBroadcasterFireShortCircuits(t *testing.T) {
	b := NewBroadcaster()

	// No subscribers and ring capture off: Fire must do nothing at all.
	b.SetRingCapture(false)
	e := logrus.NewEntry(logrus.New())
	e.Level = logrus.InfoLevel
	e.Message = "orphan"
	e.Data = logrus.Fields{logModuleKey: "proc:skysocks-client:abc"}
	require.NoError(t, b.Fire(e))

	events, logs := b.RecentByApp("skysocks-client", logrus.DebugLevel)
	require.Nil(t, events)
	require.Nil(t, logs)

	// Ring capture back on (the default) still records.
	b.SetRingCapture(true)
	require.NoError(t, b.Fire(e))
	_, logs = b.RecentByApp("skysocks-client", logrus.DebugLevel)
	require.Len(t, logs, 1)
	require.Equal(t, "orphan", logs[0].Message)
}

// The fan-out is skipped on the zero-subscriber fast path, so subCount must
// track Subscribe/cancel exactly or live subscribers would go silent.
func TestBroadcasterSubCountTracksSubscribers(t *testing.T) {
	b := NewBroadcaster()
	b.SetRingCapture(false)
	require.Zero(t, b.subCount.Load())

	ch, cancel := b.Subscribe(Filter{Modules: []string{"router"}}, 4)
	require.EqualValues(t, 1, b.subCount.Load())

	e := logrus.NewEntry(logrus.New())
	e.Level = logrus.InfoLevel
	e.Message = "delivered"
	e.Data = logrus.Fields{logModuleKey: "router"}
	require.NoError(t, b.Fire(e))

	select {
	case got := <-ch:
		require.Equal(t, "delivered", got.Message)
	default:
		t.Fatal("entry was not delivered to a live subscriber")
	}

	cancel()
	require.Zero(t, b.subCount.Load())
	// Fire after cancel must not panic on the closed channel.
	require.NoError(t, b.Fire(e))
}
