// Package netutil pkg/netutil/copy.go c0-com-util
package netutil

import (
	"io"
	"sync"
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
	done := make(chan error, 2)
	go func() {
		done <- copyPooled(conn2, conn1)
	}()
	go func() {
		done <- copyPooled(conn1, conn2)
	}()

	// Wait for one direction to finish.
	firstErr := <-done

	// Force interrupt both directions by setting past deadlines. This is
	// necessary because yamux.Stream.Close() does not unblock a concurrent
	// Read(), but SetReadDeadline(past) does (via asyncNotify(recvNotifyCh)).
	forceInterrupt(conn1)
	forceInterrupt(conn2)

	// Close both connections.
	_ = conn1.Close() //nolint:errcheck
	_ = conn2.Close() //nolint:errcheck

	// Wait briefly for the second goroutine to finish. With the forced
	// deadline, the blocked Read should return quickly. Fall back to a
	// hard timeout so we never block the caller indefinitely; any stragglers
	// will clean themselves up asynchronously (the buffered channel allows
	// the late send to succeed without blocking).
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}

	return firstErr
}

// copyBufSize is the per-direction relay buffer. io.Copy would allocate a
// fresh 32 KiB for every stream direction; a dmsg server bridging ~2,000
// streams held ~120 MB of those (heap profile on the TPD host, 2026-09-10),
// and re-allocated them on every stream open. Pooled, the buffers are reused
// across streams and released when idle.
const copyBufSize = 32 * 1024

var copyBufPool = sync.Pool{New: func() any { b := make([]byte, copyBufSize); return &b }}

// copyPooled is io.Copy with a pooled buffer. io.CopyBuffer still prefers the
// destination's ReaderFrom / the source's WriterTo when either exists.
func copyPooled(dst io.Writer, src io.Reader) error {
	bp := copyBufPool.Get().(*[]byte)
	_, err := io.CopyBuffer(dst, src, *bp)
	copyBufPool.Put(bp)
	return err
}
