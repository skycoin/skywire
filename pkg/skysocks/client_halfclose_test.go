package skysocks

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// TestSpliceSurvivesTheClientsHalfClose pins the reply against a client that
// has finished sending.
//
// splicePrefixed used to tear down BOTH directions as soon as the first copy
// returned, so a client that shut down its write side killed the stream
// carrying its own reply: an empty body and no error, which is data loss
// wearing the shape of success. Measured through a live exit, an HTTP GET
// followed by a half close returned 0 bytes where the same request without one
// returned 29359.
//
// The exit here answers only after reading the request to EOF, which is what
// makes the test deterministic: the truncation cannot be masked by a reply
// that happened to arrive before the teardown.
func TestSpliceSurvivesTheClientsHalfClose(t *testing.T) {
	body := bytes.Repeat([]byte("the reply the client is still waiting for. "), 400)

	clientSide, connSide := tcpPair(t) // the browser's end / the splice's end
	streamSide, exitSide := tcpPair(t) // the splice's end / the exit's end

	replied := make(chan struct{})
	go func() {
		defer close(replied)
		got, err := io.ReadAll(exitSide)
		if err != nil || !bytes.Equal(got, []byte("REQUEST")) {
			return
		}
		exitSide.Write(body) //nolint:errcheck,gosec // the client end asserts on what arrives
		exitSide.Close()     //nolint:errcheck,gosec // the reply is complete
	}()

	c := &Client{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.splicePrefixed(connSide, streamSide, nil)
	}()

	if _, err := clientSide.Write([]byte("REQUEST")); err != nil {
		t.Fatalf("write request: %v", err)
	}
	// The request is complete, so shutting down the write side is legitimate —
	// and the whole reply must still arrive.
	if err := clientSide.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}

	if err := clientSide.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	got, err := io.ReadAll(clientSide)
	if err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	if len(got) != len(body) {
		t.Fatalf("the client's half close truncated the reply: got %d bytes, want %d", len(got), len(body))
	}
	if !bytes.Equal(got, body) {
		t.Fatal("the reply arrived corrupted")
	}

	clientSide.Close() //nolint:errcheck,gosec // releasing the splice
	<-done
	<-replied
}

// spliceNoFIN runs splicePrefixed against an exit that answers the request in
// pieces, gap apart, and then never closes — every exit before #5123 on origin
// EOF. finish ends the client's side once the request is out.
func spliceNoFIN(t *testing.T, pieces int, gap time.Duration, finish func(net.Conn)) (got []byte, spliceDone <-chan struct{}) {
	t.Helper()
	t.Cleanup(func() { skysettings.Reset() })
	skysettings.Apply(map[string]int64{skysettings.ChunkIdleTimeout: int64(300 * time.Millisecond)})

	clientSide, connSide := tcpPair(t)
	streamSide, exitSide := tcpPair(t)
	t.Cleanup(func() { _ = exitSide.Close() }) //nolint:errcheck
	go func() {
		if _, err := io.ReadFull(exitSide, make([]byte, len("REQUEST"))); err != nil {
			return
		}
		for i := 0; i < pieces; i++ {
			time.Sleep(gap)
			if _, err := exitSide.Write([]byte("piece.")); err != nil {
				return
			}
		}
		_, _ = io.Copy(io.Discard, exitSide) //nolint:errcheck // hold, never FIN
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		(&Client{}).splicePrefixed(connSide, streamSide, nil)
	}()
	if _, err := clientSide.Write([]byte("REQUEST")); err != nil {
		t.Fatalf("write request: %v", err)
	}
	finish(clientSide)
	_ = clientSide.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	got, _ = io.ReadAll(clientSide)                                 //nolint:errcheck // a fully closed client reads nothing
	return got, done
}

// After a half close, a reply still trickling in (gaps under the idle
// deadline) arrives whole, and an exit that then never FINs no longer holds
// the splice: it ends one idle deadline after the last byte.
func TestSpliceIdleDeadlineAfterHalfCloseWithoutFIN(t *testing.T) {
	got, done := spliceNoFIN(t, 5, 150*time.Millisecond, func(c net.Conn) {
		if err := c.(*net.TCPConn).CloseWrite(); err != nil {
			t.Fatalf("CloseWrite: %v", err)
		}
	})
	if want := bytes.Repeat([]byte("piece."), 5); !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q: the half close or the deadline truncated the reply", got, want)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the splice leaked: the exit never sent FIN and the pair stayed open")
	}
}

// A client that goes away entirely releases the splice the same way.
func TestSpliceIdleDeadlineAfterClientCloseWithoutFIN(t *testing.T) {
	_, done := spliceNoFIN(t, 1, 0, func(c net.Conn) { _ = c.Close() }) //nolint:errcheck
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the splice leaked after the client closed")
	}
}
