// Package commands cmd/apps/skychat/commands/framedconn_writedeadline_test.go:
// pins the bounded-write behavior of framedConn.WriteFrameDeadline so a
// slow / hung peer transport can't pin the /message HTTP handler
// goroutine indefinitely.
//
// Pre-fix the chat-app's messageHandler called conn.WriteFrame, which
// blocks inside the inner c.Conn.Write call with no deadline. A peer
// whose visor was crashlooping (so its dmsg listener side absorbed but
// never drained writes) wedged the handler's goroutine. Worse,
// c.writeMu was held the whole time, so EVERY subsequent /message to
// the same peer queued behind the hung write. Observed cross-peer
// cascade on 2026-05-17 around 20:20Z: a single Beta-send hang
// preceded every subsequent /message — to Alpha, Beta, anyone — timing
// out at the outer HTTP context deadline (~30 s) for hours after.
// Bounding the inner Write lets the handler return cleanly, the
// writeMu releases, and the next caller is freed.

package commands

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func TestFramedConn_WriteFrameDeadline_TimesOutOnStalledPeer(t *testing.T) {
	// Build a real TCP pair via net.Pipe so the underlying conn
	// honors SetWriteDeadline. Receiver never reads, so once the
	// kernel send buffer fills (or in net.Pipe's case, the first
	// write blocks immediately since pipes are unbuffered) the
	// write blocks. Without a deadline the WriteFrame call would
	// pin the goroutine; with one it returns net.Error{Timeout: true}.
	srvSide, cliSide := net.Pipe()
	defer cliSide.Close() //nolint:errcheck,gosec
	defer srvSide.Close() //nolint:errcheck,gosec
	// Don't drain srvSide — that's the whole point.

	c := newFramedConn(cliSide)

	const timeout = 100 * time.Millisecond
	start := time.Now()
	err := c.WriteFrameDeadline([]byte("hello"), timeout)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("WriteFrameDeadline against an undrained peer: want timeout error, got nil")
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Errorf("WriteFrameDeadline err = %v (Timeout()=%v); want net.Error with Timeout()==true", err, ne != nil && ne.Timeout())
	}
	if elapsed < timeout {
		t.Errorf("WriteFrameDeadline returned in %v (before %v deadline) — deadline did not bound the write", elapsed, timeout)
	}
	if elapsed > timeout+500*time.Millisecond {
		// Allow scheduler slop but flag a really late return so the
		// deadline machinery isn't silently broken.
		t.Errorf("WriteFrameDeadline returned %v after the %v deadline — net.Conn.SetWriteDeadline appears not to bound this transport", elapsed-timeout, timeout)
	}
}

func TestFramedConn_WriteFrameDeadline_DeadlineResetForNextCaller(t *testing.T) {
	// After a deadline-bounded write times out, the next call must
	// not inherit the expired deadline — pre-fix a lingering
	// SetWriteDeadline could pre-expire subsequent writes on the
	// same conn. Verify by stalling-then-draining: first call hits
	// the deadline; reader starts draining; second call must
	// succeed (with a non-expired deadline) instead of returning
	// instant-timeout because of leftover state.
	//
	// Skipped on net.Pipe because pipes don't honor concurrent
	// drain semantics the way TCP does. Build a localhost TCP
	// pair instead.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer lis.Close() //nolint:errcheck,gosec

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, aErr := lis.Accept()
		if aErr != nil {
			return
		}
		accepted <- conn
	}()

	cli, err := net.Dial("tcp", lis.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer cli.Close() //nolint:errcheck,gosec
	srv := <-accepted
	defer srv.Close() //nolint:errcheck,gosec

	c := newFramedConn(cli)

	// First call: timeout immediately by setting the deadline to a
	// past instant. Pre-fix the deadline would not get reset after
	// this call and a subsequent SetWriteDeadline call in
	// WriteFrameDeadline would re-establish a deadline — but if a
	// hypothetical buggy fix skipped the reset, the next plain
	// WriteFrame (no deadline arg) would inherit the past deadline
	// and instant-fail. Cover both paths.

	// Fill the buffer by writing a large payload until it blocks,
	// then trigger a short-timeout call. To keep the test bounded
	// and portable, just use a short timeout on a small payload —
	// TCP send buffer isn't reliably exhausted by one frame, but
	// the deadline expires the SECOND write call (payload) when
	// the kernel buffer fills.
	largePayload := make([]byte, 64*1024)
	for i := range largePayload {
		largePayload[i] = 'x'
	}

	// Repeated writes against an unread srv: eventually one blocks.
	// Cap the loop so a kernel with a large buffer doesn't spin
	// forever.
	const maxWrites = 64
	var lastErr error
	var lastWasTimeout bool
	for i := 0; i < maxWrites; i++ {
		err := c.WriteFrameDeadline(largePayload, 50*time.Millisecond)
		if err != nil {
			lastErr = err
			var ne net.Error
			lastWasTimeout = errors.As(err, &ne) && ne.Timeout()
			break
		}
	}
	if lastErr == nil {
		t.Skip("Could not stall the TCP send buffer within bounded write loop; this kernel/buffer combination doesn't reproduce the deadline path")
	}
	if !lastWasTimeout {
		// Non-timeout error (e.g. broken pipe) — still proves the
		// deadline didn't wedge the goroutine; skip the
		// reset-verification half.
		t.Logf("first-blocking-write returned non-timeout: %v — deadline still bounded the call, but skipping reset check on this kernel", lastErr)
		return
	}

	// Now drain srv to free the buffer.
	go func() {
		buf := make([]byte, 16*1024)
		for {
			if _, err := srv.Read(buf); err != nil {
				return
			}
		}
	}()
	// Give the drainer a moment to release pressure.
	time.Sleep(50 * time.Millisecond)

	// Plain WriteFrame (no explicit deadline). Pre-buggy-fix this
	// could have inherited the now-expired deadline from
	// WriteFrameDeadline's stale SetWriteDeadline call. Post-fix
	// the deadline was reset to zero-time on the defer, so this
	// call uses no deadline and succeeds.
	if err := c.WriteFrame([]byte("after-reset")); err != nil {
		t.Errorf("plain WriteFrame after deadline-bounded timeout: got %v, want nil (deadline reset should leave the conn unbounded for subsequent calls)", err)
	}
}

// recordingConn wraps a net.Conn and records every SetWriteDeadline
// argument in order. Propagates the call to the embedded conn so
// the underlying transport's deadline machinery still fires —
// without this propagation the inner Write would block forever
// while the test only watched the recorder.
type recordingConn struct {
	net.Conn
	mu        sync.Mutex
	deadlines []time.Time
}

func (r *recordingConn) SetWriteDeadline(t time.Time) error {
	r.mu.Lock()
	r.deadlines = append(r.deadlines, t)
	r.mu.Unlock()
	return r.Conn.SetWriteDeadline(t)
}

// TestFramedConn_WriteFrameDeadline_SetsAndResets verifies the
// envelope contract directly: SetWriteDeadline is called twice per
// WriteFrameDeadline invocation — once with a non-zero deadline on
// entry, once with time.Time{} (the "no deadline" sentinel) on
// exit — regardless of whether the Write itself succeeded or
// timed out. This is the portable contract test that doesn't
// depend on kernel buffer behavior or net.Conn flavor.
func TestFramedConn_WriteFrameDeadline_SetsAndResets(t *testing.T) {
	srv, cli := net.Pipe()
	defer srv.Close() //nolint:errcheck,gosec
	defer cli.Close() //nolint:errcheck,gosec

	rc := &recordingConn{Conn: cli}
	c := newFramedConn(rc)

	// Short timeout — the Write will time out (net.Pipe honors
	// deadlines via pipeDeadline) since srv isn't being drained.
	// We don't care about the timeout outcome here; we care about
	// the deadline-setting envelope.
	_ = c.WriteFrameDeadline([]byte("ping"), 50*time.Millisecond) //nolint:errcheck

	rc.mu.Lock()
	defer rc.mu.Unlock()
	if len(rc.deadlines) < 2 {
		t.Fatalf("WriteFrameDeadline made %d SetWriteDeadline calls; want >=2 (set + reset)", len(rc.deadlines))
	}
	// First call: non-zero deadline ~50ms in the future.
	if rc.deadlines[0].IsZero() {
		t.Errorf("first SetWriteDeadline arg was zero-time; want a future deadline ~now+timeout")
	}
	// Last call: time.Time{} reset.
	last := rc.deadlines[len(rc.deadlines)-1]
	if !last.IsZero() {
		t.Errorf("last SetWriteDeadline arg was %v; want time.Time{} (the 'no deadline' sentinel) so subsequent calls don't inherit an expired deadline", last)
	}
}
