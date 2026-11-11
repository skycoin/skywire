package appserver

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// Every start of an in-process app opens its log file again. Each lumberjack
// writer starts a goroutine that its Close does not stop, so the writer is
// shared per file instead of leaking one goroutine per start.
func TestStoreLogSharesOneWriterPerFile(t *testing.T) {
	dir := t.TempDir()
	// The hook outlives the test otherwise, and Windows does not remove the
	// open file with the directory.
	t.Cleanup(func() {
		h, ok := appLogHooks.LoadAndDelete(dir + "/log/skywire.log")
		require.True(t, ok)
		require.NoError(t, h.(*appLogHook).Close())
	})
	before := millGoroutines()
	for i := 0; i < 3; i++ {
		ml := logging.NewMasterLogger()
		storeLog(ml, dir)
		ml.PackageLogger("app").Info("started")
	}
	require.FileExists(t, filepath.Join(dir, "log", "skywire.log"))
	require.Eventually(t, func() bool { return millGoroutines()-before == 1 }, 2*time.Second, 50*time.Millisecond,
		"%d writer goroutines for one log file", millGoroutines()-before)
}

func millGoroutines() int {
	buf := make([]byte, 1<<20)
	return strings.Count(string(buf[:runtime.Stack(buf, true)]), "lumberjack%2ev2.(*Logger).millRun")
}
