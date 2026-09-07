// Package skyobject pkg/cxo/skyobject/stat_loops_test.go c2-net-cxo
package skyobject

import (
	"runtime"
	"testing"
	"time"
)

// TestTrackRollingAveragesGatesGoroutines guards the invariant that the
// per-second rolling-average goroutines run only where something can read them.
// They surface solely through Container.Stat() -> Node.Stat() -> the node RPC,
// so a node started without an RPC listener must not pay for them — which is
// every in-visor CXO node (treestore publisher/subscriber, cxoaggregate,
// cxopreview), seven per visor at two loops each.
func TestTrackRollingAveragesGatesGoroutines(t *testing.T) {
	if !NewConfig().TrackRollingAverages {
		t.Fatal("NewConfig should default TrackRollingAverages true")
	}

	count := func(rolling bool) int {
		runtime.GC()
		before := runtime.NumGoroutine()
		is := newIndexStat(5, rolling)
		cs := newCxdsStat(5, rolling)
		// Give any spawned loop a moment to actually be scheduled.
		time.Sleep(50 * time.Millisecond)
		got := runtime.NumGoroutine() - before
		is.Close()
		cs.Close()
		return got
	}

	if n := count(false); n != 0 {
		t.Errorf("TrackRollingAverages=false spawned %d goroutine(s), want 0", n)
	}
	if n := count(true); n != 2 {
		t.Errorf("TrackRollingAverages=true spawned %d goroutine(s), want 2", n)
	}
}
