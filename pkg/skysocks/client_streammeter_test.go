// Package skysocks per-stream byte/rate accounting tests.
package skysocks

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/proxystatus"
)

// TestCountingConnMetersBothDirections verifies the countingConn credits reads to
// its rd counter and writes to its wr counter (used to meter a yamux exit stream:
// reads = exit→browser/down, writes = browser→exit/up).
func TestCountingConnMetersBothDirections(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close() //nolint:errcheck
	defer b.Close() //nolint:errcheck

	up, down := new(atomic.Uint64), new(atomic.Uint64)
	cc := &countingConn{Conn: a, rd: down, wr: up}

	// Write 5 bytes through cc (credits wr/up); the peer drains them.
	go func() {
		buf := make([]byte, 8)
		_, _ = b.Read(buf) //nolint:errcheck
	}()
	if _, err := cc.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := up.Load(); got != 5 {
		t.Fatalf("up counter = %d, want 5", got)
	}

	// Read 4 bytes through cc (credits rd/down).
	go func() { _, _ = b.Write([]byte("data")) }() //nolint:errcheck
	buf := make([]byte, 4)
	if _, err := cc.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := down.Load(); got != 4 {
		t.Fatalf("down counter = %d, want 4", got)
	}
}

// TestStreamSnapshotBytesAndRate verifies streamSnapshot reports the per-stream
// cumulative bytes and derives a smoothed rate from them.
func TestStreamSnapshotBytesAndRate(t *testing.T) {
	c := &Client{}
	up, down := c.addStream(9, "example.com:443")

	// Simulate 100 KiB up and 400 KiB down having flowed through the stream.
	up.Add(100 * 1024)
	down.Add(400 * 1024)

	// Let a real interval elapse so the first sample divides a real delta by a
	// real dt (well above rateSampleMin), seeding the rate directly.
	time.Sleep(rateSampleMin + 100*time.Millisecond)

	snaps := c.streamSnapshot()
	if len(snaps) != 1 {
		t.Fatalf("streamSnapshot len = %d, want 1", len(snaps))
	}
	s := snaps[0]
	if s.ID != 9 || s.Target != "example.com:443" {
		t.Fatalf("unexpected stream identity: %+v", s)
	}
	if s.SentBytes != 100*1024 || s.RecvBytes != 400*1024 {
		t.Fatalf("bytes = up %d / down %d, want 102400 / 409600", s.SentBytes, s.RecvBytes)
	}
	if s.SentRateBps <= 0 || s.RecvRateBps <= 0 {
		t.Fatalf("rate not derived: up %.1f down %.1f", s.SentRateBps, s.RecvRateBps)
	}
	// Down moved 4x the up bytes over the same interval, so its rate must exceed up.
	if s.RecvRateBps <= s.SentRateBps {
		t.Fatalf("down rate %.1f should exceed up rate %.1f", s.RecvRateBps, s.SentRateBps)
	}
}

// TestRepresentativeRouteRTT verifies the route-group rtt picks the fastest alive
// leg's route latency and ignores dead legs and unmeasured ones.
func TestRepresentativeRouteRTT(t *testing.T) {
	legs := []proxystatus.Leg{
		{Alive: false, RouteLatencyMS: 10},              // dead: ignored even though fastest
		{Alive: true, RouteLatencyMS: 250},              // alive
		{Alive: true, RouteLatencyMS: 0, LatencyMS: 90}, // no route rtt: falls back to first-hop
		{Alive: true, RouteLatencyMS: 140},              // alive, fastest live route rtt
	}
	if got := representativeRouteRTT(legs); got != 90 {
		// 90 (first-hop fallback) is the minimum among the alive candidates.
		t.Fatalf("representativeRouteRTT = %.0f, want 90", got)
	}
	if got := representativeRouteRTT(nil); got != 0 {
		t.Fatalf("representativeRouteRTT(nil) = %.0f, want 0", got)
	}
}
