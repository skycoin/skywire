package skysocks

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
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
