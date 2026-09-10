package logstore

import (
	"io"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// fire pushes one log entry through the hook with the given message.
func fire(t *testing.T, hook logrus.Hook, msg string) {
	t.Helper()
	e := logrus.NewEntry(logrus.New())
	e.Message = msg
	e.Level = logrus.InfoLevel
	require.NoError(t, hook.Fire(e))
}

func TestMakeStoreAndLevels(t *testing.T) {
	store, hook := MakeStore(10)
	require.NotNil(t, store)
	require.NotNil(t, hook)
	// Info and above only: the hook JSON-encodes every accepted entry under a
	// process-wide mutex, so the debug firehose must not reach it by default.
	require.Equal(t, logrus.AllLevels[:logrus.InfoLevel+1], hook.Levels())
	require.NotContains(t, hook.Levels(), logrus.DebugLevel)
	require.NotContains(t, hook.Levels(), logrus.TraceLevel)
}

// The hook must be a genuine no-op below its level — logrus consults Levels()
// before calling Fire, so a debug line never reaches the JSON formatter or the
// store mutex.
func TestHookLevelGatesFire(t *testing.T) {
	store, hook := MakeStore(10)

	l := logrus.New()
	l.SetLevel(logrus.DebugLevel)
	l.SetOutput(io.Discard)
	l.AddHook(hook)

	l.Debug("hot-path-debug")
	l.Info("kept-info")

	logs, _ := store.GetLogs()
	require.Len(t, logs, 1)
	require.Contains(t, logs[0], "kept-info")
}

// An operator can still opt back in to capturing debug in the buffer.
func TestMakeStoreLevelOptIn(t *testing.T) {
	store, hook := MakeStoreLevel(10, logrus.DebugLevel)
	require.Contains(t, hook.Levels(), logrus.DebugLevel)

	l := logrus.New()
	l.SetLevel(logrus.DebugLevel)
	l.SetOutput(io.Discard)
	l.AddHook(hook)

	l.Debug("wanted-debug")

	logs, _ := store.GetLogs()
	require.Len(t, logs, 1)
	require.Contains(t, logs[0], "wanted-debug")
}

func TestGetLogs_UnderCapacity(t *testing.T) {
	store, hook := MakeStore(5)

	logs, dropped := store.GetLogs()
	require.Empty(t, logs)
	require.Zero(t, dropped)

	fire(t, hook, "alpha")
	fire(t, hook, "beta")
	fire(t, hook, "gamma")

	logs, dropped = store.GetLogs()
	require.Len(t, logs, 3)
	require.Zero(t, dropped) // nothing overwritten
	require.Contains(t, logs[0], "alpha")
	require.Contains(t, logs[2], "gamma")
	// Each entry is JSON carrying the 1-indexed real line number.
	require.Contains(t, logs[0], `"`+LogRealLineKey+`":1`)
	require.Contains(t, logs[2], `"`+LogRealLineKey+`":3`)
}

func TestGetLogs_OverCapacityWraps(t *testing.T) {
	store, hook := MakeStore(3)
	for _, m := range []string{"m1", "m2", "m3", "m4", "m5"} {
		fire(t, hook, m)
	}

	logs, dropped := store.GetLogs()
	require.Len(t, logs, 3)
	require.EqualValues(t, 2, dropped) // entryNum(5) - cap(3)
	// Ring order: oldest still-held line first.
	require.Contains(t, logs[0], "m3")
	require.Contains(t, logs[1], "m4")
	require.Contains(t, logs[2], "m5")
	require.NotContains(t, strings.Join(logs, "\n"), "m1") // aged out
}

func TestGetLogsSince_Empty(t *testing.T) {
	store, _ := MakeStore(5)
	entries, dropped, latest := store.GetLogsSince(0)
	require.Empty(t, entries)
	require.Zero(t, dropped)
	require.Zero(t, latest)
}

func TestGetLogsSince_DiffStreaming(t *testing.T) {
	store, hook := MakeStore(5)
	fire(t, hook, "m1")
	fire(t, hook, "m2")
	fire(t, hook, "m3")

	// since == 0 → whole buffer.
	entries, dropped, latest := store.GetLogsSince(0)
	require.Len(t, entries, 3)
	require.Zero(t, dropped)
	require.EqualValues(t, 3, latest)

	// since == 1 → only lines 2 and 3.
	entries, dropped, latest = store.GetLogsSince(1)
	require.Len(t, entries, 2)
	require.Zero(t, dropped)
	require.EqualValues(t, 3, latest)
	require.Contains(t, entries[0], "m2")
	require.Contains(t, entries[1], "m3")

	// Negative since is clamped to 0.
	entries, _, _ = store.GetLogsSince(-100)
	require.Len(t, entries, 3)

	// since >= latest → empty entry slice, latest reported.
	entries, dropped, latest = store.GetLogsSince(3)
	require.Empty(t, entries)
	require.Zero(t, dropped)
	require.EqualValues(t, 3, latest)

	entries, _, latest = store.GetLogsSince(99)
	require.Empty(t, entries)
	require.EqualValues(t, 3, latest)
}

func TestGetLogsSince_DroppedAfterWrap(t *testing.T) {
	store, hook := MakeStore(3)
	for _, m := range []string{"m1", "m2", "m3", "m4", "m5"} {
		fire(t, hook, m)
	}

	// Caller still at line 0 but lines 1-2 aged out of the 3-slot ring.
	entries, dropped, latest := store.GetLogsSince(0)
	require.EqualValues(t, 5, latest)
	require.EqualValues(t, 2, dropped) // oldestAvailable(3) - startLine(1)
	require.Len(t, entries, 3)         // lines 3,4,5
	require.Contains(t, entries[0], "m3")
	require.Contains(t, entries[2], "m5")

	// A caller keeping up sees no drops.
	entries, dropped, _ = store.GetLogsSince(4)
	require.Len(t, entries, 1)
	require.Zero(t, dropped)
	require.Contains(t, entries[0], "m5")
}
