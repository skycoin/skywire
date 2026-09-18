// Package emu pkg/router/emu/netem_test.go
package emu

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/transport/network"
)

// TestConnIsNetworkTransport pins the contract the whole testbed rests on:
// an emulated end is what transport.NewManagedTransportForTest accepts.
func TestConnIsNetworkTransport(t *testing.T) {
	a, b := NewPair(PairConfig{Name: "iface"})
	defer a.Close() //nolint:errcheck
	var _ network.Transport = a
	var _ network.Transport = b
}

func readFrame(t *testing.T, c *Conn, within time.Duration) []byte {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(within)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1<<16)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return buf[:n]
}

// TestOneWayDelay proves a frame is held for the configured delay and not
// longer, and that frame boundaries survive the wire.
func TestOneWayDelay(t *testing.T) {
	a, b := NewPair(PairConfig{Name: "delay", AtoB: LinkConfig{Delay: 60 * time.Millisecond}})
	defer a.Close() //nolint:errcheck

	start := time.Now()
	if _, err := a.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	got := readFrame(t, b, time.Second)
	elapsed := time.Since(start)
	if string(got) != "hello" {
		t.Fatalf("frame boundary lost: %q", got)
	}
	if elapsed < 55*time.Millisecond || elapsed > 400*time.Millisecond {
		t.Fatalf("one-way delay %v, want ~60ms", elapsed)
	}
}

// TestRateLimitSerializes proves the token bucket produces real queueing: the
// Nth frame lands at N*len/rate, not after one per-frame sleep.
func TestRateLimitSerializes(t *testing.T) {
	const rate = 512 * 1024 // B/s
	a, b := NewPair(PairConfig{Name: "rate", AtoB: LinkConfig{RateBps: rate, QueueBytes: 1 << 20}})
	defer a.Close() //nolint:errcheck

	frame := make([]byte, 64*1024)
	start := time.Now()
	for i := 0; i < 4; i++ {
		if _, err := a.Write(frame); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		readFrame(t, b, 2*time.Second)
	}
	elapsed := time.Since(start)
	want := time.Duration(float64(4*len(frame)) / rate * float64(time.Second)) // 500ms
	if elapsed < want*8/10 || elapsed > want*3 {
		t.Fatalf("4x64KiB at %d B/s took %v, want ~%v", rate, elapsed, want)
	}
}

// TestQueueBlocksWriter proves a full egress queue applies backpressure
// rather than growing without bound — the socket-buffer behavior a real slow
// leg has, and what makes a leg's queueing delay finite.
func TestQueueBlocksWriter(t *testing.T) {
	a, b := NewPair(PairConfig{Name: "queue", AtoB: LinkConfig{RateBps: 256 * 1024, QueueBytes: 64 * 1024}})
	defer a.Close() //nolint:errcheck

	frame := make([]byte, 32*1024)
	start := time.Now()
	for i := 0; i < 6; i++ {
		if _, err := a.Write(frame); err != nil {
			t.Fatal(err)
		}
	}
	blocked := time.Since(start)
	if blocked < 300*time.Millisecond {
		t.Fatalf("writer was not blocked by a 64KiB queue at 256KiB/s: %v", blocked)
	}
	for i := 0; i < 6; i++ {
		readFrame(t, b, 3*time.Second)
	}
}

// TestLossIsSeededAndReproducible proves the drop decision is a function of
// the seed, so a loss scenario reproduces frame for frame.
func TestLossIsSeededAndReproducible(t *testing.T) {
	run := func() uint64 {
		a, b := NewPair(PairConfig{Name: "loss", AtoB: LinkConfig{LossPct: 30, Seed: 7}})
		defer a.Close() //nolint:errcheck
		defer b.Close() //nolint:errcheck
		for i := 0; i < 200; i++ {
			if _, err := a.Write([]byte{byte(i)}); err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if a.Egress().Stats().DeliveredFrames+a.Egress().Stats().DroppedFrames >= 200 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		return a.Egress().Stats().DroppedFrames
	}
	first, second := run(), run()
	if first != second {
		t.Fatalf("same seed dropped %d then %d frames — loss is not reproducible", first, second)
	}
	if first < 40 || first > 80 {
		t.Fatalf("30%% loss over 200 frames dropped %d, want roughly 60", first)
	}
}

// TestReorderDelaysAFrame proves the reorder knob actually delivers a frame
// after one sent behind it.
func TestReorderDelaysAFrame(t *testing.T) {
	a, b := NewPair(PairConfig{Name: "reorder", AtoB: LinkConfig{
		Delay: 10 * time.Millisecond, ReorderPct: 100, ReorderDelay: 80 * time.Millisecond, Seed: 1}})
	defer a.Close() //nolint:errcheck

	// Only the first frame is reordered: flip the knob off right after.
	if _, err := a.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	a.Egress().SetReorderPct(0)
	time.Sleep(5 * time.Millisecond)
	if _, err := a.Write([]byte{2}); err != nil {
		t.Fatal(err)
	}
	first := readFrame(t, b, time.Second)
	second := readFrame(t, b, time.Second)
	if first[0] != 2 || second[0] != 1 {
		t.Fatalf("frames arrived %d,%d — the reorder knob did not reorder", first[0], second[0])
	}
}

// TestCutBlackHoles proves a cut drops the in-flight frame too, and that
// Restore brings the direction back without reopening the conn.
func TestCutBlackHoles(t *testing.T) {
	a, b := NewPair(PairConfig{Name: "cut", AtoB: LinkConfig{Delay: 50 * time.Millisecond}})
	defer a.Close() //nolint:errcheck

	if _, err := a.Write([]byte("inflight")); err != nil {
		t.Fatal(err)
	}
	a.Egress().Cut()
	if _, err := a.Write([]byte("after-cut")); err != nil {
		t.Fatalf("a cut link must still ACCEPT writes (the socket is open): %v", err)
	}
	if err := b.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Read(make([]byte, 64)); err == nil {
		t.Fatalf("cut link delivered %d bytes", n)
	}

	a.Egress().Restore()
	if _, err := a.Write([]byte("restored")); err != nil {
		t.Fatal(err)
	}
	if got := string(readFrame(t, b, time.Second)); got != "restored" {
		t.Fatalf("after Restore got %q", got)
	}
	if st := a.Egress().Stats(); st.CutFrames == 0 || st.DroppedFrames < 2 {
		t.Fatalf("cut accounting: %+v", st)
	}
}

// TestPairPreservesOrderWithoutImpairment is the regression for the reordering
// that made pkg/router's TestTransitWriteStillRelaysInOrder fail on CI's linux
// and windows lanes: a direction with nothing configured must deliver frames in
// the order they were written. Delivery was one already-expired time.AfterFunc
// per frame, so the runtime pushed them into the peer's inbox in whatever order
// it liked — 20 of 20 rounds reordered before the fix.
func TestPairPreservesOrderWithoutImpairment(t *testing.T) {
	const rounds, frames = 20, 64
	for round := 0; round < rounds; round++ {
		a, b := NewPair(PairConfig{Name: "order"})
		for i := 0; i < frames; i++ {
			if _, err := a.Write([]byte{byte(i)}); err != nil {
				t.Fatalf("round %d: write %d: %v", round, i, err)
			}
		}
		for i := 0; i < frames; i++ {
			got := readFrame(t, b, 5*time.Second)
			if len(got) != 1 || got[0] != byte(i) {
				t.Fatalf("round %d: frame %d arrived as %v", round, i, got)
			}
		}
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
