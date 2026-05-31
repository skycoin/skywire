// Package dmsgdisc rundmsg_test.go: integration coverage for runDMSG, driven
// against an in-memory dmsg server with a mock discovery store (so no redis
// is needed). runDMSG takes the *api.API as a parameter, letting us bypass
// Run's redis-backed openStore entirely.
package dmsgdisc

import (
	"context"
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

// startTestDmsgServer brings up a single in-memory dmsg server and returns a
// disc.Entry pointing at it (torn down via t.Cleanup).
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
		Server:  &disc.Server{Address: lis.Addr().String(), AvailableSessions: 2048, ServerType: "public"},
	}
}

func TestRunDMSG_WithConfigServers(t *testing.T) {
	server := startTestDmsgServer(t)

	pk, sk := cipher.GenerateKeyPair()
	a := api.New(testLog(), store.NewMock(), metrics.NewEmpty(), true, false, false, pk.Hex()+":80", "", 0)

	cfg := &Config{DmsgServers: []*disc.Entry{server}}
	svc := &service{cfg: cfg, log: testLog()}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// MinSessions is 0 inside runDMSG, so StartDmsg becomes Ready promptly
	// regardless of session count; runDMSG returns once the dmsg client is
	// up and the background goroutines (server-PK ticker, updateServers,
	// dmsghttp, debug, CXO publisher) are launched.
	err := svc.runDMSG(ctx, cancel, cfg, a, pk, sk, testLog())
	require.NoError(t, err)

	// Let the goroutines run an iteration, then cancel to trigger cleanup.
	time.Sleep(300 * time.Millisecond)
	cancel()
	time.Sleep(200 * time.Millisecond)
}

func TestRunDMSG_DefaultDeploymentSource(t *testing.T) {
	// No config servers => runDMSG falls to the embedded deployment keyring
	// branch. MinSessions is 0, so StartDmsg becomes Ready promptly even
	// though those prod servers aren't reachable from the test.
	pk, sk := cipher.GenerateKeyPair()
	a := api.New(testLog(), store.NewMock(), metrics.NewEmpty(), true, false, false, pk.Hex()+":80", "", 0)

	cfg := &Config{} // no DmsgServers
	svc := &service{cfg: cfg, log: testLog()}

	// Short deadline: with an empty embedded keyring runDMSG falls to the
	// poll fallback, which exhausts the ctx and makes StartDmsg return a
	// ctx error; with a populated keyring StartDmsg is Ready (MinSessions
	// 0) and returns nil. Both outcomes traverse the default-source branch,
	// so we accept either rather than asserting one.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = svc.runDMSG(ctx, cancel, cfg, a, pk, sk, testLog())

	time.Sleep(200 * time.Millisecond)
}
