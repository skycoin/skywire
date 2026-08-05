package call

import (
	"io"
	"math"
	"testing"
)

// closeIt closes a Source/Sink the way Session.Close does.
func closeIt(t *testing.T, v any) {
	t.Helper()
	c, ok := v.(io.Closer)
	if !ok {
		t.Fatalf("%T does not implement io.Closer — Session.Close would never unregister it", v)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestBridgeCaptureReachesTheCall(t *testing.T) {
	b := NewBridge()
	src := b.Source()

	b.Push([]int16{1, 2, 3})

	got := make([]int16, 4)
	if n, err := src.Read(got); err != nil || n != len(got) {
		t.Fatalf("Read() = %d, %v; want %d, nil", n, err, len(got))
	}
	// The tail is padded rather than withheld: the send loop needs a frame
	// every tick even when capture is behind.
	if want := []int16{1, 2, 3, 0}; !equal(got, want) {
		t.Fatalf("Read() = %v, want %v", got, want)
	}
}

// Two calls must each get their OWN copy of the microphone. Sharing one buffer
// would let whichever session read first swallow the other's audio.
func TestBridgeCaptureFansOutToEveryCall(t *testing.T) {
	b := NewBridge()
	first, second := b.Source(), b.Source()

	b.Push([]int16{7, 8})

	for i, src := range []Source{first, second} {
		got := make([]int16, 2)
		if _, err := src.Read(got); err != nil {
			t.Fatalf("source %d: Read() = %v", i, err)
		}
		if want := []int16{7, 8}; !equal(got, want) {
			t.Fatalf("source %d: Read() = %v, want %v", i, got, want)
		}
	}
}

func TestBridgePlaybackReachesTheDevice(t *testing.T) {
	b := NewBridge()
	sink := b.Sink()

	if n, err := sink.Write([]int16{4, 5}); err != nil || n != 2 {
		t.Fatalf("Write() = %d, %v; want 2, nil", n, err)
	}

	got := make([]int16, 3)
	if n := b.Pull(got); n != len(got) {
		t.Fatalf("Pull() = %d, want %d — playback must always get a full frame", n, len(got))
	}
	if want := []int16{4, 5, 0}; !equal(got, want) {
		t.Fatalf("Pull() = %v, want %v", got, want)
	}
}

// One speaker, two calls: the device hears the sum, not one of them.
func TestBridgePlaybackMixesAndClips(t *testing.T) {
	b := NewBridge()
	first, second := b.Sink(), b.Sink()

	if _, err := first.Write([]int16{100, math.MaxInt16, math.MinInt16}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Write([]int16{-30, 1000, -1000}); err != nil {
		t.Fatal(err)
	}

	got := make([]int16, 3)
	b.Pull(got)
	// Clipped, not wrapped: wrapping turns a loud moment into a burst of noise.
	if want := []int16{70, math.MaxInt16, math.MinInt16}; !equal(got, want) {
		t.Fatalf("Pull() = %v, want %v", got, want)
	}
}

func TestBridgeIdleIsSilentNotBroken(t *testing.T) {
	b := NewBridge()
	if b.Live() {
		t.Fatal("Live() = true with no call holding the device")
	}
	// An app pushing before any call exists, and pulling with nothing to play,
	// are both normal: it is a device, not a session.
	b.Push([]int16{1, 2, 3})
	got := []int16{9, 9, 9}
	if n := b.Pull(got); n != 3 {
		t.Fatalf("Pull() = %d, want 3", n)
	}
	if want := []int16{0, 0, 0}; !equal(got, want) {
		t.Fatalf("Pull() = %v, want silence %v", got, want)
	}
}

// A finished call must let go of the device, or the app would keep feeding a
// ring nobody reads and Live() would never go false.
func TestBridgeCloseReleasesTheDevice(t *testing.T) {
	b := NewBridge()
	src, sink := b.Source(), b.Sink()
	if !b.Live() {
		t.Fatal("Live() = false while a call holds the device")
	}

	closeIt(t, src)
	if !b.Live() {
		t.Fatal("Live() = false while the sink is still open")
	}
	closeIt(t, sink)
	if b.Live() {
		t.Fatal("Live() = true after every call let go")
	}

	// Still a usable device afterwards — the app's loops may outlive a call by
	// a tick or two and must not panic or wedge.
	b.Push([]int16{1})
	got := make([]int16, 2)
	if n := b.Pull(got); n != 2 {
		t.Fatalf("Pull() after close = %d, want 2", n)
	}
}

func equal(a, b []int16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
