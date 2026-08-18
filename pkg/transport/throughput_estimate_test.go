package transport

import "testing"

// TestSampleThroughputHighWatermark pins the passive throughput estimator: it
// tracks the peak observed goodput, and — critically — an IDLE or SLOW interval
// never lowers it (absence of traffic is not evidence of low capacity).
func TestSampleThroughputHighWatermark(t *testing.T) {
	mt := NewManagedTransportForTest(nil)
	const s = int64(1_000_000_000) // 1 second in ns
	base := int64(1_000_000_000)

	// Seed the baseline (a rate needs two points).
	mt.sampleThroughput(base)
	if mt.GetThroughputBps() != 0 {
		t.Fatalf("seed should leave estimate 0, got %v", mt.GetThroughputBps())
	}

	// 10 MB in 1 s → a high goodput sample.
	mt.LogEntry.AddRecv(10_000_000)
	mt.sampleThroughput(base + s)
	peak := mt.GetThroughputBps()
	if peak < 5_000_000 {
		t.Fatalf("after 10MB/s the estimate should be multi-MB/s, got %v", peak)
	}

	// Idle interval: no bytes over 10 s → estimate must NOT drop.
	mt.sampleThroughput(base + 11*s)
	if mt.GetThroughputBps() < peak {
		t.Errorf("idle interval lowered the estimate: %v < %v", mt.GetThroughputBps(), peak)
	}

	// Slow interval: 100 KB in 1 s (well below peak) → high-watermark holds.
	mt.LogEntry.AddRecv(100_000)
	mt.sampleThroughput(base + 12*s)
	if mt.GetThroughputBps() < peak {
		t.Errorf("slow interval lowered the high-watermark: %v < %v", mt.GetThroughputBps(), peak)
	}

	// A NEW, higher burst raises it: 40 MB in 1 s.
	mt.LogEntry.AddSent(40_000_000)
	mt.sampleThroughput(base + 13*s)
	if mt.GetThroughputBps() <= peak {
		t.Errorf("a faster burst should raise the estimate: %v <= %v", mt.GetThroughputBps(), peak)
	}
}

// TestSampleThroughputCounterReset: a backwards byte delta (transport
// re-created, counters reset) yields a 0 delta, never a negative/garbage rate.
func TestSampleThroughputCounterReset(t *testing.T) {
	mt := NewManagedTransportForTest(nil)
	const s = int64(1_000_000_000)
	base := int64(1_000_000_000)

	mt.LogEntry.AddSent(5_000_000)
	mt.sampleThroughput(base)     // seed at 5MB sent
	mt.sampleThroughput(base + s) // no new bytes → rate 0, estimate stays 0
	if mt.GetThroughputBps() != 0 {
		t.Fatalf("no-traffic interval should keep estimate 0, got %v", mt.GetThroughputBps())
	}
	// Simulate a counter reset: recreate the log entry with lower counters.
	mt.LogEntry = NewLogEntry() // sent/recv back to 0 (< last sample's 5MB)
	mt.sampleThroughput(base + 2*s)
	if mt.GetThroughputBps() != 0 {
		t.Errorf("counter reset must not produce a spurious rate, got %v", mt.GetThroughputBps())
	}
}
