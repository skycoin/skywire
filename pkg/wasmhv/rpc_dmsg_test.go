//go:build !js

// Package wasmhv_test (rpc_dmsg_test.go): a real dmsg round-trip proving the
// wasm-visor's RPC gateway is reachable over dmsg exactly as `skywire cli ...
// --rpc dmsg://<pk>` reaches it — the serving path ServeRPCOverDmsg wires into the
// browser leaf (cmd/wasm-visor/peerserve_js.go). Host-only (!js): dmsgtest and
// pkg/visor don't build for wasm, which is why the mirror types + ServeRPCOverDmsg
// (build-tag-free) exist and are exercised here on the native side.
package wasmhv_test

import (
	"context"
	"net/rpc"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/wasmhv"
)

// stubSelf is a minimal SelfProvider for the gateway under test.
type stubSelf struct{ pk cipher.PubKey }

func (s stubSelf) SelfPK() cipher.PubKey                     { return s.pk }
func (stubSelf) SelfOverview() wasmhv.Overview               { return wasmhv.Overview{} }
func (stubSelf) SelfSummary() wasmhv.Summary                 { return wasmhv.Summary{Online: true, MinHops: 2} }
func (stubSelf) SelfTransports() []*wasmhv.TransportSummary  { return []*wasmhv.TransportSummary{} }
func (stubSelf) SelfRoutes() []byte                          { return nil }
func (stubSelf) SelfNetworkView() []byte                     { return nil }
func (stubSelf) SelfNetworkTransports(_ int) []byte          { return nil }
func (stubSelf) SelfServiceHealth() []byte                   { return nil }
func (stubSelf) SelfDmsgSessions() []byte                    { return nil }
func (stubSelf) SelfDmsgConnectAll() []byte                  { return nil }
func (stubSelf) SelfRouterSettings() []byte                  { return nil }
func (stubSelf) SelfRuntimeConfig() []byte                   { return nil }
func (stubSelf) SelfSetRuntimeConfig(_ []byte) (int, []byte) { return 200, nil }
func (stubSelf) SelfRuntimeLogs(_ int64) []byte              { return nil }
func (stubSelf) SelfApps() []byte                            { return nil }
func (stubSelf) StartApp(_ string) error                     { return nil }
func (stubSelf) StopApp(_ string) error                      { return nil }
func (stubSelf) SetAutoStart(_ string, _ bool) error         { return nil }

// TestServeRPCOverDmsg_StateSnapshot stands up two dmsg clients, serves the RPC
// gateway over dmsg on DmsgVisorRPCPort from one, dials it from the other exactly
// as the CLI's `--rpc dmsg://<pk>` does (net/rpc over the dmsg stream), and
// confirms `visor state` (app-visor.StateSnapshot) returns the partial snapshot —
// decoded into the REAL pkg/visor.StateSnapshot, proving the wire matches.
func TestServeRPCOverDmsg_StateSnapshot(t *testing.T) {
	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	conf := dmsg.Config{MinSessions: 1}
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 1, 2, &conf))
	t.Cleanup(env.Shutdown)

	server := env.AllClients()[0]
	client := env.AllClients()[1]

	srv, err := wasmhv.NewRPCServer(stubSelf{pk: server.LocalPK()}, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	// Only the client's PK is authorized — mirrors the leaf's whitelist gate.
	authorized := map[cipher.PubKey]bool{client.LocalPK(): true}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- wasmhv.ServeRPCOverDmsg(ctx, server, srv, authorized, logging.MustGetLogger("dmsg_visor_rpc_test"))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-serveErr:
			require.NoError(t, err) // a clean listener close returns nil
		case <-time.After(2 * time.Second):
			t.Error("ServeRPCOverDmsg did not return after ctx cancel")
		}
	})

	// Let the listener come up on DmsgVisorRPCPort.
	time.Sleep(500 * time.Millisecond)

	stream, err := client.DialStream(ctx, dmsg.Addr{PK: server.LocalPK(), Port: skyenv.DmsgVisorRPCPort})
	require.NoError(t, err)
	defer stream.Close() //nolint:errcheck

	rc := rpc.NewClient(stream)
	defer rc.Close() //nolint:errcheck

	var snap visor.StateSnapshot
	require.NoError(t, rc.Call("app-visor.StateSnapshot", &struct{}{}, &snap))
	require.NotNil(t, snap.Summary)
	require.True(t, snap.Summary.Online)
	require.NotNil(t, snap.Health)
	require.Equal(t, "healthy", snap.Health.ServicesHealth)
	require.False(t, snap.At.IsZero())

	// A Summary call (an existing read method) over the same dmsg path, too.
	var sum visor.Summary
	require.NoError(t, rc.Call("app-visor.Summary", &struct{}{}, &sum))
	require.True(t, sum.Online)
}
