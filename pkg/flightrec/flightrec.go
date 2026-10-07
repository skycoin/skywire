// Package flightrec pkg/flightrec/flightrec.go c1-util-debug
//
// A process-wide execution-trace flight recorder (runtime/trace): it keeps
// the last few seconds of trace in memory, so a stall that is over before
// anyone looks can still be seen. A detector calls Snapshot when it notices
// one, and Handler serves the current window on demand.
package flightrec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/trace"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// windowBytes bounds the recorder's memory; a window of this size holds
	// a few seconds of a busy visor.
	windowBytes = 8 << 20
	// snapshotGap is the least time between two saved snapshots, so a
	// detector that fires repeatedly costs one file, not a stream of them.
	snapshotGap = time.Minute
	// keepSnapshots is how many saved snapshots are kept; older ones are removed.
	keepSnapshots = 5
)

var (
	mu   sync.Mutex
	rec  *trace.FlightRecorder
	dir  string
	last time.Time
	logf = func(string, ...any) {}

	// writeMu orders WriteTo and Stop, which the recorder does not allow to
	// overlap. Taken before mu, never after it.
	writeMu sync.Mutex
)

// Start runs the recorder, saving snapshots into snapDir. Calling it again
// while it runs only changes the directory.
func Start(snapDir string, log func(string, ...any)) error {
	mu.Lock()
	defer mu.Unlock()
	dir = snapDir
	if log != nil {
		logf = log
	}
	if rec != nil {
		return nil
	}
	r := trace.NewFlightRecorder(trace.FlightRecorderConfig{MaxBytes: windowBytes})
	if err := r.Start(); err != nil {
		return err
	}
	rec = r
	return nil
}

// Stop ends the recorder.
func Stop() {
	writeMu.Lock()
	defer writeMu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	if rec != nil {
		rec.Stop()
		rec = nil
	}
}

// Running reports whether the recorder is on.
func Running() bool {
	mu.Lock()
	defer mu.Unlock()
	return rec != nil
}

// writeWindow writes the current window to w. It reports false when the
// recorder is not running.
func writeWindow(w io.Writer) (bool, error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	mu.Lock()
	r := rec
	mu.Unlock()
	if r == nil {
		return false, nil
	}
	_, err := r.WriteTo(w)
	return true, err
}

// Snapshot saves the current window to a file named for reason, at most once
// a minute, in the background so the caller is never held. It keeps the last
// few files.
func Snapshot(reason string) {
	mu.Lock()
	d := dir
	if rec == nil || d == "" || time.Since(last) < snapshotGap {
		mu.Unlock()
		return
	}
	last = time.Now()
	mu.Unlock()
	go func() {
		path, err := save(d, reason)
		if err != nil {
			logf("flight recorder: snapshot for %s failed: %v", reason, err)
			return
		}
		logf("flight recorder: saved the last seconds of trace for %s to %s (go tool trace %s)", reason, path, path)
	}()
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// save writes under a .partial name and renames it, so Snapshots never lists
// a file still being written.
func save(d, reason string) (string, error) {
	if err := os.MkdirAll(d, 0o750); err != nil {
		return "", err
	}
	name := fmt.Sprintf("flightrec-%s-%s.trace", time.Now().UTC().Format("20060102T150405Z"), unsafeName.ReplaceAllString(reason, "_"))
	path := filepath.Join(d, name)
	tmp := path + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // our own directory
	if err != nil {
		return "", err
	}
	ok, err := writeWindow(f)
	if err == nil && !ok {
		err = errors.New("the flight recorder stopped")
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp) //nolint:errcheck,gosec
		return "", err
	}
	prune(d)
	return path, nil
}

// prune removes all but the newest keepSnapshots snapshots in d.
func prune(d string) {
	entries, err := os.ReadDir(d)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "flightrec-") && strings.HasSuffix(e.Name(), ".trace") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // the timestamp in the name sorts oldest first
	for len(names) > keepSnapshots {
		os.Remove(filepath.Join(d, names[0])) //nolint:errcheck,gosec
		names = names[1:]
	}
}

// Snapshots lists the saved snapshot files, newest first.
func Snapshots() []string {
	mu.Lock()
	d := dir
	mu.Unlock()
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil
	}
	var out []string
	for i := len(entries) - 1; i >= 0; i-- {
		n := entries[i].Name()
		if strings.HasPrefix(n, "flightrec-") && strings.HasSuffix(n, ".trace") {
			out = append(out, filepath.Join(d, n))
		}
	}
	return out
}

// Handler serves the current window as a trace file for `go tool trace`. The
// window is buffered first so a slow client cannot hold Stop up.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var buf bytes.Buffer
		ok, err := writeWindow(&buf)
		if !ok {
			http.Error(w, "the flight recorder is off, start it with: skywire cli config set flight_recorder=true", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "reading the flight recorder failed", http.StatusInternalServerError)
			logf("flight recorder: serving the window failed: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="flightrec.trace"`)
		w.Write(buf.Bytes()) //nolint:errcheck,gosec // the client went away
	})
}
