//go:build !tinygo

package flightrec

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFlightRecorder(t *testing.T) {
	mu.Lock()
	last = time.Time{} // the once-a-minute limit outlives a single run
	mu.Unlock()
	d := t.TempDir()
	require.NoError(t, Start(d, t.Logf))
	t.Cleanup(Stop)
	require.True(t, Running())

	busy := make(chan struct{})
	go func() {
		for i := 0; i < 1e6; i++ {
			_ = make([]byte, 64) //nolint:staticcheck // trace events to record
		}
		close(busy)
	}()
	<-busy

	Snapshot("test wedge")
	require.Eventually(t, func() bool { return len(Snapshots()) == 1 }, 10*time.Second, 50*time.Millisecond)
	fi, err := os.Stat(Snapshots()[0])
	require.NoError(t, err)
	require.Positive(t, fi.Size())
	require.Contains(t, filepath.Base(Snapshots()[0]), "test_wedge")

	Snapshot("again")
	time.Sleep(200 * time.Millisecond)
	require.Len(t, Snapshots(), 1, "a second snapshot within a minute is skipped")

	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/debug/pprof/flightrecorder", nil))
	require.Equal(t, 200, rr.Code)
	require.Positive(t, rr.Body.Len())
}

func TestPruneKeepsTheNewest(t *testing.T) {
	d := t.TempDir()
	for i := 0; i < keepSnapshots+3; i++ {
		name := filepath.Join(d, "flightrec-2026010"+string(rune('0'+i))+"T000000Z-x.trace")
		require.NoError(t, os.WriteFile(name, []byte("x"), 0o600))
	}
	prune(d)
	entries, err := os.ReadDir(d)
	require.NoError(t, err)
	require.Len(t, entries, keepSnapshots)
	require.Equal(t, "flightrec-20260103T000000Z-x.trace", entries[0].Name(), "the oldest are removed")
}

func TestHandlerWhenStopped(t *testing.T) {
	Stop()
	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, 404, rr.Code)
}
