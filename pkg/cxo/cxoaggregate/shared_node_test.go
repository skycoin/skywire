package cxoaggregate

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
)

// A host that embeds the service already publishes on the port, under the
// same key. The aggregator attaches to that node: it gets the visors' feeds
// from their announce conns, the host's own feed through Ingest, and leaves
// the node running when it closes.
func TestAggregatorOnHostNode(t *testing.T) {
	env := dmsgtest.NewEnv(t, testTimeout)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	hostPK, hostSK := cipher.GenerateKeyPair()
	hostC, err := env.NewClientWithKeys(hostPK, hostSK, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	hostPub, err := treestore.NewWithDMSG(hostC, hostSK, treestore.PubConfig{
		InMemoryDB: true, DmsgPort: 50, Logger: logging.MustGetLogger("host"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = hostPub.Close() }) //nolint:errcheck

	var mu sync.Mutex
	filled := map[skycipher.PubKey]bool{}
	core, err := New(nil, hostSK, 50, Options{
		Node: hostPub.Node(),
		OnRootFilled: func(r *registry.Root) {
			mu.Lock()
			filled[r.Pub] = true
			mu.Unlock()
		},
		Logger: logging.MustGetLogger("agg"),
	})
	require.NoError(t, err)
	require.False(t, core.Owned())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	core.Run(ctx)
	hostPub.SetPublishHook(core.Ingest)

	visorPK, visorSK := cipher.GenerateKeyPair()
	visorC, err := env.NewClientWithKeys(visorPK, visorSK, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	visorPub, err := treestore.NewWithDMSG(visorC, visorSK, treestore.PubConfig{
		InMemoryDB: true, DmsgPort: 50, SubscriberAllowlist: []cipher.PubKey{hostPK},
		Logger: logging.MustGetLogger("visor"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = visorPub.Close() }) //nolint:errcheck
	require.NoError(t, visorPub.Put("tp-list", []byte("[]")))
	require.NoError(t, visorPub.Flush())
	actx, acancel := context.WithTimeout(ctx, 15*time.Second)
	require.NoError(t, visorPub.AnnounceTo(actx, hostPK))
	acancel()

	require.NoError(t, hostPub.Put("tp-list", []byte("[]")))
	require.NoError(t, hostPub.Flush())
	// Announcing to itself is a no-op, not a dial.
	require.NoError(t, hostPub.AnnounceTo(ctx, hostPK))

	has := func(pk cipher.PubKey) func() bool {
		return func() bool {
			mu.Lock()
			defer mu.Unlock()
			return filled[skycipher.PubKey(pk)]
		}
	}
	require.Eventually(t, has(visorPK), 20*time.Second, 100*time.Millisecond, "visor feed filled via the announce conn")
	require.Eventually(t, has(hostPK), 5*time.Second, 50*time.Millisecond, "host feed taken in-process")
	st := core.Stats()
	require.Equal(t, 1, st.Subscribed)
	require.NotZero(t, st.LocalRoots)

	require.NoError(t, core.Close())
	require.NoError(t, hostPub.Put("more", []byte("x")))
	require.NoError(t, hostPub.Flush(), "host publisher keeps working after the aggregator closes")
}
