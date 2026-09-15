// Package visor pkg/visor/jsonlog_test.go c3-vis-core
package visor

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// TestJSONLogHook_PreservesFields is the point of the whole thing: the text log
// flattens logrus fields into the message, so answering "what did wasm-serve
// report during this boot" means grepping a format never meant to be parsed.
// The JSON lines must keep module and every WithField intact so the same
// question is a jq select with no regex.
func TestJSONLogHook_PreservesFields(t *testing.T) {
	path := filepath.Join(jsonLogDir(t), "skywire.jsonl")

	hook, err := newJSONLogHook(path)
	require.NoError(t, err)

	log := logrus.New()
	log.SetOutput(io.Discard) // the hook is what is under test
	log.SetLevel(logrus.TraceLevel)
	log.AddHook(hook)

	log.WithField("module", "wasm-serve").
		WithField("module_revision", "016223ccbda87d101b2f9658e45eb8906c57b327").
		WithField("binary_revision", "fdd7bfc6184c73500dde23bf7fbbc5a36d2e23a5").
		Warn("the embedded command module is older than this binary")

	raw, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	require.NotEmpty(t, raw, "the hook wrote nothing")

	var entry map[string]any
	require.NoError(t, json.Unmarshal(raw[:len(raw)-1], &entry), "each line must be one JSON object")

	require.Equal(t, "wasm-serve", entry["module"], "module must survive as a queryable field")
	require.Equal(t, "016223ccbda87d101b2f9658e45eb8906c57b327", entry["module_revision"])
	require.Equal(t, "fdd7bfc6184c73500dde23bf7fbbc5a36d2e23a5", entry["binary_revision"])
	require.Equal(t, "warning", entry["level"])
	require.Contains(t, entry["msg"], "older than this binary")
	require.NotEmpty(t, entry["time"], "entries must be orderable")
}

// Nanosecond timestamps: the text log is second-resolution, which cannot order
// a boot sequence where several subsystems start in the same second.
func TestJSONLogHook_TimestampHasSubSecondResolution(t *testing.T) {
	path := filepath.Join(jsonLogDir(t), "skywire.jsonl")
	hook, err := newJSONLogHook(path)
	require.NoError(t, err)

	log := logrus.New()
	log.SetOutput(io.Discard)
	log.AddHook(hook)
	log.Info("first")

	raw, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	var entry map[string]any
	require.NoError(t, json.Unmarshal(raw[:len(raw)-1], &entry))

	ts, _ := entry["time"].(string)
	require.Regexp(t, `\.\d+`, ts, "timestamp %q carries no sub-second part", ts)
}

// jsonLogDir is t.TempDir with the one behavior this test cannot use: its
// cleanup calls RemoveAll and FAILS the test if that errors.
//
// The hook holds skywire.jsonl open for as long as it exists, which is correct
// for a log sink and which lumberjackrus gives no way to undo — Hook keeps its
// *lumberjack.Logger unexported and exposes no Close. On Unix that is
// invisible: an open file unlinks fine. On Windows it is not, and both tests
// failed in cleanup, after every assertion had already passed:
//
//	TempDir RemoveAll cleanup: unlinkat ...\skywire.jsonl:
//	  The process cannot access the file because it is being used by another process.
//
// So the directory is removed on a best-effort basis instead. A leftover temp
// directory on a CI runner is not worth failing a green test over.
func jsonLogDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "skywire-jsonlog")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) }) //nolint:errcheck // best-effort by design; see above
	return dir
}
