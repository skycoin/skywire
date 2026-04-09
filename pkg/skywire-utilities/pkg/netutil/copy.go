// Package netutil pkg/netutil/copy.go
package netutil

import (
	"io"
)

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

	// Close both connections to signal the other direction to stop.
	_ = conn1.Close() //nolint:errcheck
	_ = conn2.Close() //nolint:errcheck

	// Don't wait for the second goroutine — yamux Stream.Close() does
	// not reliably unblock a concurrent Read(), so the goroutine may
	// be stuck indefinitely. The buffered errCh allows it to complete
	// asynchronously without leaking (the channel and goroutine will
	// be GC'd once the goroutine eventually returns).
	return firstErr
}
