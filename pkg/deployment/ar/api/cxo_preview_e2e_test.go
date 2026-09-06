// Package api — cxo_preview_e2e_test.go runs the whole read path in-process
// over the CXO node's native TCP transport: this package's bindings publisher
// on one side, the Preview-backed resolver on the other. It pins the three
// claims the design rests on — the lookup finds the record, it costs a small
// FIXED number of object fetches no matter how many peers the feed holds, and
// the reader ends up subscribed to nothing.
//
// It lives here rather than under arpreview because it needs this package's
// unexported publisher constructor; arpreview imports nothing from here, so
// there is no cycle.
package api

import (
	"context"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxopreview"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/deployment/ar/arpreview"
	"github.com/skycoin/skywire/pkg/deployment/ar/store"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// maxLookupObjects is the ceiling the walk must stay under. The walk itself is
// the schema registry, the root TreeNode, the Refs nodes on the path to the
// bucket, and the bucket entry. If the reader were scanning the bucket level
// instead of indexing into it this would be in the hundreds, and if it were
// subscribing it would be the whole feed.
const maxLookupObjects = 12

func TestPreviewLookupIsFixedCostAndHoldsNothing(t *testing.T) {
	ctx := context.Background()
	log := logging.MustGetLogger("ar-preview-e2e")

	st, err := store.New(ctx, storeconfig.Config{Type: storeconfig.Memory}, time.Hour, log)
	require.NoError(t, err)

	// Deliberately far more peers than fit in one bucket: a scanning walk's
	// cost would scale with this number, an indexed walk's does not.
	const nPeers = 1000
	peers := make([]cipher.PubKey, 0, nPeers)
	for i := 0; i < nPeers; i++ {
		pk, _ := cipher.GenerateKeyPair()
		peers = append(peers, pk)
		require.NoError(t, st.Bind(ctx, types.STCPR, pk, addrresolver.VisorData{
			RemoteAddr: "203.0.113.7:7777",
			LocalAddresses: addrresolver.LocalAddresses{
				Port: "7777", Addresses: []string{"203.0.113.7", "10.0.0.5"},
			},
		}))
		require.NoError(t, st.Bind(ctx, types.SUDPH, pk, addrresolver.VisorData{
			RemoteAddr: "203.0.113.7:30000",
		}))
	}

	arPK, arSK := cipher.GenerateKeyPair()
	pub, err := treestore.NewWithTCP("127.0.0.1:0", arSK, treestore.PubConfig{
		InMemoryDB:  true,
		BatchWindow: 50 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	pub.SetAllowlist(nil)

	p := newBindingsCXOPublisher(pub, st, log, 50*time.Millisecond, time.Hour)
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck

	_, readerSK := cipher.GenerateKeyPair()
	reader, err := cxopreview.NewTCP(readerSK, cxopreview.Config{Logger: log})
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() }) //nolint:errcheck

	gotPK, err := reader.ConnectTCP(pub.Node().TCP().Address())
	require.NoError(t, err)
	require.Equal(t, arPK, gotPK, "the publisher's node identity is the AR key")

	res := arpreview.NewWithReader(reader, arPK, log)

	// Wait for the publisher's first Root to carry the seeded peers. Every
	// attempt is a real preview, so this also proves a lookup against a feed
	// that is not yet complete fails cheaply rather than hanging.
	target := peers[nPeers/2]
	var stats cxopreview.Stats
	deadline := time.Now().Add(60 * time.Second)
	for {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		rec, s, lerr := res.Bindings(lctx, target)
		cancel()
		if lerr == nil && rec != nil {
			stats = s
			require.NotNil(t, rec.Get(types.STCPR))
			require.NotNil(t, rec.Get(types.SUDPH))
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("target peer never appeared in the previewed feed: %v", lerr)
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Logf("preview lookup over %d peers: objects=%d bytes=%d elapsed=%s",
		nPeers, stats.Objects, stats.Bytes, stats.Elapsed)
	require.LessOrEqual(t, stats.Objects, maxLookupObjects,
		"a bucket lookup must be a fixed handful of fetches, not a scan")

	// Peers at both ends of the key space must cost the same as one in the
	// middle — that is what "independent of position" means.
	for _, pk := range []cipher.PubKey{peers[0], peers[1], peers[nPeers-1]} {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		vd, lerr := res.Resolve(lctx, string(types.STCPR), pk)
		cancel()
		require.NoErrorf(t, lerr, "resolving %s", pk)
		require.Equal(t, "203.0.113.7:7777", vd.RemoteAddr)
		require.LessOrEqual(t, res.LastStats().Objects, maxLookupObjects)
	}

	// An unknown peer is a clean, cheap miss the caller can fall through on.
	unknown, _ := cipher.GenerateKeyPair()
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, lerr := res.Resolve(lctx, string(types.STCPR), unknown)
	cancel()
	require.ErrorIs(t, lerr, arpreview.ErrNoEntry)
	require.LessOrEqual(t, res.LastStats().Objects, maxLookupObjects)

	// The claim everything else rests on: after all those lookups the reader
	// is subscribed to nothing.
	n := reader.Node()
	require.Empty(t, n.Feeds(), "preview must leave the reader subscribed to nothing")
	require.False(t, n.IsSharing(skycipher.PubKey(arPK)),
		"the preview callback returned false, so the feed must not be subscribed")
}

// TestPreviewFallsBackOnASparseTree covers the compatibility path: a publisher
// that does NOT keep its levels dense — an older build, or one that dropped
// empty buckets — breaks index addressing, because the child at the computed
// position is then some other bucket. The reader must notice (the name check)
// and fall back to a binary search over the sorted level rather than returning
// a wrong record or a false miss.
func TestPreviewFallsBackOnASparseTree(t *testing.T) {
	ctx := context.Background()
	log := logging.MustGetLogger("ar-preview-sparse")

	arPK, arSK := cipher.GenerateKeyPair()
	pub, err := treestore.NewWithTCP("127.0.0.1:0", arSK, treestore.PubConfig{
		InMemoryDB:  true,
		BatchWindow: 50 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	pub.SetAllowlist(nil)

	// Publish ONLY the buckets that have content, so both levels are sparse.
	target, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	for _, pk := range []cipher.PubKey{target, other} {
		blob, eerr := arfeed.EncodeBucket(map[cipher.PubKey]*arfeed.PeerBindings{
			pk: {STCPR: &addrresolver.VisorData{RemoteAddr: "198.51.100.9:1234"}},
		})
		require.NoError(t, eerr)
		require.NoError(t, pub.Put(arfeed.BucketPath(pk), blob))
	}
	require.NoError(t, pub.Flush())

	_, readerSK := cipher.GenerateKeyPair()
	reader, err := cxopreview.NewTCP(readerSK, cxopreview.Config{Logger: log})
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close() }) //nolint:errcheck
	_, err = reader.ConnectTCP(pub.Node().TCP().Address())
	require.NoError(t, err)

	res := arpreview.NewWithReader(reader, arPK, log)
	deadline := time.Now().Add(30 * time.Second)
	for {
		lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		vd, lerr := res.Resolve(lctx, string(types.STCPR), target)
		cancel()
		if lerr == nil {
			require.Equal(t, "198.51.100.9:1234", vd.RemoteAddr)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sparse-tree lookup never succeeded: %v", lerr)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// And a key whose bucket was never published is still a clean miss.
	absent, _ := cipher.GenerateKeyPair()
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, lerr := res.Resolve(lctx, string(types.STCPR), absent)
	cancel()
	require.Error(t, lerr)
}
