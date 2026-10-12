package logserver

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

var heapDumpMu sync.Mutex

// heapDumpHandler writes runtime/debug.WriteHeapDump to the visor's log
// directory, after a GC so only live objects remain, and answers with the
// path. The world stops while it writes.
func heapDumpHandler(localPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if localPath == "" {
			writeString(w, http.StatusNotFound, "no local path")
			return
		}
		if !heapDumpMu.TryLock() {
			writeString(w, http.StatusConflict, "a heap dump is already being written")
			return
		}
		defer heapDumpMu.Unlock()
		dir := filepath.Join(localPath, "log")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			writeString(w, http.StatusInternalServerError, "%v", err)
			return
		}
		path := filepath.Join(dir, fmt.Sprintf("heapdump-%d", time.Now().Unix()))
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec
		if err != nil {
			writeString(w, http.StatusInternalServerError, "%v", err)
			return
		}
		start := time.Now()
		runtime.GC()
		debug.WriteHeapDump(f.Fd())
		took := time.Since(start)
		st, statErr := f.Stat()
		if err := f.Close(); err != nil || statErr != nil {
			writeString(w, http.StatusInternalServerError, "%v %v", err, statErr)
			return
		}
		writeString(w, http.StatusOK, "%s %d bytes in %s\n", path, st.Size(), took.Round(time.Millisecond))
	}
}
