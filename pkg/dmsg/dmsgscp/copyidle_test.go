// Package dmsgscp pkg/dmsg/dmsgscp/copyidle_test.go c1-net-dmsg
package dmsgscp

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCopyNIdle_ProgressNeverTimesOut: a transfer whose TOTAL duration far
// exceeds the idle timeout completes anyway, because bytes keep moving with
// each gap under the idle window. This is the property the old fixed
// whole-transfer cap lacked.
func TestCopyNIdle_ProgressNeverTimesOut(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close() //nolint:errcheck
	defer c2.Close() //nolint:errcheck

	chunk := bytes.Repeat([]byte("x"), 4096)
	const chunks = 15
	total := chunks * len(chunk)

	// Gap and idle window are kept a full order of magnitude apart (50ms vs
	// 500ms) so ordinary CI scheduling jitter — which on a loaded Windows
	// runner once stretched a nominal 15ms gap past a 60ms window and tripped
	// a spurious "read pipe: i/o timeout" — cannot cross the idle deadline.
	const gap = 50 * time.Millisecond
	go func() {
		for i := 0; i < chunks; i++ {
			_ = c2.SetWriteDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck // test writer deadline is best-effort
			if _, err := c2.Write(chunk); err != nil {
				return
			}
			time.Sleep(gap) // gap << idle, but sum >> idle
		}
	}()

	var buf bytes.Buffer
	// idle 500ms; total transfer ~750ms. Must NOT time out.
	n, err := copyNIdle(&buf, c1, int64(total), c1, 500*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, int64(total), n)
	require.Equal(t, total, buf.Len())
}

// TestCopyNIdle_StallAbortsFast: a stalled transfer (no bytes moving) aborts
// within a small multiple of the idle window — not never, and not after a
// coarse whole-transfer cap.
func TestCopyNIdle_StallAbortsFast(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close() //nolint:errcheck
	defer c2.Close() //nolint:errcheck

	var buf bytes.Buffer
	start := time.Now()
	_, err := copyNIdle(&buf, c1, 100, c1, 30*time.Millisecond) // peer never writes
	elapsed := time.Since(start)

	require.Error(t, err)
	require.Less(t, elapsed, 300*time.Millisecond, "a stall must abort within a few idle windows")
}

// TestCopyNIdle_ZeroIdleUnbounded: idle<=0 disables the deadline entirely (the
// legacy behavior) and still copies correctly from a non-blocking source.
func TestCopyNIdle_ZeroIdleUnbounded(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close() //nolint:errcheck
	defer c2.Close() //nolint:errcheck

	src := bytes.NewReader(bytes.Repeat([]byte("y"), 5000))
	var buf bytes.Buffer
	n, err := copyNIdle(&buf, src, 5000, c1, 0) // conn unused when idle<=0
	require.NoError(t, err)
	require.Equal(t, int64(5000), n)
	require.Equal(t, 5000, buf.Len())
}
