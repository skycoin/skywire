package wasmrpc

import (
	"io"
	"net"
	"net/rpc"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// mockVisor is a stand-in for the wasm-visor's RPC gateway.
type mockVisor struct{}

func (mockVisor) Info(arg string, reply *string) error {
	*reply = "wasm-visor: " + arg
	return nil
}

// startMockTab runs the tab end over tabConn: a net/rpc server with mockVisor,
// served per yamux stream. Returns when the session ends.
func startMockTab(t *testing.T, tabConn net.Conn) {
	t.Helper()
	srv := rpc.NewServer()
	require.NoError(t, srv.RegisterName("app-visor", mockVisor{}))
	go func() {
		_ = ServeTab(tabConn, func(stream io.ReadWriteCloser) { srv.ServeConn(stream) }) //nolint:errcheck
	}()
}

// TestBridge_EndToEnd proves the whole host bridge: a real rpc.Client dialing the
// bridge's local TCP addr reaches the mock tab's RPC over the yamux-muxed link —
// exactly the path `skywire cli --rpc <addr>` takes, with no browser involved.
func TestBridge_EndToEnd(t *testing.T) {
	hostConn, tabConn := net.Pipe() // stands in for the tab's WebSocket
	startMockTab(t, tabConn)

	b, err := NewBridge(hostConn, 0, nil) // 0 = ephemeral local port
	require.NoError(t, err)
	defer b.Close() //nolint:errcheck

	require.Contains(t, b.Addr(), "127.0.0.1:")

	cli, err := rpc.Dial("tcp", b.Addr())
	require.NoError(t, err)
	defer cli.Close() //nolint:errcheck

	var reply string
	require.NoError(t, cli.Call("app-visor.Info", "hello", &reply))
	require.Equal(t, "wasm-visor: hello", reply)
}

// TestBridge_MultipleClients confirms several concurrent cli connections each get
// their own yamux stream to the same tab (the basis for many CLI calls / tabs).
func TestBridge_MultipleClients(t *testing.T) {
	hostConn, tabConn := net.Pipe()
	startMockTab(t, tabConn)
	b, err := NewBridge(hostConn, 0, nil)
	require.NoError(t, err)
	defer b.Close() //nolint:errcheck

	for i := 0; i < 5; i++ {
		cli, err := rpc.Dial("tcp", b.Addr())
		require.NoError(t, err)
		var reply string
		require.NoError(t, cli.Call("app-visor.Info", "n", &reply))
		require.Equal(t, "wasm-visor: n", reply)
		_ = cli.Close() //nolint:errcheck
	}
}

// TestBridge_CloseStopsListener confirms Close releases the local port.
func TestBridge_CloseStopsListener(t *testing.T) {
	hostConn, tabConn := net.Pipe()
	startMockTab(t, tabConn)
	b, err := NewBridge(hostConn, 0, nil)
	require.NoError(t, err)
	addr := b.Addr()
	require.NoError(t, b.Close())

	// A dial after Close must fail (listener gone). Give the OS a moment.
	time.Sleep(20 * time.Millisecond)
	_, err = net.DialTimeout("tcp", addr, 100*time.Millisecond)
	require.Error(t, err)
}
