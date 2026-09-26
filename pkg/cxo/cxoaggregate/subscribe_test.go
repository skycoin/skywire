package cxoaggregate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestAnnouncingVisorsAreSubscribedOnConnect: a visor's announce conn
// carries nothing until the aggregator subscribes, and the node idles it
// out after 90 s — so the subscribe has to follow the connect promptly,
// not wait for its turn in a pass over the whole fleet. The periodic pass
// is set to an hour here, so only the on-connect path can do it.
func TestAnnouncingVisorsAreSubscribedOnConnect(t *testing.T) {
	const visors = 5
	env := dmsgtest.NewEnv(t, testTimeout)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	svcPK, svcSK := cipher.GenerateKeyPair()
	svcC, err := env.NewClientWithKeys(svcPK, svcSK, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	core, err := New(svcC, svcSK, 50, Options{
		InMemoryDB: true, ReconcileInterval: time.Hour, Logger: logging.MustGetLogger("agg"),
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	core.Run(ctx)
	t.Cleanup(func() { _ = core.Close() }) //nolint:errcheck

	var want []cipher.PubKey
	for i := 0; i < visors; i++ {
		pk, sk := cipher.GenerateKeyPair()
		c, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
		require.NoError(t, err)
		pub, err := treestore.NewWithDMSG(c, sk, treestore.PubConfig{
			InMemoryDB: true, DmsgPort: 50, SubscriberAllowlist: []cipher.PubKey{svcPK},
			Logger: logging.MustGetLogger("visor"),
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
		require.NoError(t, pub.Put("tp-list", []byte("[]")))
		actx, acancel := context.WithTimeout(ctx, 15*time.Second)
		require.NoError(t, pub.AnnounceTo(actx, svcPK))
		acancel()
		want = append(want, pk)
	}

	subscribed := func() int {
		n := 0
		for _, conn := range core.Node().Connections() {
			for _, pk := range want {
				if conn.PeerID() == skycipher.PubKey(pk) && alreadySubscribed(conn, skycipher.PubKey(pk)) {
					n++
				}
			}
		}
		return n
	}
	require.Eventually(t, func() bool { return subscribed() == visors }, 15*time.Second, 100*time.Millisecond,
		"every announcing visor subscribed on connect")
}
