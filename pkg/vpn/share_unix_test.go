//go:build unix && !tinygo

package vpn

import (
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// unixPair is a connected pair of unix stream sockets.
func unixPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	conns := make([]*net.UnixConn, 2)
	for i, fd := range fds {
		f := os.NewFile(uintptr(fd), "pair")
		c, err := net.FileConn(f)
		require.NoError(t, f.Close())
		require.NoError(t, err)
		conns[i] = c.(*net.UnixConn)
	}
	return conns[0], conns[1]
}

// TestReceiveShared hands a live TCP connection over the way the app does —
// one byte plus its descriptor — and checks the received conn is that
// connection, still open after the sender dropped every copy of its own.
func TestReceiveShared(t *testing.T) {
	app, core := unixPair(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close() //nolint:errcheck
	device, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	defer device.Close() //nolint:errcheck
	accepted, err := l.Accept()
	require.NoError(t, err)

	got := make(chan net.Conn, 1)
	done := make(chan error, 1)
	go func() { done <- receiveShared(core, func(c net.Conn) bool { got <- c; return true }) }()

	f, err := accepted.(*net.TCPConn).File()
	require.NoError(t, err)
	_, _, err = app.WriteMsgUnix([]byte{1}, unix.UnixRights(int(f.Fd())), nil)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, accepted.Close())

	var shared net.Conn
	select {
	case shared = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no connection received")
	}
	defer shared.Close() //nolint:errcheck

	_, err = device.Write([]byte("ping"))
	require.NoError(t, err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(shared, buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf))

	_, err = shared.Write([]byte("pong"))
	require.NoError(t, err)
	_, err = io.ReadFull(device, buf)
	require.NoError(t, err)
	require.Equal(t, "pong", string(buf))

	require.NoError(t, app.Close())
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("receiveShared did not return when the app closed the channel")
	}
}

func TestConnQueueClose(t *testing.T) {
	q := newConnQueue()
	require.NoError(t, q.Close())
	_, err := q.Accept()
	require.ErrorIs(t, err, net.ErrClosed)

	a, b := net.Pipe()
	defer b.Close() //nolint:errcheck
	require.False(t, q.push(a))
	_, err = a.Write([]byte{1})
	require.Error(t, err, "a conn pushed after close must be closed")
}
