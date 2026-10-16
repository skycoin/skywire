package tpviz

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/geoip"
)

// The network view uses the process's memory-mapped GeoIP reader rather
// than inflating its own 59 MB copy onto the heap.
func TestNewServerUsesSharedGeoIP(t *testing.T) {
	if !geoip.Embedded() {
		t.Skip("no embedded GeoIP database in this build")
	}
	shared, err := geoip.Shared()
	require.NoError(t, err)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	s := NewServer(Config{})
	runtime.GC()
	runtime.ReadMemStats(&after)
	require.Same(t, shared, s.geoDB)
	require.Less(t, int64(after.HeapAlloc)-int64(before.HeapAlloc), int64(16<<20), "no heap copy of the database") //nolint:gosec
	runtime.KeepAlive(s)
}
