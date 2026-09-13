// Package clidmsg cmd/skywire-cli/commands/dmsg/pty_interrupt_test.go c4-vis-cli
package clidmsg

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestExecInterruptWatch_NormalReturnIsNotAnInterrupt is the regression for
// `pty exec` reporting "exec: interrupted" (exit 130) on runs that SUCCEEDED.
//
// ctx is a cmdutil.SignalContext, so its Done channel closes on the command's
// own deferred cancel exactly as it does on SIGINT. The watcher woke on every
// clean run and raced the process's own exit — measured 2 of 5 trivial
// `echo ok` execs failing this way at ~0.6s, after the real output had already
// been written. Scripted callers read that line as the result.
func TestExecInterruptWatch_NormalReturnIsNotAnInterrupt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var finished atomic.Bool
	var out bytes.Buffer
	exited := make(chan int, 1)

	done := make(chan struct{})
	go func() {
		execInterruptWatch(ctx, &finished, &out, func(code int) { exited <- code })
		close(done)
	}()

	// The command returns: finished is set BEFORE cancel runs, which is what
	// defer LIFO guarantees at the call site.
	finished.Store(true)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher never returned")
	}

	select {
	case code := <-exited:
		t.Fatalf("a completed command exited %d", code)
	default:
	}
	require.Empty(t, out.String(), "a completed command must print nothing")
}

// A real signal still has to interrupt: cancellation with finished unset is
// the SIGINT path, and it must report and exit 130.
func TestExecInterruptWatch_SignalStillInterrupts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var finished atomic.Bool
	var out bytes.Buffer
	exited := make(chan int, 1)

	go execInterruptWatch(ctx, &finished, &out, func(code int) { exited <- code })

	cancel() // signal arrives while the command is still running

	select {
	case code := <-exited:
		require.Equal(t, 130, code)
	case <-time.After(5 * time.Second):
		t.Fatal("a signal did not interrupt")
	}
	require.Contains(t, out.String(), "exec: interrupted")
}
