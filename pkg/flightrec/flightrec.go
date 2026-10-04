// Package flightrec pkg/flightrec/flightrec.go c1-util-debug
//
// A process-wide execution-trace flight recorder (runtime/trace): it keeps
// the last few seconds of trace in memory, so a stall that is over before
// anyone looks can still be seen. A detector calls Snapshot when it notices
// one, and Handler serves the current window on demand.
package flightrec

import (
	"fmt"
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

// Snapshot saves the current window to a file named for reason, at most once
// a minute, in the background so the caller is never held. It keeps the last
// few files.
func Snapshot(reason string) {
	mu.Lock()
	r, d := rec, dir
	if r == nil || d == "" || time.Since(last) < snapshotGap {
		mu.Unlock()
		return
	}
	last = time.Now()
	mu.Unlock()
	go func() {
		path, err := save(r, d, reason)
		if err != nil {
			logf("flight recorder: snapshot for %s failed: %v", reason, err)
			return
		}
		logf("flight recorder: saved the last seconds of trace for %s to %s (go tool trace %s)", reason, path, path)
	}()
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func save(r *trace.FlightRecorder, d, reason string) (string, error) {
	if err := os.MkdirAll(d, 0o750); err != nil {
		return "", err
	}
	name := fmt.Sprintf("flightrec-%s-%s.trace", time.Now().UTC().Format("20060102T150405Z"), unsafeName.ReplaceAllString(reason, "_"))
	path := filepath.Join(d, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // our own directory
	if err != nil {
		return "", err
	}
	if _, err := r.WriteTo(f); err != nil {
		f.Close()       //nolint:errcheck,gosec
		os.Remove(path) //nolint:errcheck,gosec
		return "", err
	}
	if err := f.Close(); err != nil {
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

// Handler serves the current window as a trace file for `go tool trace`.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		r := rec
		mu.Unlock()
		if r == nil {
			http.Error(w, "the flight recorder is not running", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="flightrec.trace"`)
		if _, err := r.WriteTo(w); err != nil {
			logf("flight recorder: serving the window failed: %v", err)
		}
	})
}
