// Package dmsgclient start_integration_test.go: integration tests for the
// direct-client Start* paths, driven against a real (localhost) dmsg server.
// These cover the connect-and-Ready paths that the pure unit tests can't
// reach. The HTTP-discovery paths (StartDmsg / StartDmsgWithSyntheticDiscovery)
// still need a live dmsg-discovery HTTP service and are left to nil-logger
// coverage in the unit test file.
package dmsgclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/disc/metrics"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/api"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// startTestDmsgServer brings up a single in-memory dmsg server on a localhost
// listener and returns a disc.Entry pointing at it. The server is torn down
// via t.Cleanup.
func startTestDmsgServer(t *testing.T) *disc.Entry {
	t.Helper()

	srvPK, srvSK := cipher.GenerateKeyPair()
	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)

	dc := disc.NewMock(0)
	conf := &dmsg.ServerConfig{MaxSessions: 10, UpdateInterval: dmsg.DefaultUpdateInterval}
	srv := dmsg.NewServer(srvPK, srvSK, dc, conf, nil)

	go func() { _ = srv.Serve(lis, "") }()
	t.Cleanup(func() { _ = srv.Close() })

	select {
	case <-srv.Ready():
	case <-time.After(20 * time.Second):
		t.Fatal("dmsg test server did not become ready")
	}

	return &disc.Entry{
		Version: "0.0.1",
		Static:  srvPK,
		Server:  &disc.Server{Address: lis.Addr().String(), AvailableSessions: 2048},
	}
}

func TestStartDmsgDirectWithServers_Connects(t *testing.T) {
	server := startTestDmsgServer(t)
	pk, sk := cipher.GenerateKeyPair()
	dest, _ := cipher.GenerateKeyPair()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// dmsgDiscAddr "" skips the over-DMSG discovery /health validation, so
	// this exercises the build-direct-client -> connect -> Ready path.
	dmsgC, stop, err := StartDmsgDirectWithServers(ctx, testLog(), pk, sk, "", []*disc.Entry{server}, 1, dest.Hex())
	require.NoError(t, err)
	require.NotNil(t, dmsgC)
	require.NotNil(t, stop)
	require.Equal(t, pk, dmsgC.LocalPK())
	stop()
}

// startTestDiscovery runs the real dmsg-discovery HTTP API backed by an
// in-memory store and returns its base URL.
func startTestDiscovery(t *testing.T) string {
	t.Helper()
	db, err := store.NewStore(context.Background(), "mock", nil, testLog())
	require.NoError(t, err)
	a := api.New(nil, db, metrics.NewEmpty(), true, false, false, "", "", 0)
	ts := httptest.NewServer(a)
	t.Cleanup(ts.Close)
	return ts.URL
}

// startDmsgServerInDiscovery brings up a dmsg server that registers itself in
// the given HTTP discovery, so clients querying that discovery can find it.
func startDmsgServerInDiscovery(t *testing.T, discURL string) {
	t.Helper()
	srvPK, srvSK := cipher.GenerateKeyPair()
	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)

	dc := disc.NewHTTP(discURL, &http.Client{}, testLog())
	conf := &dmsg.ServerConfig{MaxSessions: 10, UpdateInterval: dmsg.DefaultUpdateInterval}
	srv := dmsg.NewServer(srvPK, srvSK, dc, conf, nil)
	go func() { _ = srv.Serve(lis, "") }()
	t.Cleanup(func() { _ = srv.Close() })

	select {
	case <-srv.Ready():
	case <-time.After(20 * time.Second):
		t.Fatal("dmsg server did not become ready")
	}
}

func TestStartDmsg_Connects(t *testing.T) {
	discURL := startTestDiscovery(t)
	startDmsgServerInDiscovery(t, discURL)

	pk, sk := cipher.GenerateKeyPair()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dmsgC, stop, err := StartDmsg(ctx, testLog(), pk, sk, &http.Client{}, discURL, 1)
	require.NoError(t, err)
	require.NotNil(t, dmsgC)
	require.Equal(t, pk, dmsgC.LocalPK())
	stop()
}

func TestStartDmsgWithSyntheticDiscovery_Connects(t *testing.T) {
	discURL := startTestDiscovery(t)
	startDmsgServerInDiscovery(t, discURL)

	pk, sk := cipher.GenerateKeyPair()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dmsgC, stop, err := StartDmsgWithSyntheticDiscovery(ctx, testLog(), pk, sk, &http.Client{}, discURL, 1)
	require.NoError(t, err)
	require.NotNil(t, dmsgC)
	stop()
}

// withProdServers temporarily overrides dmsg.Prod.DmsgServers (used by the
// Prod-driven Start* helpers) and restores it on cleanup.
func withProdServers(t *testing.T, servers ...*disc.Entry) {
	t.Helper()
	orig := dmsg.Prod.DmsgServers
	entries := make([]disc.Entry, len(servers))
	for i, s := range servers {
		entries[i] = *s
	}
	dmsg.Prod.DmsgServers = entries
	t.Cleanup(func() { dmsg.Prod.DmsgServers = orig })
}

func TestStartDmsgDirect_Connects(t *testing.T) {
	server := startTestDmsgServer(t)
	withProdServers(t, server)

	pk, sk := cipher.GenerateKeyPair()
	dest, _ := cipher.GenerateKeyPair()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dmsgC, stop, err := StartDmsgDirect(ctx, testLog(), pk, sk, "", 1, dest.Hex())
	require.NoError(t, err)
	require.NotNil(t, dmsgC)
	stop()
}

func TestStartDmsgWithDirectClient_Connects(t *testing.T) {
	server := startTestDmsgServer(t)
	withProdServers(t, server)

	pk, sk := cipher.GenerateKeyPair()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dmsgC, stop, err := StartDmsgWithDirectClient(ctx, testLog(), pk, sk, 1)
	require.NoError(t, err)
	require.NotNil(t, dmsgC)
	require.Equal(t, pk, dmsgC.LocalPK())
	stop()
}

func TestInitDmsgWithFlags_DirectMode(t *testing.T) {
	server := startTestDmsgServer(t)
	withProdServers(t, server)

	origDC, origSrv := UseDC, DmsgServerAddr
	defer func() { UseDC, DmsgServerAddr = origDC, origSrv }()
	UseDC = true
	DmsgServerAddr = ""

	pk, sk := cipher.GenerateKeyPair()
	dest, _ := cipher.GenerateKeyPair()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// UseDC -> StartDmsgDirect -> StartDmsgDirectWithServers against the test
	// server. destination carries a valid dmsg address (pk:port).
	dmsgC, stop, err := InitDmsgWithFlags(ctx, testLog(), pk, sk, nil, dest.Hex()+":80")
	require.NoError(t, err)
	require.NotNil(t, dmsgC)
	stop()
}

func TestInitDmsgWithFlags_ServerAddrFlag(t *testing.T) {
	server := startTestDmsgServer(t)

	origSrv := DmsgServerAddr
	defer func() { DmsgServerAddr = origSrv }()
	// --srv pk@addr routes through ParseServerAddr -> StartDmsgDirectWithServers.
	DmsgServerAddr = server.Static.Hex() + "@" + server.Server.Address

	pk, sk := cipher.GenerateKeyPair()
	dest, _ := cipher.GenerateKeyPair()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dmsgC, stop, err := InitDmsgWithFlags(ctx, testLog(), pk, sk, nil, dest.Hex()+":80")
	require.NoError(t, err)
	require.NotNil(t, dmsgC)
	stop()
}
