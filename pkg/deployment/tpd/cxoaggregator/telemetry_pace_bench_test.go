package cxoaggregator

import (
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// BenchmarkTelemetryPacerHeld reports the live heap of a pacer holding one
// pending snapshot for each of 100k (transport, reporter) keys.
func BenchmarkTelemetryPacerHeld(b *testing.B) {
	const keys = 100000
	ks := make([]telKey, keys)
	pk, _ := cipher.GenerateKeyPair()
	for i := range ks {
		ks[i] = telKey{id: uuid.New(), reporter: pk}
	}
	t0 := time.Now()
	var perKey float64
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		p := &telemetryPacer{}
		for _, k := range ks {
			p.offer(k, telSnap{sent: 1, latMin: 1, latMax: 2, latAvg: 1.5, tp: tpCodeOf("stcpr")}, t0)
			p.offer(k, telSnap{sent: 2, latMin: 1, latMax: 2, latAvg: 1.5, tp: tpCodeOf("stcpr")}, t0.Add(time.Second))
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		perKey = float64(after.HeapAlloc-before.HeapAlloc) / keys
		runtime.KeepAlive(p)
	}
	b.ReportMetric(perKey, "heapB/key")
}
