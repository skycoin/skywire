// Package appnet pkg/app/appnet/skywire_networker_more_test.go: unit tests for
// the SkywireNetworker listener/serve plumbing and helpers that don't require a
// live router — Listen/ListenContext, the skywireListener accept queue,
// serve/close dispatch, readWithTimeout, and SetAppDirectMux's nil path.
package appnet

import (
	"bytes"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// serveConn is a net.Conn whose LocalAddr and Close are configurable, for
// driving SkywireNetworker.serve directly.
type serveConn struct {
	net.Conn
	local   net.Addr
	onClose func()
}

func (c serveConn) LocalAddr() net.Addr { return c.local }
func (c serveConn) Close() error {
	if c.onClose != nil {
		c.onClose()
	}
	return nil
}

func testNetworker(t *testing.T) *SkywireNetworker {
	t.Helper()
	r, ok := NewSkywireNetworker(logging.MustGetLogger("sn-test"), nil).(*SkywireNetworker)
	require.True(t, ok)
	return r
}

func TestNewSkywireNetworker(t *testing.T) {
	r := testNetworker(t)
	require.NotNil(t, r.porter)
	require.Nil(t, r.appDirectMux)
}

func TestSkywireNetworker_ListenAndPortBound(t *testing.T) {
	r := testNetworker(t)
	// Pretend the serve loop is already running so ListenContext doesn't
	// spawn serveRouteGroup against the nil router.
	atomic.StoreInt32(&r.isServing, 1)

	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 9123}

	lis, err := r.Listen(addr)
	require.NoError(t, err)
	require.NotNil(t, lis)
	require.Equal(t, addr, lis.Addr())

	// Re-binding the same port fails.
	_, err = r.Listen(addr)
	require.ErrorIs(t, err, ErrPortAlreadyBound)

	require.NoError(t, lis.Close())
}

func TestSkywireListener_AcceptPassthrough(t *testing.T) {
	lis := &skywireListener{
		addr:     Addr{Net: TypeSkynet, Port: 1},
		connsCh:  make(chan net.Conn, 1),
		freePort: func() {},
	}

	// A pre-wrapped SkywireConn (direct-dial path) passes straight through.
	sc := &SkywireConn{}
	lis.putConn(sc)
	got, err := lis.Accept()
	require.NoError(t, err)
	require.Equal(t, sc, got)
}

func TestSkywireListener_AcceptClosed(t *testing.T) {
	lis := &skywireListener{
		connsCh:  make(chan net.Conn),
		freePort: func() {},
	}
	require.NoError(t, lis.Close())

	_, err := lis.Accept()
	require.ErrorIs(t, err, ErrClosedConn)

	// Close is once-guarded — second call is a no-op.
	require.NoError(t, lis.Close())
}

func TestSkywireNetworker_Serve(t *testing.T) {
	r := testNetworker(t)

	t.Run("wrong addr type → closed", func(t *testing.T) {
		closed := make(chan struct{}, 1)
		conn := serveConn{local: &net.TCPAddr{}, onClose: func() { closed <- struct{}{} }}
		r.serve(conn)
		select {
		case <-closed:
		default:
			t.Fatal("conn should have been closed")
		}
	})

	t.Run("no listener for port → closed", func(t *testing.T) {
		closed := make(chan struct{}, 1)
		conn := serveConn{local: routing.Addr{Port: 4321}, onClose: func() { closed <- struct{}{} }}
		r.serve(conn)
		select {
		case <-closed:
		default:
			t.Fatal("conn should have been closed")
		}
	})

	t.Run("listener present → putConn", func(t *testing.T) {
		const port = 9777
		lis := &skywireListener{
			addr:    Addr{Net: TypeSkynet, Port: port},
			connsCh: make(chan net.Conn, 1),
		}
		ok, free := r.porter.Reserve(uint16(port), lis)
		require.True(t, ok)
		defer free()

		conn := serveConn{local: routing.Addr{Port: port}}
		r.serve(conn)

		select {
		case got := <-lis.connsCh:
			require.NotNil(t, got)
		case <-time.After(time.Second):
			t.Fatal("conn was not handed to the listener")
		}
	})
}

func TestSkywireNetworker_SetAppDirectMux_Nil(t *testing.T) {
	r := testNetworker(t)
	r.SetAppDirectMux(nil) // nil mux is a no-op; must not start the accept loop
	require.Nil(t, r.appDirectMux)
	require.EqualValues(t, 0, atomic.LoadInt32(&r.appDirectAccepting))
}

func TestReadWithTimeout(t *testing.T) {
	t.Run("reads full buffer", func(t *testing.T) {
		buf := make([]byte, 5)
		require.NoError(t, readWithTimeout(bytes.NewReader([]byte("hello")), buf, time.Second))
		require.Equal(t, "hello", string(buf))
	})

	t.Run("times out when no data arrives", func(t *testing.T) {
		pr, pw := io.Pipe()
		defer func() { _ = pw.Close() }()
		err := readWithTimeout(pr, make([]byte, 4), 50*time.Millisecond)
		require.Error(t, err)
		require.Contains(t, err.Error(), "timed out")
	})
}
