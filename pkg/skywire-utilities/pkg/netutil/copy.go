// Package netutil pkg/netutil/copy.go
package netutil

import (
	"io"
)

// DeadlineSetter allows setting a read deadline to unblock pending reads.
// This is needed because yamux Stream.Close() sends FIN but does NOT
// unblock a concurrent local Read() on the same stream.
type DeadlineSetter interface {
	ForceReadDeadline()
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

	// Close both connections.
	_ = conn1.Close() //nolint:errcheck
	_ = conn2.Close() //nolint:errcheck

	// Force-expire any pending read deadlines to unblock the other goroutine.
	if ds, ok := conn1.(DeadlineSetter); ok {
		ds.ForceReadDeadline()
	}
	if ds, ok := conn2.(DeadlineSetter); ok {
		ds.ForceReadDeadline()
	}

	// Wait for the other direction to finish (should return quickly now).
	<-errCh1
	<-errCh2

	return firstErr
}
