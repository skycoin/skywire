package router

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestRemoteThroughputZeroWindow is the regression guard: sampling twice inside
// one clock tick left timePassed == 0, and float64->int64 is undefined in Go for
// the resulting Inf/NaN — on amd64 it yields math.MinInt64. That value is not
// local; it goes into the ping packet sent to the peer, so a nonsense negative
// throughput propagated into the remote's view of the leg. Windows CI hits this
// deterministically because its timer granularity is coarse.
func TestRemoteThroughputZeroWindow(t *testing.T) {
	s := &networkStats{}
	now := time.Now().UTC()
	s.bandwidthReceivedRecStart = now

	// Bytes counted, but no time has passed: the +Inf case.
	atomic.StoreUint64(&s.bandwidthReceived, 4096)
	if got := s.remoteThroughputAt(now); got != 0 {
		t.Errorf("zero-width window with traffic returned %d, want 0", got)
	}

	// No bytes and no time: the NaN case.
	atomic.StoreUint64(&s.bandwidthReceived, 0)
	if got := s.remoteThroughputAt(now); got != 0 {
		t.Errorf("zero-width window with no traffic returned %d, want 0", got)
	}
}

// TestRemoteThroughputZeroWindowKeepsBytes pins that an unmeasurable sample does
// not silently discard the bytes counted so far — the next real window must
// still see them, or a fast poller would zero out the accumulator forever.
func TestRemoteThroughputZeroWindowKeepsBytes(t *testing.T) {
	s := &networkStats{}
	start := time.Now().UTC()
	s.bandwidthReceivedRecStart = start

	atomic.StoreUint64(&s.bandwidthReceived, 1000)
	if got := s.remoteThroughputAt(start); got != 0 {
		t.Fatalf("zero-width window returned %d, want 0", got)
	}
	if got := atomic.LoadUint64(&s.bandwidthReceived); got != 1000 {
		t.Fatalf("zero-width window consumed the accumulator: %d bytes left, want 1000", got)
	}

	// One second later the same bytes must be reported as throughput.
	if got := s.remoteThroughputAt(start.Add(time.Second)); got != 1000 {
		t.Errorf("throughput over a 1s window = %d, want 1000", got)
	}
}

// TestRemoteThroughputBackwardsClock covers an NTP step: a negative window must
// not produce a negative throughput.
func TestRemoteThroughputBackwardsClock(t *testing.T) {
	s := &networkStats{}
	now := time.Now().UTC()
	s.bandwidthReceivedRecStart = now.Add(time.Second) // start is in the future

	atomic.StoreUint64(&s.bandwidthReceived, 4096)
	if got := s.remoteThroughputAt(now); got != 0 {
		t.Errorf("backwards clock returned %d, want 0", got)
	}
}

// TestRemoteThroughputNormalWindow keeps the ordinary path honest.
func TestRemoteThroughputNormalWindow(t *testing.T) {
	s := &networkStats{}
	start := time.Now().UTC()
	s.bandwidthReceivedRecStart = start

	atomic.StoreUint64(&s.bandwidthReceived, 8192)
	got := s.remoteThroughputAt(start.Add(2 * time.Second))
	if got != 4096 {
		t.Errorf("throughput = %d, want 4096 (8192 bytes over 2s)", got)
	}
}
