//go:build !tinygo && !(js && wasm)

package network

import (
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/0magnet/webrtc/v4"
)

// newTestDCConn builds a dcConn over a real PeerConnection, which is what the
// leak is about — the pc field has to be a live object for its teardown to be
// observable.
func newTestDCConn(t *testing.T) (*dcConn, *webrtc.PeerConnection) {
	t.Helper()
	pc, err := newWebRTCAPI().NewPeerConnection(webrtcConfig(nil))
	if err != nil {
		t.Fatalf("NewPeerConnection: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() }) //nolint:errcheck
	return newDCConn(newIdleRWC(), pc, nil), pc
}

// idleRWC is a data channel with nothing to read. Its Read blocks until Close,
// so the read pump cannot fail the conn before the test does.
type idleRWC struct {
	nopRWC
	once sync.Once
	done chan struct{}
}

func newIdleRWC() *idleRWC { return &idleRWC{done: make(chan struct{})} }

func (r *idleRWC) Read([]byte) (int, error) {
	<-r.done
	return 0, io.EOF
}

func (r *idleRWC) Close() error {
	r.once.Do(func() { close(r.done) })
	return nil
}

// TestDCConnFailThenCloseReleasesPeerConnection is the regression guard for a
// production leak: on two public visors ~1200 PeerConnections were alive
// against ~110 webrtc transports, pinning both at 96% CPU, and the population
// only ever grew.
//
// The sequence is the ordinary death of a WebRTC transport. The ICE path dies,
// pion reports Failed, the OnConnectionStateChange handler calls fail() to
// surface a read error, and the transport layer then tears the conn down with
// Close. Close used to see closed==true — set by fail, which frees nothing —
// and return before reaching pc.Close(), stranding the PeerConnection and its
// ~13 ICE/DTLS/SCTP/SRTP goroutines until the process exited.
func TestDCConnFailThenCloseReleasesPeerConnection(t *testing.T) {
	conn, pc := newTestDCConn(t)

	conn.fail(errors.New("webrtc: peer connection failed"))

	if got := pc.ConnectionState(); got == webrtc.PeerConnectionStateClosed {
		t.Fatal("fail() closed the PeerConnection by itself; this test no longer covers the ordering it was written for")
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := pc.ConnectionState(); got != webrtc.PeerConnectionStateClosed {
		t.Fatalf("PeerConnection left in %v after fail()+Close(), want Closed — this is the leak", got)
	}
}

// TestDCConnCloseReleasesPeerConnection covers the plain path, so a future
// change cannot fix the fail() ordering by breaking the ordinary one.
func TestDCConnCloseReleasesPeerConnection(t *testing.T) {
	conn, pc := newTestDCConn(t)

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := pc.ConnectionState(); got != webrtc.PeerConnectionStateClosed {
		t.Fatalf("PeerConnection left in %v after Close(), want Closed", got)
	}
}

// TestDCConnReleaseIsIdempotent pins that the teardown can be reached from
// both triggers — Close and the terminal-state handler — without closing
// anything twice. countingCloser stands in for the signaling stream, the one
// resource where a double Close was already a known hazard (see
// TestDCConnCloseDoesNotDoubleCloseSignal).
func TestDCConnReleaseIsIdempotent(t *testing.T) {
	pc, err := newWebRTCAPI().NewPeerConnection(webrtcConfig(nil))
	if err != nil {
		t.Fatalf("NewPeerConnection: %v", err)
	}
	signal := &countingCloser{}
	conn := newDCConn(nopRWC{}, pc, signal)

	conn.fail(errors.New("boom"))
	for i := 0; i < 3; i++ {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
	conn.release()

	if got := signal.n.Load(); got != 1 {
		t.Fatalf("signaling stream closed %d times, want exactly 1", got)
	}
}

// TestDCConnFailStillSurfacesItsError pins that moving the teardown does not
// cost the caller the reason. Read and Write answer from buffered state, so
// closing raw underneath them must not replace the ICE failure with a generic
// "closed".
func TestDCConnFailStillSurfacesItsError(t *testing.T) {
	conn, _ := newTestDCConn(t)

	want := errors.New("webrtc: peer connection failed")
	conn.fail(want)
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, want) {
		t.Fatalf("Read after fail+Close returned %v, want %v", err, want)
	}
	if _, err := conn.Write([]byte{0}); !errors.Is(err, want) {
		t.Fatalf("Write after fail+Close returned %v, want %v", err, want)
	}
}
