//go:build !tinygo && !(js && wasm)

package network

import (
	"errors"
	"sync/atomic"
	"testing"
)

// countingCloser records how many times Close was called.
type countingCloser struct{ n atomic.Int32 }

func (c *countingCloser) Close() error {
	c.n.Add(1)
	return nil
}

// TestReleaseSignalingClosesOnce pins that the signaling stream is closed when
// handed off. The stream used to be retained for the transport's lifetime with
// no reader, holding a reserved dmsg port each — 31 of a host visor's 94.
func TestReleaseSignalingClosesOnce(t *testing.T) {
	c := &countingCloser{}
	releaseSignaling(c)
	if got := c.n.Load(); got != 1 {
		t.Fatalf("signaling stream closed %d times, want 1", got)
	}
}

// TestReleaseSignalingNilSafe covers the accept path, where a conn may be
// constructed without a signaling stream.
func TestReleaseSignalingNilSafe(t *testing.T) {
	releaseSignaling(nil) // must not panic
}

// TestDCConnCloseDoesNotDoubleCloseSignal is the regression guard for the
// handoff: newDCConn is now given a nil signal because releaseSignaling already
// closed it. If a future change passes the live stream again, the transport
// would close an already-closed dmsg stream on teardown.
func TestDCConnCloseDoesNotDoubleCloseSignal(t *testing.T) {
	c := &countingCloser{}
	releaseSignaling(c)

	api := newWebRTCAPI()
	pc, err := api.NewPeerConnection(webrtcConfig(nil))
	if err != nil {
		t.Fatalf("NewPeerConnection: %v", err)
	}
	defer func() { _ = pc.Close() }() //nolint:errcheck

	conn := newDCConn(nopRWC{}, pc, nil)
	if conn.signal != nil {
		t.Fatal("dcConn must not retain the signaling stream after handoff")
	}
	if got := c.n.Load(); got != 1 {
		t.Fatalf("signaling stream closed %d times, want exactly 1", got)
	}
}

// nopRWC satisfies the detached-DataChannel interface for construction only.
type nopRWC struct{}

func (nopRWC) Read([]byte) (int, error)  { return 0, errors.New("closed") }
func (nopRWC) Write([]byte) (int, error) { return 0, errors.New("closed") }
func (nopRWC) ReadDataChannel([]byte) (int, bool, error) {
	return 0, false, errors.New("closed")
}
func (nopRWC) WriteDataChannel([]byte, bool) (int, error) { return 0, errors.New("closed") }
func (nopRWC) Close() error                               { return nil }
