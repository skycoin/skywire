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

// A relayed stream starts on a small buffer and moves to a large one once a
// run of reads fills the small one, so request/response streams, most of a
// dmsg server's ~2,000, hold 4 KiB per direction instead of 32 KiB, and bulk
// transfers still copy in 32 KiB reads. Buffers are pooled and reused.
const (
	copyBufSmall  = 4 * 1024
	copyBufLarge  = 32 * 1024
	copyGrowAfter = 4
)

var (
	copySmallPool = sync.Pool{New: func() any { b := make([]byte, copyBufSmall); return &b }}
	copyLargePool = sync.Pool{New: func() any { b := make([]byte, copyBufLarge); return &b }}
)

// copyPooled is io.Copy with a pooled buffer that grows when the stream
// needs it. A destination's ReaderFrom or a source's WriterTo still wins,
// and then no buffer is taken at all.
func copyPooled(dst io.Writer, src io.Reader) error {
	_, err := copyAdaptive(dst, src)
	return err
}

// copyAdaptive is copyPooled, also returning the buffer size it ended on
// (0 when the copy needed none).
func copyAdaptive(dst io.Writer, src io.Reader) (int, error) {
	_, wt := src.(io.WriterTo)
	_, rf := dst.(io.ReaderFrom)
	if wt || rf {
		_, err := io.Copy(dst, src)
		return 0, err
	}
	pool := &copySmallPool
	bp := pool.Get().(*[]byte)
	defer func() { pool.Put(bp) }()
	full := 0
	for {
		buf := *bp
		n, rerr := src.Read(buf)
		if n > 0 {
			nw, werr := dst.Write(buf[:n])
			if werr == nil && nw != n {
				werr = io.ErrShortWrite
			}
			if werr != nil {
				return len(buf), werr
			}
			if n < len(buf) {
				full = 0
			} else if full++; pool == &copySmallPool && full >= copyGrowAfter {
				copySmallPool.Put(bp)
				pool = &copyLargePool
				bp = pool.Get().(*[]byte)
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return len(*bp), nil
			}
			return len(*bp), rerr
		}
	}
}
