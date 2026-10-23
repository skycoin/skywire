package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/tpd/routingfeed"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// entriesStore serves a settable transport list.
type entriesStore struct {
	store.Store
	entries []*transport.Entry
}

func (s *entriesStore) GetAllTransports(context.Context, bool) ([]*transport.Entry, error) {
	return s.entries, nil
}

func newRoutingTestPublisher(t *testing.T, st store.Store, snapshotEvery int) *RoutingCXOPublisher {
	t.Helper()
	cfg := node.NewConfig()
	cfg.TCP.Listen, cfg.UDP.Listen, cfg.RPC = "", "", ""
	cfg.Config.InMemoryDB = true
	n, err := node.NewNode(cfg)
	require.NoError(t, err)
	_, sk := cipher.GenerateKeyPair()
	pub, err := treestore.New(n, sk, treestore.Config{BatchWindow: 10 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() {
		pub.Close() //nolint:errcheck,gosec
		n.Close()   //nolint:errcheck,gosec
	})
	return &RoutingCXOPublisher{
		api: &API{store: st}, pub: pub, log: logging.MustGetLogger("routing-test"),
		snapshotEvery: snapshotEvery,
		// Publish every change, so the test can follow exact figures.
		smooth: newRoutingSmoother(RoutingSmoothing{LatencyWindow: 1, Hysteresis: -1}),
	}
}

// feed returns every leaf the publisher holds under the routing prefix.
func feed(p *RoutingCXOPublisher) map[string][]byte {
	out := map[string][]byte{}
	p.pub.Walk(routingfeed.Prefix, func(path string, v []byte) bool {
		out[path] = append([]byte(nil), v...)
		return true
	})
	return out
}

func describe(entries []*transport.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, fmt.Sprintf("%s-%s %s %.0f", e.Edges[0].Hex(), e.Edges[1].Hex(), e.Type, e.Latency))
	}
	sort.Strings(out)
	return out
}

func readFeed(leaves map[string][]byte) []*transport.Entry {
	r := routingfeed.NewReader()
	for path, body := range leaves {
		r.Add(path, body)
	}
	return r.Entries()
}

// A publish between snapshots adds one delta and leaves the snapshot shards
// alone; a reader of snapshot plus deltas sees the current transports; the
// next snapshot folds the deltas in and deletes them.
func TestRoutingSnapshotAndDeltas(t *testing.T) {
	k := sortedKeys(4)
	st := &entriesStore{entries: []*transport.Entry{tp(k[0], k[1], 10), tp(k[0], k[2], 20), tp(k[1], k[2], 30)}}
	p := newRoutingTestPublisher(t, st, 2)
	publish := func() map[string][]byte {
		p.publishOnce(context.Background())
		require.NoError(t, p.pub.Flush())
		return feed(p)
	}

	snap := publish()
	require.Len(t, snap, 2, "two shards, no deltas")
	require.Equal(t, describe(st.entries), describe(readFeed(snap)))

	// One latency changes, one transport goes, one arrives.
	st.entries = []*transport.Entry{tp(k[0], k[1], 15), tp(k[1], k[2], 30), tp(k[2], k[3], 40)}
	leaves := publish()
	require.Contains(t, leaves, routingfeed.DeltaPath(1))
	for path, body := range snap {
		require.Equal(t, body, leaves[path], "snapshot shard %s rewritten between snapshots", path)
	}
	require.Equal(t, describe(st.entries), describe(readFeed(leaves)))

	// Nothing changes: no new delta.
	leaves = publish()
	require.NotContains(t, leaves, routingfeed.DeltaPath(2))

	st.entries = st.entries[:2]
	leaves = publish()
	require.Contains(t, leaves, routingfeed.DeltaPath(2))
	require.Equal(t, describe(st.entries), describe(readFeed(leaves)))

	// The third publish since the snapshot is the next snapshot.
	st.entries = append(st.entries, tp(k[0], k[3], 50))
	leaves = publish()
	for path := range leaves {
		require.False(t, strings.HasPrefix(path, routingfeed.DeltaPrefix), "delta %s left after a snapshot", path)
	}
	require.Equal(t, describe(st.entries), describe(readFeed(leaves)))
}

// A reader that decodes every leaf as a shard, as visors before the deltas
// did, skips the deltas and sees the snapshot.
func TestRoutingDeltasSkippedByShardReaders(t *testing.T) {
	k := sortedKeys(3)
	st := &entriesStore{entries: []*transport.Entry{tp(k[0], k[1], 10)}}
	p := newRoutingTestPublisher(t, st, 6)
	p.publishOnce(context.Background())
	st.entries = []*transport.Entry{tp(k[0], k[1], 99), tp(k[1], k[2], 5)}
	p.publishOnce(context.Background())
	require.NoError(t, p.pub.Flush())

	var old []*transport.Entry
	for _, body := range feed(p) {
		var shard []*transport.Entry
		if json.Unmarshal(cxoutils.Gunzip(body), &shard) != nil {
			continue
		}
		old = append(old, shard...)
	}
	require.Equal(t, []string{describe([]*transport.Entry{tp(k[0], k[1], 10)})[0]}, describe(old))
}
