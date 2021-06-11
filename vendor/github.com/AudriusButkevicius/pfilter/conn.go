package pfilter

import (
	"io"
	"net"
	"sync/atomic"
	"time"
)

type filteredConn struct {
	// Alignment
	deadline atomic.Value

	source   *PacketFilter
	priority int

	recvBuffer chan packet

	filter Filter

	closed chan struct{}
}

// LocalAddr returns the local address
func (r *filteredConn) LocalAddr() net.Addr {
	return r.source.conn.LocalAddr()
}

// SetReadDeadline sets a read deadline
func (r *filteredConn) SetReadDeadline(t time.Time) error {
	r.deadline.Store(t)
	return nil
}

// SetWriteDeadline sets a write deadline
func (r *filteredConn) SetWriteDeadline(t time.Time) error {
	return r.source.conn.SetWriteDeadline(t)
}

// SetDeadline sets a read and a write deadline
func (r *filteredConn) SetDeadline(t time.Time) error {
	_ = r.SetReadDeadline(t)
	return r.SetWriteDeadline(t)
}

// WriteTo writes bytes to the given address
func (r *filteredConn) WriteTo(b []byte, addr net.Addr) (n int, err error) {
	select {
	case <-r.closed:
		return 0, errClosed
	default:
	}

	if r.filter != nil {
		r.filter.Outgoing(b, addr)
	}
	return r.source.conn.WriteTo(b, addr)
}

// ReadFrom reads from the filtered connection
func (r *filteredConn) ReadFrom(b []byte) (n int, addr net.Addr, err error) {
	select {
	case <-r.closed:
		return 0, nil, errClosed
	default:
	}

	var timeout <-chan time.Time

	if deadline, ok := r.deadline.Load().(time.Time); ok && !deadline.IsZero() {
		timer := time.NewTimer(deadline.Sub(time.Now()))
		timeout = timer.C
		defer timer.Stop()
	}

	select {
	case <-timeout:
		return 0, nil, errTimeout
	case pkt := <-r.recvBuffer:
		n := pkt.n
		err := pkt.err
		if l := len(b); l < n {
			n = l
			if err == nil {
				err = io.ErrShortBuffer
			}
		}
		copy(b, pkt.buf[:n])
		r.source.bufPool.Put(pkt.buf[:r.source.packetSize])
		if pkt.oobBuf != nil {
			r.source.bufPool.Put(pkt.oobBuf[:r.source.packetSize])
		}
		return n, pkt.addr, err
	case <-r.closed:
		return 0, nil, errClosed
	}
}

// Close closes the filtered connection, removing it's filters
func (r *filteredConn) Close() error {
	select {
	case <-r.closed:
		return errClosed
	default:
	}
	close(r.closed)
	r.source.removeConn(r)
	return nil
}
