// Package appserver pkg/app/appserver/rpc_ingress_local_service_test.go
package appserver

import (
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// A dial to a local service on this visor's own PK is served in-process, so it
// reaches the handler with no networker registered at all — the resolving
// proxies' path, and proof the short-circuit happens before any carrier.
func TestRPCIngressGatewayDialLocalService(t *testing.T) {
	t.Cleanup(appnet.ClearLocalServices)

	rpc := NewRPCGateway(logging.MustGetLogger("rpc_gateway_local"), &Proc{})

	pk, _ := cipher.GenerateKeyPair()
	addr := appnet.Addr{Net: appnet.TypeSkynet, PubKey: pk, Port: routing.Port(4446)}
	appnet.RegisterLocalService(pk, appnet.LocalService{Port: addr.Port, Label: "skynet_web"}, func(conn net.Conn) {
		defer conn.Close()         //nolint:errcheck
		_, _ = io.Copy(conn, conn) //nolint:errcheck
	})

	var resp DialResp
	require.NoError(t, rpc.Dial(&addr, &resp))
	// No route group was built, so the app owns no local routing port.
	require.Equal(t, routing.Port(0), resp.LocalPort)

	var wResp WriteResp
	require.NoError(t, rpc.Write(&WriteReq{ConnID: resp.ConnID, B: []byte("ping")}, &wResp))
	require.Nil(t, wResp.Err)
	require.Equal(t, 4, wResp.N)

	var rResp ReadResp
	require.NoError(t, rpc.Read(&ReadReq{ConnID: resp.ConnID, BufLen: 4}, &rResp))
	require.Nil(t, rResp.Err)
	require.Equal(t, "ping", string(rResp.B[:rResp.N]))

	require.NoError(t, rpc.CloseConn(&resp.ConnID, nil))
}

// An address that is not a local service still takes the ordinary dial path.
func TestRPCIngressGatewayDialSkipsLocalServiceForOtherAddrs(t *testing.T) {
	t.Cleanup(appnet.ClearLocalServices)

	rpc := NewRPCGateway(logging.MustGetLogger("rpc_gateway_local"), &Proc{})

	pk, _ := cipher.GenerateKeyPair()
	appnet.RegisterLocalService(pk, appnet.LocalService{Port: routing.Port(4446), Label: "skynet_web"}, func(conn net.Conn) {
		_ = conn.Close() //nolint:errcheck
	})

	// Right PK, port with no local service: no networker answers for this
	// address family, which is what an ordinary dial reports.
	var resp DialResp
	err := rpc.Dial(&appnet.Addr{Net: appnet.Type("no-such-network"), PubKey: pk, Port: routing.Port(4445)}, &resp)
	require.ErrorIs(t, err, appnet.ErrNoSuchNetworker)
}

// LocalServices answers with what the calling app's OWN visor published, and
// nothing another visor did.
func TestRPCIngressGatewayLocalServices(t *testing.T) {
	t.Cleanup(appnet.ClearLocalServices)

	pk, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	rpc := NewRPCGateway(logging.MustGetLogger("rpc_gateway_local"), &Proc{
		conf: appcommon.ProcConfig{VisorPK: pk},
	})

	mine := appnet.LocalService{Port: routing.Port(4446), Label: "skynet_web", Suffixes: []string{".skynet"}}
	appnet.RegisterLocalService(pk, mine, func(conn net.Conn) { _ = conn.Close() })                    //nolint:errcheck
	appnet.RegisterLocalService(other, appnet.LocalService{Port: routing.Port(4445), Label: "theirs"}, //nolint:errcheck
		func(conn net.Conn) { _ = conn.Close() }) //nolint:errcheck

	var resp []appnet.LocalService
	require.NoError(t, rpc.LocalServices(&struct{}{}, &resp))
	require.Equal(t, []appnet.LocalService{mine}, resp)
}
