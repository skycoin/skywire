package cxoaggregator

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// TestRedialReplacesLiveConn: AnnounceTo keeps a live conn unless Redial
// asks for a new one, and the feed is served on the new conn.
func TestRedialReplacesLiveConn(t *testing.T) {
	const timeout = 2 * time.Minute
	env := dmsgtest.NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	tpdPK, tpdSK := cipher.GenerateKeyPair()
	visorPK, visorSK := cipher.GenerateKeyPair()
	tpdClient, err := env.NewClientWithKeys(tpdPK, tpdSK, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	visorClient, err := env.NewClientWithKeys(visorPK, visorSK, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	agg, err := New(tpdClient, tpdSK, &recordingSink{}, Config{
		DmsgPort: skyenv.DmsgVisorTPListCXOPort,
		Logger:   logging.MustGetLogger("tpd-cxo-tplist-aggregator"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = agg.Close() }) //nolint:errcheck
	agg.Run(ctx)

	var dials atomic.Int32
	var redial atomic.Bool
	pub, err := treestore.NewWithDMSG(visorClient, visorSK, treestore.PubConfig{
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgVisorTPListCXOPort,
		Logger:     logging.MustGetLogger("visor-tplist-pub"),
		Dial: func(ctx context.Context, addr dmsg.Addr) (net.Conn, error) {
			dials.Add(1)
			return visorClient.Dial(ctx, addr)
		},
		Redial: func(_ context.Context, peer cipher.PubKey, addr net.Addr) bool {
			require.Equal(t, tpdPK, peer)
			_, overDmsg := addr.(dmsg.Addr)
			require.True(t, overDmsg)
			return redial.Load()
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	announce := func() {
		actx, acancel := context.WithTimeout(ctx, 20*time.Second)
		defer acancel()
		require.NoError(t, pub.AnnounceTo(actx, tpdPK))
	}
	announce()
	require.Equal(t, int32(1), dials.Load())
	announce()
	require.Equal(t, int32(1), dials.Load(), "a live conn is kept")

	redial.Store(true)
	announce()
	require.Equal(t, int32(2), dials.Load(), "Redial replaces the live conn")
	redial.Store(false)
	_, live := pub.PeerAddr(tpdPK)
	require.True(t, live, "the new conn is live")
}
