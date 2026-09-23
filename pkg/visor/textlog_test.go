// Package visor pkg/visor/textlog_test.go c3-vis-core
package visor

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// TestTextLogHook_LevelsShareOneWriter pins the bug that lost every INFO, WARN
// and ERROR line from skywire.log. With a lumberjack writer per level over the
// same path, DEBUG volume rotated the file out from under the other levels and
// they went on writing to the renamed inode. One writer means an Info line
// logged after a rotation is in the file that is actually on disk.
func TestTextLogHook_LevelsShareOneWriter(t *testing.T) {
	path := filepath.Join(logTestDir(t), "skywire.log")

	hook, err := newTextLogHook(path)
	require.NoError(t, err)

	log := logrus.New()
	log.SetOutput(io.Discard) // the hook is what is under test
	log.SetLevel(logrus.TraceLevel)
	log.AddHook(hook)

	log.Info("before rotation")

	// MaxSize is 1 MB; push well past it with DEBUG alone, which is what wins
	// the rotation race in a running visor.
	filler := strings.Repeat("x", 1024)
	for i := 0; i < 1400; i++ {
		log.Debug(filler)
	}

	log.Info("after rotation marker")
	log.Warn("warn marker")
	log.Error("error marker")

	raw, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)

	entries, err := filepath.Glob(filepath.Join(filepath.Dir(path), "skywire-*.log"))
	require.NoError(t, err)
	require.NotEmpty(t, entries, "the log did not rotate; the test wrote too little to be meaningful")

	for _, want := range []string{"after rotation marker", "warn marker", "error marker"} {
		require.Contains(t, string(raw), want,
			"the current log file lost a line written after rotation")
	}
}

// logTestDir is a temp dir whose removal failure is tolerated: the hook's
// lumberjack writer has no Close, so on Windows the log file is still open
// when the test ends and t.TempDir's cleanup would fail the test.
func logTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "loghook")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) }) //nolint:errcheck
	return dir
}
