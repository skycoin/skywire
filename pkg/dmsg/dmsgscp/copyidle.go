// Package dmsgscp pkg/dmsg/dmsgscp/copyidle.go c1-net-dmsg
package dmsgscp

import (
	"io"
	"net"
	"time"
)

// DefaultIdleTimeout is the no-progress deadline used when a caller does not
// supply one: a transfer aborts only after this long with zero bytes moving. It
// bounds a genuine stall without capping the total duration of a healthy
// transfer of any size.
const DefaultIdleTimeout = 60 * time.Second

// copyNIdle copies exactly n bytes from src to dst, arming an IDLE (no-progress)
// deadline on conn before every chunk. The deadline is pushed forward to
// now+idle on each iteration, so a transfer that keeps moving bytes never times
// out — regardless of total size or how slow the link is — while a genuine stall
// (nothing read or written for `idle`) aborts within that window instead of
// hanging until the underlying conn happens to error, or being cut off by a
// coarse whole-transfer cap that also kills healthy long transfers.
//
// idle <= 0 disables the deadline (unbounded, the old behavior). The deadline is
// cleared on return so a following control frame (e.g. the final ack) is not
// bounded by a stale deadline.
func copyNIdle(dst io.Writer, src io.Reader, n int64, conn net.Conn, idle time.Duration) (int64, error) {
	const chunk = 64 * 1024
	buf := make([]byte, chunk)
	var done int64
	for done < n {
		if idle > 0 {
			_ = conn.SetDeadline(time.Now().Add(idle)) //nolint:errcheck // advisory idle deadline; a set failure surfaces on the next Read/Write
		}
		want := n - done
		if want > int64(len(buf)) {
			want = int64(len(buf))
		}
		nr, rerr := src.Read(buf[:want])
		if nr > 0 {
			nw, werr := dst.Write(buf[:nr])
			done += int64(nw)
			if werr != nil {
				return done, werr
			}
			if nw < nr {
				return done, io.ErrShortWrite
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return done, rerr
		}
	}
	if idle > 0 {
		_ = conn.SetDeadline(time.Time{}) //nolint:errcheck // clearing the deadline is best-effort
	}
	if done < n {
		return done, io.ErrUnexpectedEOF
	}
	return done, nil
}
