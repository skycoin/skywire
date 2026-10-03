// Package mobilecore pkg/mobilecore/logsink_test.go c4-vis-core
package mobilecore

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// TestSinkSurvivesAFlood is G4's crash without the network. A shutdown left an
// accept loop logging some 100,000 warnings a second, every goroutine that
// logged called the host's sink itself (on iOS a cgo call into Swift writing
// the unified log), and the app was killed for running out of thread stack.
// Logging must only enqueue: 20,000 lines from 8 goroutines into a host that
// takes half a millisecond a line must not wait for it, must reach it from one
// goroutine at a time, in order, and what could not be delivered is counted
// and said.
func TestSinkSurvivesAFlood(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	var delivered atomic.Int64
	var dropNote atomic.Value
	SetLogSink(func(_ int32, line string) {
		n := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		if strings.Contains(line, "lines dropped") {
			dropNote.Store(line)
		} else if strings.Contains(line, "sink flood") {
			delivered.Add(1)
		}
		time.Sleep(500 * time.Microsecond) // a slow host
		inFlight.Add(-1)
	})
	t.Cleanup(func() { SetLogSink(nil) })

	log := logging.MustGetLogger("sink-flood")
	start := time.Now()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 2500 {
				log.Debug("sink flood")
			}
		}()
	}
	wg.Wait()
	took := time.Since(start)

	// The host needs ~10 s for 20,000 lines; logging them must not.
	require.Less(t, took, 3*time.Second, "logging waited for the host")
	require.Eventually(t, func() bool { return dropNote.Load() != nil }, 10*time.Second, 10*time.Millisecond,
		"the dropped lines were never reported")
	require.EqualValues(t, 1, maxInFlight.Load(), "the host was called from two goroutines at once")
	t.Logf("logged 20,000 lines in %s; delivered %d; %s", took.Round(time.Millisecond), delivered.Load(), dropNote.Load())
}
