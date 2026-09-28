// Package transport — pkg/transport/manager_close_test.go: Close must return
// after Serve.
//
// Serve adds its background goroutines to tm.wg and Close waits on tm.wg
// while holding tm.mx. When the Add count outgrew the goroutines (#4633
// dropped the discovery-conformance loop but kept Add(7)), Close never
// returned: every visor shutdown timed the transport manager out, and every
// WalkTransports caller blocked on tm.mx for good. A desktop process exits
// anyway; the phone core, which stops and starts the visor inside one
// process, leaked those goroutines on every restart.
package transport

import (
	"context"
	"testing"
	"time"
)

func TestManagerCloseAfterServe(t *testing.T) {
	tm := newTestManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm.Serve(ctx)

	closed := make(chan struct{})
	go func() {
		tm.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Manager.Close did not return after Serve: tm.wg is waiting on a goroutine Serve never started")
	}

	// The manager's lock is free again.
	walked := make(chan struct{})
	go func() {
		tm.WalkTransports(func(*ManagedTransport) bool { return true })
		close(walked)
	}()
	select {
	case <-walked:
	case <-time.After(5 * time.Second):
		t.Fatal("WalkTransports blocked after Close")
	}
}
