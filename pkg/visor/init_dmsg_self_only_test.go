package visor

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// selfOnlyVisor builds a visor whose only dmsg server is its own, at an address
// nothing listens on yet, with TPD and AR mounted under the visor key.
func selfOnlyVisor(t *testing.T) (*Visor, string) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	discPK, _ := cipher.GenerateKeyPair()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	common, err := visorconfig.NewCommon(nil, "", &sk)
	require.NoError(t, err)
	conf := &visorconfig.V1{
		Common: common,
		Dmsg: &dmsgc.DmsgConfig{
			DiscoveryDmsg: "dmsg://" + discPK.Hex() + ":80",
			SessionsCount: 2,
			Servers:       []*disc.Entry{{Static: pk, Server: &disc.Server{Address: addr}}},
			Server:        &dmsgc.DmsgServerConfig{Enabled: true},
		},
		Transport: &visorconfig.Transport{
			Discovery:           "dmsg://" + pk.Hex() + ":80/tpd",
			AddressResolver:     "dmsg://" + pk.Hex() + ":80/ar",
			DiscoveryDmsg:       "dmsg://" + pk.Hex() + ":80/tpd",
			AddressResolverDmsg: "dmsg://" + pk.Hex() + ":80/ar",
		},
	}
	return &Visor{conf: conf, initLock: new(sync.RWMutex), dmsgHTTPReady: make(chan struct{})}, addr
}

// serveLate starts the visor's own dmsg server once the client's first dial
// has failed, as the folded server opens after initDmsg returns.
func serveLate(t *testing.T, v *Visor, addr string) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	srv := dmsg.NewServer(v.conf.PK, v.conf.SK, disc.NewMock(0), &dmsg.ServerConfig{MaxSessions: 10}, nil)
	lis, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	go srv.Serve(lis, addr)               //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() }) //nolint:errcheck
}

func requireReady(t *testing.T, dmsgC *dmsg.Client) {
	t.Helper()
	select {
	case <-dmsgC.Ready():
	case <-time.After(20 * time.Second):
		t.Fatal("the self-only visor's dmsg client never became ready")
	}
}

// The visor's startup order: the client's first dial races the server opening,
// then the service entries are seeded and the dmsg:// service clients are made.
func TestSelfOnlyVisorDmsgClientBecomesReady(t *testing.T) {
	v, addr := selfOnlyVisor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mLog := v.MasterLogger()
	log := logging.MustGetLogger("test")

	require.NoError(t, initDmsgHTTP(ctx, v, log))
	ebc := appevent.NewBroadcaster(log, time.Second)
	dmsgC := dmsgc.New(v.conf.PK, v.conf.SK, ebc, v.conf.Dmsg, &http.Client{}, v.dClient, false, nil, mLog)
	go dmsgC.Serve(ctx)
	t.Cleanup(func() { _ = dmsgC.Close() }) //nolint:errcheck

	require.False(t, dmsgc.SkipSelfServer(v.conf.PK, v.conf.Dmsg))
	v.dmsgC, v.dmsgSelfOnly = dmsgC, true
	v.seedDmsgServiceEntries(dmsgC, log)
	v.dmsgHTTP = &http.Client{}
	close(v.dmsgHTTPReady)
	for _, u := range []string{v.conf.Transport.Discovery, v.conf.Transport.AddressResolver} {
		_, err := getHTTPClient(ctx, v, u)
		require.NoError(t, err)
	}

	srvs, err := v.dClient.AvailableServers(ctx)
	require.NoError(t, err)
	require.Len(t, srvs, 1, "registering a service on the visor key dropped its server from the direct client")

	serveLate(t, v, addr)
	requireReady(t, dmsgC)
}

// With no discovery listing the server, the seeded entry is all the client
// has. Several services on the visor key must not replace it.
func TestSelfOnlyVisorSeedKeepsOwnServer(t *testing.T) {
	v, addr := selfOnlyVisor(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dmsgC := dmsg.NewClient(v.conf.PK, v.conf.SK, disc.NewMock(0), &dmsg.Config{MinSessions: 1})
	v.dmsgSelfOnly = true
	v.seedDmsgServiceEntries(dmsgC, logging.MustGetLogger("test"))
	go dmsgC.Serve(ctx)
	t.Cleanup(func() { _ = dmsgC.Close() }) //nolint:errcheck

	serveLate(t, v, addr)
	requireReady(t, dmsgC)
}
