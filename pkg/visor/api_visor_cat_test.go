// Package visor unit tests for the splice helper used by VisorCat.
// We don't spin up the full visor RPC + dmsg stack here — the splice
// behavior is the only thing that's non-trivial to get right and can
// be exercised in isolation with net.Pipe pairs.
package visor

import (
	"bytes"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// TestSpliceBidirectional verifies bytes flow in both directions and
// the call returns cleanly when both ends close.
func TestSpliceBidirectional(t *testing.T) {
	// Two pipes — splice connects a1↔b1.
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()

	wantAtoB := []byte("a-to-b")
	wantBtoA := []byte("b-to-a")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = splice(a1, b1) //nolint:errcheck
	}()

	// Driver: write to a2 → splice copies to b1 → readable from b2.
	gotB := make([]byte, len(wantAtoB))
	done := make(chan struct{}, 2)
	go func() {
		if _, err := io.ReadFull(b2, gotB); err != nil {
			t.Errorf("read b2: %v", err)
		}
		done <- struct{}{}
	}()
	gotA := make([]byte, len(wantBtoA))
	go func() {
		if _, err := io.ReadFull(a2, gotA); err != nil {
			t.Errorf("read a2: %v", err)
		}
		done <- struct{}{}
	}()

	if _, err := a2.Write(wantAtoB); err != nil {
		t.Fatalf("write a2: %v", err)
	}
	if _, err := b2.Write(wantBtoA); err != nil {
		t.Fatalf("write b2: %v", err)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for splice direction %d", i)
		}
	}

	if !bytes.Equal(gotB, wantAtoB) {
		t.Fatalf("a→b: got %q want %q", gotB, wantAtoB)
	}
	if !bytes.Equal(gotA, wantBtoA) {
		t.Fatalf("b→a: got %q want %q", gotA, wantBtoA)
	}

	// Closing one driver-side closes the splice; verify it returns.
	_ = a2.Close() //nolint:errcheck
	_ = b2.Close() //nolint:errcheck
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatalf("splice did not return after both ends closed")
	}
}

// TestSpliceEOFClosesPeer verifies that EOF on one side tears down
// the other — important so a remote that hangs up doesn't leave
// stdio dangling.
func TestSpliceEOFClosesPeer(t *testing.T) {
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()

	done := make(chan error, 1)
	go func() {
		done <- splice(a1, b1)
	}()

	// Close one driver — peer should see EOF and splice should
	// return.
	_ = a2.Close() //nolint:errcheck

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("splice did not return after one side closed")
	}

	// b2 read should return EOF or ErrClosed (splice closed b1).
	_ = b2.SetReadDeadline(time.Now().Add(500 * time.Millisecond)) //nolint:errcheck
	buf := make([]byte, 1)
	if _, err := b2.Read(buf); err == nil {
		t.Fatalf("expected error reading b2 after splice tore down")
	}
	_ = b2.Close() //nolint:errcheck
}
