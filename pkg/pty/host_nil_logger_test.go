// Package pty pkg/pty/host_nil_logger_test.go c3-vis-pty
package pty

import (
	"context"
	"net/rpc"
	"net/url"
	"testing"
	"time"
)

// TestHostLog_NilDmsgClient covers the direct-TCP host, which is built as
// NewHost(nil, wl) by `cli pty host`: it has no dmsg client to borrow a
// logger from, so log() must fall back rather than dereference nil.
func TestHostLog_NilDmsgClient(t *testing.T) {
	h := NewHost(nil, NewMemoryWhitelist())
	if got := h.log(); got == nil {
		t.Fatal("log() returned nil for a host with no dmsg client")
	}
	h.log().Debug("must not panic")
}

// TestHandlePty_TeardownWithNilDmsgClient reproduces the actual crash: the
// classic path spawns a goroutine that calls h.log() once the connection's
// context is done, so a direct-TCP host panicked as soon as its first session
// ended. A panic in that goroutine takes the process down, so reaching the
// end of this test is the assertion.
func TestHandlePty_TeardownWithNilDmsgClient(t *testing.T) {
	h := NewHost(nil, NewMemoryWhitelist())

	ctx, cancel := context.WithCancel(context.Background())
	if err := handlePty(h)(ctx, &url.URL{}, rpc.NewServer()); err != nil {
		t.Fatalf("handlePty: %v", err)
	}

	// Canceling is what fires the teardown goroutine.
	cancel()
	time.Sleep(250 * time.Millisecond)
}
