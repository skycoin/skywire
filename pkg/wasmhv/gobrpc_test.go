package wasmhv

import (
	"errors"
	"net"
	"net/rpc" // test-only: the SERVER side, to prove gobRPCClient is wire-compatible
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type echoSvc struct{}

func (echoSvc) Echo(in *string, out *string) error { *out = "echo:" + *in; return nil }
func (echoSvc) Fail(in *string, out *string) error { return errors.New("boom") }

// TestGobRPCClientWireCompat proves the net/rpc-free gobRPCClient speaks the
// exact net/rpc wire protocol: it drives a real net/rpc server over a pipe,
// including the error path (whose empty body must be discarded so the stream
// stays aligned for the next call).
func TestGobRPCClientWireCompat(t *testing.T) {
	srv := rpc.NewServer()
	require.NoError(t, srv.RegisterName("Svc", echoSvc{}))

	a, b := net.Pipe()
	go srv.ServeConn(a)
	defer a.Close() //nolint:errcheck

	client := newGobRPCClient(b)
	defer client.Close() //nolint:errcheck

	done := make(chan struct{})
	go func() {
		defer close(done)
		var out string
		in := "hi"

		require.NoError(t, client.Call("Svc.Echo", &in, &out))
		require.Equal(t, "echo:hi", out)

		// Error path: the server's empty error body must be discarded.
		require.EqualError(t, client.Call("Svc.Fail", &in, &out), "boom")

		// The stream is still aligned — a follow-up call works.
		out = ""
		require.NoError(t, client.Call("Svc.Echo", &in, &out))
		require.Equal(t, "echo:hi", out)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out — gob stream likely misaligned")
	}
}
