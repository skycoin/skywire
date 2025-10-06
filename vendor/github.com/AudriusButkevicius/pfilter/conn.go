package pfilter

import (
	"io"
	"net"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/net/ipv4"
)

type filteredConn struct {
	// Alignment
	deadline atomic.Value

	source   *PacketFilter
	priority int

	recvBuffer chan messageWithError

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
	case msg := <-r.recvBuffer:
		n, _, err := copyBuffers(msg, b, nil)

		r.source.returnBuffers(msg.Message)

		return n, msg.Addr, err
	case <-r.closed:
		return 0, nil, errClosed
	}
}

func (r *filteredConn) ReadBatch(ms []ipv4.Message, flags int) (int, error) {
	if flags != 0 {
		return 0, errNotSupported
	}

	if len(ms) == 0 {
		return 0, nil
	}

	var timeout <-chan time.Time

	if deadline, ok := r.deadline.Load().(time.Time); ok && !deadline.IsZero() {
		timer := time.NewTimer(deadline.Sub(time.Now()))
		timeout = timer.C
		defer timer.Stop()
	}

	msgs := make([]messageWithError, 0, len(ms))

	defer func() {
		for _, msg := range msgs {
			r.source.returnBuffers(msg.Message)
		}
	}()

	// We must read at least one message.
	select {
	//goland:noinspection GoNilness
	case <-timeout:
		return 0, errTimeout
	case msg := <-r.recvBuffer:
		msgs = append(msgs, msg)
		if msg.Err != nil {
			return 0, msg.Err
		}
	case <-r.closed:
		return 0, errClosed
	}

	// After that, it's best effort. If there are messages, we read them.
	// If not, we break out and return what we got.
loop:
	for len(msgs) != len(ms) {
		select {
		case msg := <-r.recvBuffer:
			msgs = append(msgs, msg)
			if msg.Err != nil {
				return 0, msg.Err
			}
		case <-r.closed:
			return 0, errClosed
		default:
			break loop
		}
	}

	for i, msg := range msgs {
		if len(ms[i].Buffers) != 1 {
			return 0, errNotSupported
		}

		n, nn, err := copyBuffers(msg, ms[i].Buffers[0], ms[i].OOB)
		if err != nil {
			return 0, err
		}

		ms[i].N = n
		ms[i].NN = nn
		ms[i].Flags = msg.Flags
		ms[i].Addr = msg.Addr
	}

	return len(msgs), nil
}

func copyBuffers(msg messageWithError, buf, oobBuf []byte) (n, nn int, err error) {
	if msg.Err != nil {
		return 0, 0, msg.Err
	}

	if len(buf) < msg.N {
		return 0, 0, io.ErrShortBuffer
	}

	copy(buf, msg.Buffers[0][:msg.N])

	// Truncate, probably?
	oobn := msg.NN
	if oobl := len(oobBuf); oobl < oobn {
		oobn = oobl
	}
	if oobn > 0 {
		copy(oobBuf, msg.OOB[:oobn])
	}

	return msg.N, oobn, nil
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

func (r *filteredConn) SetReadBuffer(sz int) error {
	if srb, ok := r.source.conn.(interface{ SetReadBuffer(int) error }); ok {
		return srb.SetReadBuffer(sz)
	}
	return errNotSupported
}

func (r *filteredConn) SyscallConn() (syscall.RawConn, error) {
	if r.source.oobConn != nil {
		return r.source.oobConn.SyscallConn()
	}
	if scon, ok := r.source.conn.(interface {
		SyscallConn() (syscall.RawConn, error)
	}); ok {
		return scon.SyscallConn()
	}
	return nil, errNotSupported
}
