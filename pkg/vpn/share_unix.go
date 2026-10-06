//go:build unix && !tinygo

// Package vpn pkg/vpn/share_unix.go c4-app-vpn
package vpn

import (
	"io"
	"net"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// shareMaxFDs bounds the descriptors one message may carry. The app sends one
// at a time; the room for more only keeps a misbehaving sender's extras from
// being truncated away (and leaked) instead of closed.
const shareMaxFDs = 8

// receiveShared reads handed-over connections off uc until it ends. Each one
// arrives as a descriptor in SCM_RIGHTS ancillary data riding a single byte —
// the same handoff the TUN itself takes (tun_device_android.go) — and is
// passed to push as a net.Conn of its own.
func receiveShared(uc *net.UnixConn, push func(net.Conn) bool) error {
	buf := make([]byte, 1)
	oob := make([]byte, unix.CmsgSpace(4*shareMaxFDs))
	for {
		n, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
		if err != nil {
			return err
		}
		if n == 0 && oobn == 0 {
			return io.EOF
		}
		msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			continue
		}
		for i := range msgs {
			fds, err := unix.ParseUnixRights(&msgs[i])
			if err != nil {
				continue
			}
			for _, fd := range fds {
				conn, err := fdConn(fd)
				if err != nil {
					continue
				}
				push(conn)
			}
		}
	}
}

// fdConn adopts a received socket descriptor as a net.Conn. FileConn takes a
// duplicate, so the original is closed here either way.
func fdConn(fd int) (net.Conn, error) {
	f := os.NewFile(uintptr(fd), "shared")
	defer f.Close() //nolint:errcheck
	return net.FileConn(f)
}

// connQueue is a net.Listener fed by hand: on the phone the app accepts shared
// clients itself — it knows which interface is the hotspot — and passes each
// connection over (share_android.go).
type connQueue struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newConnQueue() *connQueue {
	return &connQueue{conns: make(chan net.Conn), done: make(chan struct{})}
}

// push hands conn to Accept, closing it instead when the queue has closed.
func (q *connQueue) push(conn net.Conn) bool {
	select {
	case q.conns <- conn:
		return true
	case <-q.done:
		_ = conn.Close() //nolint:errcheck
		return false
	}
}

func (q *connQueue) Accept() (net.Conn, error) {
	select {
	case conn := <-q.conns:
		return conn, nil
	case <-q.done:
		return nil, net.ErrClosed
	}
}

func (q *connQueue) Close() error {
	q.once.Do(func() { close(q.done) })
	return nil
}

func (q *connQueue) Addr() net.Addr { return shareAddr{} }

type shareAddr struct{}

func (shareAddr) Network() string { return "share" }
func (shareAddr) String() string  { return "hotspot" }
