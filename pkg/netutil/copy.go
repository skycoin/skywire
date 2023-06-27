// Package netutil pkg/netutil/copy.go
package netutil

import (
	"io"
)

// CopyReadWriteCloser copies reads and writes between two connections.
// It returns when a connection returns an error.
func CopyReadWriteCloser(conn1, conn2 io.ReadWriteCloser) error {
	errCh1 := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn2, conn1)
		errCh1 <- err
		close(errCh1)
	}()

	errCh2 := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn1, conn2)
		errCh2 <- err
		close(errCh2)
	}()

	select {
	case err := <-errCh1:
		_ = conn1.Close() //nolint:errcheck
		_ = conn2.Close() //nolint:errcheck
		<-errCh2
		return err
	case err := <-errCh2:
		_ = conn2.Close() //nolint:errcheck
		_ = conn1.Close() //nolint:errcheck
		<-errCh1
		return err
	}
}
