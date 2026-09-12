// Package visor pkg/visor/api_state_roles_dmsgsrv_test.go c3-vis-api
package visor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// TestDmsgServerSessionsReportsConnectedClients covers the question the
// discovery cannot answer: which clients is this server actually holding, and
// how many streams is each running.
//
// A discovery entry records what a CLIENT claims about the servers it
// delegated to — self-reported, refresh-interval stale, and with no stream
// counts. A co-resident visor has the server's own view, and this is how it
// reports it.
func TestDmsgServerSessionsReportsConnectedClients(t *testing.T) {
	srvPK, srvSK := cipher.GenerateKeyPair()
	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)

	dc := dmsgdisc.NewMock(0)
	srv := dmsg.NewServer(srvPK, srvSK, dc, &dmsg.ServerConfig{
		MaxSessions:    10,
		UpdateInterval: dmsg.DefaultUpdateInterval,
	}, nil)
	go func() { _ = srv.Serve(lis, "") }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })  //nolint:errcheck

	select {
	case <-srv.Ready():
	case <-time.After(20 * time.Second):
		t.Fatal("dmsg test server did not become ready")
	}

	// An idle server holds nothing, and must say so with an empty list rather
	// than a nil-vs-empty distinction a caller has to guess at.
	total, peers, clients := dmsgServerSessions(srv)
	require.Zero(t, total)
	require.Zero(t, peers)
	require.Empty(t, clients)

	// Two clients on the same server: the count must be the server's, not a
	// per-client guess.
	var pks []cipher.PubKey
	for i := 0; i < 2; i++ {
		cPK, cSK := cipher.GenerateKeyPair()
		pks = append(pks, cPK)
		c := dmsg.NewClient(cPK, cSK, dc, &dmsg.Config{MinSessions: 1})
		go c.Serve(t.Context())             //nolint:errcheck
		t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
		select {
		case <-c.Ready():
		case <-time.After(20 * time.Second):
			t.Fatalf("client %d never became ready", i)
		}
	}

	require.Eventually(t, func() bool {
		total, _, _ = dmsgServerSessions(srv)
		return total == 2
	}, 20*time.Second, 100*time.Millisecond, "server must report both connected clients")

	total, peers, clients = dmsgServerSessions(srv)
	require.Equal(t, 2, total)
	require.Len(t, clients, 2)
	require.Zero(t, peers, "plain clients are not peer servers")

	seen := map[cipher.PubKey]bool{}
	for _, c := range clients {
		require.False(t, c.Peer, "client %s wrongly classified as a peer server", c.PK)
		require.GreaterOrEqual(t, c.Streams, 0)
		seen[c.PK] = true
	}
	for _, pk := range pks {
		require.True(t, seen[pk], "connected client %s missing from the report", pk)
	}

	// Stable order, so a caller diffing two snapshots does not see map
	// iteration masquerading as churn.
	require.True(t, clients[0].PK.String() < clients[1].PK.String(), "clients must be ordered by key")
}
