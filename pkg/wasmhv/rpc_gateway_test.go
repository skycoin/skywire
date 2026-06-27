package wasmhv

import (
	"io"
	"net"
	"net/rpc"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/wasmrpc"
)

// (fakeSelf is defined in self_test.go.)

// TestRPCGateway_OverBridge drives the gateway exactly as `skywire cli` would: a
// real rpc.Client → wasmrpc.Bridge → yamux → ServeTab → the gateway's rpc.Server.
// No browser; the only thing stubbed is the SelfProvider.
func TestRPCGateway_OverBridge(t *testing.T) {
	var pk cipher.PubKey
	require.NoError(t, pk.Set("0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13"))

	srv, err := NewRPCServer(fakeSelf{pk: pk}, nil)
	require.NoError(t, err)

	hostConn, tabConn := net.Pipe()
	go func() {
		_ = wasmrpc.ServeTab(tabConn, func(s io.ReadWriteCloser) { srv.ServeConn(s) }) //nolint:errcheck
	}()
	b, err := wasmrpc.NewBridge(hostConn, 0, nil)
	require.NoError(t, err)
	defer b.Close() //nolint:errcheck

	cli, err := rpc.Dial("tcp", b.Addr())
	require.NoError(t, err)
	defer cli.Close() //nolint:errcheck

	var complete bool
	require.NoError(t, cli.Call("app-visor.IsStartupComplete", &struct{}{}, &complete))
	require.True(t, complete)

	var health HealthInfo
	require.NoError(t, cli.Call("app-visor.Health", &struct{}{}, &health))
	require.Equal(t, "healthy", health.ServicesHealth)

	var overview Overview
	require.NoError(t, cli.Call("app-visor.Overview", &struct{}{}, &overview))
	require.Equal(t, pk, overview.PubKey)
}

// TestRPCGateway_NotBooted confirms the gateway errors cleanly when no visor is
// wired (rather than panicking or returning a zero value as if booted).
func TestRPCGateway_NotBooted(t *testing.T) {
	srv, err := NewRPCServer(nil, nil)
	require.NoError(t, err)
	hostConn, tabConn := net.Pipe()
	go func() {
		_ = wasmrpc.ServeTab(tabConn, func(s io.ReadWriteCloser) { srv.ServeConn(s) }) //nolint:errcheck
	}()
	b, err := wasmrpc.NewBridge(hostConn, 0, nil)
	require.NoError(t, err)
	defer b.Close() //nolint:errcheck

	cli, err := rpc.Dial("tcp", b.Addr())
	require.NoError(t, err)
	defer cli.Close() //nolint:errcheck

	var complete bool
	require.NoError(t, cli.Call("app-visor.IsStartupComplete", &struct{}{}, &complete))
	require.False(t, complete, "not booted → startup not complete")

	var s Summary
	err = cli.Call("app-visor.Summary", &struct{}{}, &s)
	require.Error(t, err, "Summary on an unbooted tab must error")
}
