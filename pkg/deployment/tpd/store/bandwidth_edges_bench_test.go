// Package store pkg/deployment/tpd/store/bandwidth_edges_bench_test.go c4-net-discovery
package store

import (
	"runtime"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// BenchmarkBandwidthEdgePairRetained reports, as retained-B/entry, what
// keeping the type parsed from a freshly read bw:edges value holds on to.
func BenchmarkBandwidthEdgePairRetained(b *testing.B) {
	pk0, _ := cipher.GenerateKeyPair()
	pk1, _ := cipher.GenerateKeyPair()
	val := bandwidthEdgesValue(&transport.Entry{Edges: [2]cipher.PubKey{pk0, pk1}, Type: "stcpr"})
	const n = 20000
	var per float64
	b.ReportAllocs()
	for b.Loop() {
		var before, after runtime.MemStats
		types := make([]string, n)
		runtime.GC()
		runtime.ReadMemStats(&before)
		for i := range types {
			_, types[i], _ = parseBandwidthEdgePair(strings.Clone(val)) // as read from redis
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		per = (float64(after.HeapAlloc) - float64(before.HeapAlloc)) / n
		runtime.KeepAlive(types)
	}
	b.ReportMetric(per, "retained-B/entry")
}

func TestInternType(t *testing.T) {
	for _, s := range []string{"stcpr", "sudph", "dmsg", "squicr", "quic", "made-up", ""} {
		if got := internType(strings.Clone(s)); got != s {
			t.Errorf("internType(%q) = %q", s, got)
		}
	}
}
