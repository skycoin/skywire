// Package skysocks pkg/skysocks/accept_stats_test.go c4-app-proxy
package skysocks

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAcceptStatsSnapshotIsNilUntilSomethingIsAccepted(t *testing.T) {
	var a acceptStats
	if got := a.snapshot(); got != nil {
		t.Fatalf("snapshot() = %+v, want nil on an idle proxy", got)
	}
	a.accepted.Add(1)
	if got := a.snapshot(); got == nil {
		t.Fatal("snapshot() = nil after an accept, want a section")
	}
}

func TestAcceptStatsCountsOutcomes(t *testing.T) {
	var a acceptStats
	a.accepted.Add(3)
	a.pickNil.Add(1)
	a.observeOpen(2*time.Millisecond, nil)
	a.observeOpen(5*time.Millisecond, errors.New("route is down"))

	got := a.snapshot()
	if got == nil {
		t.Fatal("snapshot() = nil")
	}
	if got.Accepted != 3 {
		t.Errorf("Accepted = %d, want 3", got.Accepted)
	}
	if got.NoTunnel != 1 {
		t.Errorf("NoTunnel = %d, want 1", got.NoTunnel)
	}
	if got.Opened != 1 {
		t.Errorf("Opened = %d, want 1", got.Opened)
	}
	if got.OpenFailed != 1 {
		t.Errorf("OpenFailed = %d, want 1", got.OpenFailed)
	}
	if got.LastOpenErr != "route is down" {
		t.Errorf("LastOpenErr = %q, want %q", got.LastOpenErr, "route is down")
	}
}

// MaxOpenMS must keep the SLOWEST open, not the latest — a fast open after a
// slow one must not hide it, since the slow one is the interesting event.
func TestAcceptStatsKeepsTheSlowestOpen(t *testing.T) {
	var a acceptStats
	a.observeOpen(50*time.Millisecond, nil)
	a.observeOpen(1*time.Millisecond, nil)

	got := a.snapshot()
	if got == nil {
		// snapshot() gates on accepted, which observeOpen does not bump.
		a.accepted.Add(1)
		got = a.snapshot()
	}
	if got.MaxOpenMS < 49 || got.MaxOpenMS > 51 {
		t.Errorf("MaxOpenMS = %v, want ~50", got.MaxOpenMS)
	}
}

// The counters are written from the accept loop and from every connection's
// own goroutine at once, so they must be race-free. Run this with -race.
func TestAcceptStatsIsConcurrencySafe(t *testing.T) {
	var a acceptStats
	var wg sync.WaitGroup
	const n = 50

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a.accepted.Add(1)
			if i%2 == 0 {
				a.observeOpen(time.Duration(i)*time.Millisecond, nil)
			} else {
				a.observeOpen(time.Duration(i)*time.Millisecond, errors.New("boom"))
			}
			_ = a.snapshot()
		}(i)
	}
	wg.Wait()

	got := a.snapshot()
	if got.Accepted != n {
		t.Errorf("Accepted = %d, want %d", got.Accepted, n)
	}
	if got.Opened+got.OpenFailed != n {
		t.Errorf("Opened+OpenFailed = %d, want %d", got.Opened+got.OpenFailed, n)
	}
}
