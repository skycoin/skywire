//go:build !js

// Package logging pkg/logging/logging_more_test.go: unit tests for the level
// parser, master/package loggers, the write hook, the broadcaster, and the text
// formatter.
package logging

import (
	"bytes"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEntry(level logrus.Level, msg string, data logrus.Fields) *logrus.Entry {
	e := logrus.NewEntry(logrus.New())
	e.Level = level
	e.Message = msg
	e.Time = time.Unix(1_700_000_000, 0)
	if data != nil {
		e.Data = data
	}
	return e
}

// --- logging.go ---

func TestLevelFromString(t *testing.T) {
	cases := map[string]logrus.Level{
		"debug":    logrus.DebugLevel,
		"info":     logrus.InfoLevel,
		"notice":   logrus.InfoLevel,
		"warn":     logrus.WarnLevel,
		"warning":  logrus.WarnLevel,
		"error":    logrus.ErrorLevel,
		"fatal":    logrus.FatalLevel,
		"critical": logrus.FatalLevel,
		"panic":    logrus.PanicLevel,
		"trace":    logrus.TraceLevel,
		"INFO":     logrus.InfoLevel, // case-insensitive
	}
	for s, want := range cases {
		got, err := LevelFromString(s)
		require.NoErrorf(t, err, "level %q", s)
		assert.Equalf(t, want, got, "level %q", s)
	}

	_, err := LevelFromString("nonsense")
	assert.Error(t, err)
}

func TestGlobalLoggerControls(t *testing.T) {
	origOut, origLevel := log.Out, log.GetLevel()
	t.Cleanup(func() { log.Out = origOut; log.SetLevel(origLevel) })

	SetLevel(logrus.WarnLevel)
	assert.Equal(t, logrus.WarnLevel, GetLevel())

	var buf bytes.Buffer
	SetOutputTo(&buf)
	MustGetLogger("globaltest").Warn("hello-global")
	assert.Contains(t, buf.String(), "hello-global")
	assert.Contains(t, buf.String(), "globaltest")

	buf.Reset()
	Disable()
	MustGetLogger("globaltest").Warn("should-not-appear")
	assert.Empty(t, buf.String())

	assert.NotPanics(t, func() { EnableColors(); DisableColors() })
}

func TestAddHook(t *testing.T) {
	origOut := log.Out
	t.Cleanup(func() { log.Out = origOut })
	SetOutputTo(&bytes.Buffer{})

	var hookBuf bytes.Buffer
	AddHook(NewWriteHook(&hookBuf))
	MustGetLogger("hooktest").Info("via-hook")
	assert.Contains(t, hookBuf.String(), "via-hook")
}

// --- logger.go ---

func TestMasterAndPackageLogger(t *testing.T) {
	ml := NewMasterLogger()
	require.NotNil(t, ml)

	var buf bytes.Buffer
	ml.Out = &buf
	pl := ml.PackageLogger("mymod")
	require.NotNil(t, pl)
	pl.Info("pkg-msg")
	assert.Contains(t, buf.String(), "pkg-msg")
	assert.Contains(t, buf.String(), "mymod")

	assert.NotNil(t, pl.Critical())
	assert.NotNil(t, pl.WithTime(time.Now()))
	assert.NotPanics(t, func() { ml.EnableColors(); ml.DisableColors() })
}

func TestWithAppName(t *testing.T) {
	pl := NewMasterLogger().PackageLogger("m")

	// Empty name is a no-op returning the receiver.
	assert.Same(t, pl, pl.WithAppName(""))

	// Non-empty derives a new logger.
	derived := pl.WithAppName("myapp")
	require.NotNil(t, derived)
	assert.NotSame(t, pl, derived)
}

// --- hooks.go ---

func TestWriteHook(t *testing.T) {
	var buf bytes.Buffer
	h := NewWriteHook(&buf)
	assert.Equal(t, logrus.AllLevels, h.Levels())

	require.NoError(t, h.Fire(newEntry(logrus.InfoLevel, "hooked-line", logrus.Fields{"k": "v"})))
	out := buf.String()
	assert.Contains(t, out, "hooked-line")
	assert.Contains(t, out, "k")
}

// --- broadcaster.go ---

func TestBroadcasterDelivery(t *testing.T) {
	b := NewBroadcaster()
	assert.Equal(t, logrus.AllLevels, b.Levels())

	ch, cancel := b.Subscribe(Filter{Modules: []string{"router"}}, 8)

	// Matching module prefix is delivered.
	require.NoError(t, b.Fire(newEntry(logrus.InfoLevel, "match", logrus.Fields{logModuleKey: "router_setup"})))
	select {
	case e := <-ch:
		assert.Equal(t, "match", e.Message)
	case <-time.After(time.Second):
		t.Fatal("matching entry not delivered")
	}

	// Non-matching module is dropped.
	require.NoError(t, b.Fire(newEntry(logrus.InfoLevel, "nomatch", logrus.Fields{logModuleKey: "dmsg"})))
	select {
	case e := <-ch:
		t.Fatalf("unexpected delivery: %s", e.Message)
	case <-time.After(50 * time.Millisecond):
	}

	dropped := cancel()
	assert.Zero(t, dropped)
	_, ok := <-ch
	assert.False(t, ok, "channel closed on cancel")

	// cancel is idempotent.
	assert.Equal(t, uint64(0), cancel())
}

func TestBroadcasterDropsWhenFull(t *testing.T) {
	b := NewBroadcaster()
	_, cancel := b.Subscribe(Filter{Modules: []string{"x"}}, 1)
	defer cancel()

	for range 4 {
		require.NoError(t, b.Fire(newEntry(logrus.InfoLevel, "m", logrus.Fields{logModuleKey: "x"})))
	}
	assert.GreaterOrEqual(t, cancel(), uint64(2), "entries beyond capacity should be dropped")
}

func TestBroadcasterFilters(t *testing.T) {
	b := NewBroadcaster()

	t.Run("app name via field", func(t *testing.T) {
		ch, cancel := b.Subscribe(Filter{AppName: "vpn"}, 4)
		defer cancel()
		require.NoError(t, b.Fire(newEntry(logrus.InfoLevel, "byfield", logrus.Fields{"app_name": "vpn"})))
		select {
		case e := <-ch:
			assert.Equal(t, "byfield", e.Message)
		case <-time.After(time.Second):
			t.Fatal("app_name field match not delivered")
		}
	})

	t.Run("min level filters out less severe", func(t *testing.T) {
		ch, cancel := b.Subscribe(Filter{Modules: []string{"m"}, MinLevel: logrus.WarnLevel}, 4)
		defer cancel()
		// Info is less severe than Warn → dropped.
		require.NoError(t, b.Fire(newEntry(logrus.InfoLevel, "info", logrus.Fields{logModuleKey: "m"})))
		// Error is more severe → delivered.
		require.NoError(t, b.Fire(newEntry(logrus.ErrorLevel, "err", logrus.Fields{logModuleKey: "m"})))
		select {
		case e := <-ch:
			assert.Equal(t, "err", e.Message)
		case <-time.After(time.Second):
			t.Fatal("error entry not delivered")
		}
	})
}

// --- formatter.go ---

func TestTextFormatterPlain(t *testing.T) {
	f := &TextFormatter{DisableColors: true, FullTimestamp: true, ForceFormatting: true}
	out, err := f.Format(newEntry(logrus.InfoLevel, "plain-msg", logrus.Fields{
		logModuleKey: "mymod",
		"count":      3,
		"name":       "alice",
	}))
	require.NoError(t, err)
	s := string(out)
	assert.Contains(t, s, "plain-msg")
	assert.Contains(t, s, "count")
	assert.Contains(t, s, "alice")
}

func TestTextFormatterColored(t *testing.T) {
	f := &TextFormatter{ForceColors: true, ForceFormatting: true, FullTimestamp: true}
	out, err := f.Format(newEntry(logrus.ErrorLevel, "colored-msg", logrus.Fields{logModuleKey: "m"}))
	require.NoError(t, err)
	assert.Contains(t, string(out), "colored-msg")
}

func TestTextFormatterQuoting(t *testing.T) {
	f := &TextFormatter{DisableColors: true, AlwaysQuoteStrings: true, QuoteEmptyFields: true, ForceFormatting: true}
	out, err := f.Format(newEntry(logrus.WarnLevel, "msg with spaces", logrus.Fields{"empty": ""}))
	require.NoError(t, err)
	assert.NotEmpty(t, out)
}
