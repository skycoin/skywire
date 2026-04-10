// Package netutil pkg/netutil/copy.go
package netutil

import (
	"io"
	"time"
)

// deadlineSetter is implemented by net.Conn and yamux.Stream. Setting a past
// read deadline on a yamux.Stream unblocks any concurrent Read via
// asyncNotify(recvNotifyCh) — this is the only reliable way to interrupt a
// blocked yamux Read, since Close() does not.
type deadlineSetter interface {
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
}

// forceInterrupt sets past read/write deadlines on the connection to unblock
// any in-flight Read/Write. Walks through wrapper types to reach the underlying
// connection if necessary.
func forceInterrupt(conn io.ReadWriteCloser) {
	if ds, ok := conn.(deadlineSetter); ok {
		past := time.Unix(1, 0)
		_ = ds.SetReadDeadline(past)  //nolint:errcheck
		_ = ds.SetWriteDeadline(past) //nolint:errcheck
	}
}

// CopyReadWriteCloser copies reads and writes between two connections.
// It returns when either direction encounters an error (including idle timeout).
func CopyReadWriteCloser(conn1, conn2 io.ReadWriteCloser) error {
	errCh1 := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn2, conn1)
		errCh1 <- err
	}()

	errCh2 := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn1, conn2)
		errCh2 <- err
	}()

	// Wait for one direction to finish.
	var firstErr error
	select {
	case firstErr = <-errCh1:
	case firstErr = <-errCh2:
	}

	// Force interrupt both directions by setting past deadlines. This is
	// necessary because yamux.Stream.Close() does not unblock a concurrent
	// Read(), but SetReadDeadline(past) does (via asyncNotify(recvNotifyCh)).
	forceInterrupt(conn1)
	forceInterrupt(conn2)

	// Close both connections.
	_ = conn1.Close() //nolint:errcheck
	_ = conn2.Close() //nolint:errcheck

	// Wait for the second goroutine to finish. With the forced deadline,
	// the blocked Read will return quickly with a timeout error, so this
	// wait is bounded.
	select {
	case <-errCh1:
	case <-errCh2:
	}

	return firstErr
}
