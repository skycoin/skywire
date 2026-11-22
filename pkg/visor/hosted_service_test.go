//go:build !mobile

package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skynet"
	"github.com/skycoin/skywire/pkg/skynetweb"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// callerConn is the host's end of a skynet stream from origin.
type callerConn struct {
	net.Conn
	origin cipher.PubKey
}

func (c callerConn) RemotePK() cipher.PubKey { return c.origin }

// hostingVisor is a visor that hosts one service listening on port 80.
func hostingVisor(t *testing.T) (*Visor, cipher.PubKey, *dmsg.Listener) {
	t.Helper()
	_, sk := cipher.GenerateKeyPair()
	common, err := visorconfig.NewCommon(nil, filepath.Join(t.TempDir(), "skywire-config.json"), &sk)
	require.NoError(t, err)
	v := &Visor{conf: &visorconfig.V1{Common: common}}

	svcPK, svcSK := cipher.GenerateKeyPair()
	svc := dmsg.NewClient(svcPK, svcSK, disc.NewMock(0), nil)
	t.Cleanup(func() { _ = svc.Close() }) //nolint:errcheck
	lis, err := svc.Listen(80)
	require.NoError(t, err)
	raw := json.RawMessage(fmt.Sprintf(`{"secret_key":%q}`, svcSK.Hex()))
	v.hostService(&embeddedService{ownPK: svcPK, ownRaw: raw, log: logging.MustGetLogger("hosted-test")})
	return v, svcPK, lis
}

// dialHost opens a skynet stream from origin to v's forwarding server.
func dialHost(t *testing.T, v *Visor, origin cipher.PubKey) net.Conn {
	t.Helper()
	caller, host := net.Pipe()
	t.Cleanup(func() { _ = caller.Close() }) //nolint:errcheck
	go handleServerConn(logging.MustGetLogger("hosted-test"), callerConn{Conn: host, origin: origin}, v)
	return caller
}

// TestServiceOverSkynetHandshake: the client handshake reaches a hosted
// service through its host, the service sees the caller's key, and the
// host's proof names it.
func TestServiceOverSkynetHandshake(t *testing.T) {
	v, svcPK, lis := hostingVisor(t)
	origin, _ := cipher.GenerateKeyPair()

	caller := dialHost(t, v, origin)
	require.NoError(t, skynetweb.PerformServiceHandshake(caller, 80, svcPK, v.conf.PK))
	conn, err := lis.Accept()
	require.NoError(t, err)
	require.Equal(t, origin, conn.RemoteAddr().(dmsg.Addr).PK)
	require.NotZero(t, conn.RemoteAddr().(dmsg.Addr).Port, "each handed stream gets its own port")
	go func() { _, _ = caller.Write([]byte("GET")) }() //nolint:errcheck
	buf := make([]byte, 3)
	_, err = conn.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "GET", string(buf))

	// The same proof does not pass for a visor it does not name.
	impostor, _ := cipher.GenerateKeyPair()
	require.Error(t, skynetweb.PerformServiceHandshake(dialHost(t, v, origin), 80, svcPK, impostor))

	// Refusals.
	other, _ := cipher.GenerateKeyPair()
	require.Error(t, skynetweb.PerformServiceHandshake(dialHost(t, v, origin), 80, other, v.conf.PK), "a key not hosted here")
	require.Error(t, skynetweb.PerformServiceHandshake(dialHost(t, v, origin), 81, svcPK, v.conf.PK), "a port it does not listen on")

	v.unhostService(svcPK)
	require.Error(t, skynetweb.PerformServiceHandshake(dialHost(t, v, origin), 80, svcPK, v.conf.PK), "a stopped service")
}

// TestServiceHandshakeRejectsUnprovenHost: a visor that echoes the service
// key but cannot sign for it is refused, as is an old host that ignores it.
func TestServiceHandshakeRejectsUnprovenHost(t *testing.T) {
	svc, _ := cipher.GenerateKeyPair()
	host, _ := cipher.GenerateKeyPair()
	for name, reply := range map[string]skynet.ServerReply{
		"no proof":  {PK: svc.String()},
		"old host":  {},
		"bad proof": {PK: svc.String(), Sig: cipher.Sig{}.Hex()},
	} {
		caller, srv := net.Pipe()
		go func() {
			_, _ = srv.Write([]byte{0}) //nolint:errcheck
			buf := make([]byte, 1024)
			_, _ = srv.Read(buf)        //nolint:errcheck
			b, _ := json.Marshal(reply) //nolint:errcheck
			_, _ = srv.Write(b)         //nolint:errcheck
		}()
		require.Error(t, skynetweb.PerformServiceHandshake(caller, 80, svc, host), name)
		_ = caller.Close() //nolint:errcheck
		_ = srv.Close()    //nolint:errcheck
	}
}

// TestServiceHostLookup: a service's host comes from the local link TPD
// holds, is cached, and a failed dial backs skynet off for that service.
// The lookup's own dials are marked to skip skynet, or finding TPD's host
// would ask TPD's host.
func TestServiceHostLookup(t *testing.T) {
	dc := transport.NewDiscoveryMock()
	tm, err := transport.NewManager(nil, nil, nil, &transport.ManagerConfig{DiscoveryClient: dc}, network.ClientFactory{})
	require.NoError(t, err)
	_, sk := cipher.GenerateKeyPair()
	common, err := visorconfig.NewCommon(nil, filepath.Join(t.TempDir(), "skywire-config.json"), &sk)
	require.NoError(t, err)
	v := &Visor{conf: &visorconfig.V1{Common: common}, tpM: tm}

	svc, _ := cipher.GenerateKeyPair()
	host, _ := cipher.GenerateKeyPair()
	ctx := context.Background()
	_, err = v.serviceHost(ctx, svc)
	require.ErrorIs(t, err, errNoServiceHost)

	e := transport.MakeEntry(host, svc, tptypes.LOCAL, transport.LabelAutomatic)
	require.NoError(t, dc.RegisterTransportsV3(ctx, "", &e))
	_, err = v.serviceHost(ctx, svc)
	require.ErrorIs(t, err, errNoServiceHost, "no host is remembered until the cache expires")

	v.svcHosts = serviceHosts{}
	got, err := v.serviceHost(ctx, svc)
	require.NoError(t, err)
	require.Equal(t, host, got)

	v.svcHosts.failed(svc)
	_, err = v.serviceHost(ctx, svc)
	require.Error(t, err, "skynet is backed off after a failure")

	_, err = v.dialServiceOverSkynet(context.WithValue(ctx, lookupSkipsSkynet{}, true), svc, 80)
	require.ErrorIs(t, err, errNoRelay)
}

func TestFeedRedialsSpaced(t *testing.T) {
	var f feedRedials
	require.True(t, f.due("a"))
	require.False(t, f.due("a"), "one try per feed per interval")
	require.True(t, f.due("b"), "feeds are spaced separately")

	v := &Visor{}
	pk, _ := cipher.GenerateKeyPair()
	require.False(t, v.cxoRedial(context.Background(), pk, &net.TCPAddr{}), "a conn already off dmsg stays")
}
