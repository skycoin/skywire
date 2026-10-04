//go:build js && wasm

// Package dmsg pkg/dmsg/dmsg/wt_js_write_deadline_test.go c1-net-dmsg
// covers the write half of wtConnJS deadlines. writer.write resolves only once
// the peer drains, so a stalled peer parks the writing goroutine until the
// deadline aborts the writer.
package dmsg

import (
	"errors"
	"net"
	"syscall/js"
	"testing"
	"time"
)

// stalledWriter is a WritableStreamDefaultWriter whose write never settles on its
// own. abort rejects the pending write, which is what the Streams spec requires
// (https://streams.spec.whatwg.org/#writablestreamdefaultwriter-abort).
const stalledWriter = `(() => {
	let rejectPending = null;
	let aborted = false;
	return {
		write: () => new Promise((_resolve, reject) => { rejectPending = reject; }),
		abort: () => {
			aborted = true;
			if (rejectPending) { rejectPending(new Error("aborted")); }
			return Promise.resolve();
		},
		aborted: () => aborted,
	};
})()`

const instantWriter = `(() => {
	let aborted = false;
	return {
		write: () => Promise.resolve(),
		abort: () => { aborted = true; return Promise.resolve(); },
		aborted: () => aborted,
	};
})()`

func TestWTConnJSWriteDeadlineAbortsAStalledWrite(t *testing.T) {
	writer := js.Global().Call("eval", stalledWriter)
	c := &wtConnJS{writer: writer, notify: make(chan struct{})}

	if err := c.SetDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := c.Write([]byte("hello"))
		done <- err
	}()

	select {
	case err := <-done:
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("want a net.Error that reports Timeout, got %v (%T)", err, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Write never returned, so the write deadline did not bound it")
	}

	if !writer.Call("aborted").Bool() {
		t.Error("the deadline did not abort the writer")
	}
}

// TestWTConnJSSetDeadlineSetsBothHalves guards the regression this fixes, where
// SetDeadline forwarded to SetReadDeadline only and left writes unbounded.
func TestWTConnJSSetDeadlineSetsBothHalves(t *testing.T) {
	c := &wtConnJS{notify: make(chan struct{})}
	want := time.Now().Add(time.Minute)
	if err := c.SetDeadline(want); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	c.mu.Lock()
	gotRead, gotWrite := c.rDeadline, c.wDeadline
	c.mu.Unlock()
	if !gotRead.Equal(want) {
		t.Errorf("read deadline = %v, want %v", gotRead, want)
	}
	if !gotWrite.Equal(want) {
		t.Errorf("write deadline = %v, want %v", gotWrite, want)
	}
}

// TestWTConnJSWriteUnderDeadlineSucceeds checks that an armed deadline that does
// not elapse leaves the write alone and does not abort the writer.
func TestWTConnJSWriteUnderDeadlineSucceeds(t *testing.T) {
	writer := js.Global().Call("eval", instantWriter)
	c := &wtConnJS{writer: writer, notify: make(chan struct{})}
	if err := c.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	n, err := c.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 5 {
		t.Errorf("wrote %d bytes, want 5", n)
	}
	if writer.Call("aborted").Bool() {
		t.Error("a write that beat its deadline aborted the writer")
	}
}

// TestWTConnJSWritePastDeadlineFailsFast covers an already elapsed deadline,
// which must not reach writer.write at all.
func TestWTConnJSWritePastDeadlineFailsFast(t *testing.T) {
	writer := js.Global().Call("eval", stalledWriter)
	c := &wtConnJS{writer: writer, notify: make(chan struct{})}
	if err := c.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	var ne net.Error
	if _, err := c.Write([]byte("hello")); !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("want a timeout error, got %v (%T)", err, err)
	}
}
