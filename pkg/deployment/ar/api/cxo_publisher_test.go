// Package api — cxo_publisher_test.go drives the bindings publisher end to end
// over the CXO node's native TCP transport (no dmsg, no discovery) and asserts
// the two things a Preview reader depends on: the bucket level is DENSE (all
// 256 buckets published, so a bucket is addressable by computed index) and a
// bound peer's record lands in the bucket its public key names.
package api

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/deployment/ar/store"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestBindingsCXOPublisherTreeShape(t *testing.T) {
	ctx := context.Background()
	pkAR, skAR := cipher.GenerateKeyPair()

	st, err := store.New(ctx, storeconfig.Config{Type: storeconfig.Memory}, time.Hour, logging.MustGetLogger("t"))
	require.NoError(t, err)

	// Seed before the publisher starts, so the startup resync is what puts
	// these into the tree.
	// Enough peers to spread across most buckets.
	const nPeers = 200
	peers := make([]cipher.PubKey, 0, nPeers)
	for i := 0; i < nPeers; i++ {
		pk, _ := cipher.GenerateKeyPair()
		peers = append(peers, pk)
		require.NoError(t, st.Bind(ctx, types.STCPR, pk, addrresolver.VisorData{
			RemoteAddr:     "10.1.2.3:7777",
			LocalAddresses: addrresolver.LocalAddresses{Port: "7777", Addresses: []string{"10.1.2.3"}},
		}))
	}

	pub, err := treestore.NewWithTCP("127.0.0.1:0", skAR, treestore.PubConfig{
		InMemoryDB:  true,
		BatchWindow: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	pub.SetAllowlist(nil)

	p := newBindingsCXOPublisher(pub, st, logging.MustGetLogger("t-bindings"), 50*time.Millisecond, time.Hour)
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck

	sub, err := treestore.NewSubscriberTCP("", pkAR, treestore.SubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck

	// Connect, then wait on the CONTENT rather than on the first Root: under a
	// loaded machine (this package's other CXO test runs first) the initial
	// Root can take longer than a fixed wait, and every assertion below is
	// already a poll with its own budget.
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(cctx, pub.Node().TCP().Address()))

	// The level must be dense: exactly BucketCount leaves, named by
	// BucketNameAt(0..255). That density is what makes a bucket addressable by
	// index rather than by a linear name scan.
	waitFor(t, func() bool {
		seen := 0
		sub.Walk("", func(_ string, _ []byte) bool { seen++; return true })
		return seen == arfeed.BucketCount
	}, "publisher must materialize all %d buckets", arfeed.BucketCount)

	for i := 0; i < arfeed.BucketCount; i++ {
		_, ok := sub.Get(arfeed.BucketPathAt(i))
		require.Truef(t, ok, "bucket %s must be present", arfeed.BucketPathAt(i))
	}

	// Every seeded peer must be readable from the bucket its own key names.
	waitFor(t, func() bool {
		for _, pk := range peers {
			blob, ok := sub.Get(arfeed.BucketPath(pk))
			if !ok {
				return false
			}
			recs, err := arfeed.DecodeBucket(blob)
			if err != nil {
				return false
			}
			if recs[pk.Hex()].Get(types.STCPR) == nil {
				return false
			}
		}
		return true
	}, "all seeded peers must appear in their own bucket")

	// A later bind reaches the feed through the dirty-mark path, not a resync
	// (the resync interval here is an hour).
	late, _ := cipher.GenerateKeyPair()
	require.NoError(t, st.Bind(ctx, types.SUDPH, late, addrresolver.VisorData{RemoteAddr: "9.9.9.9:1234"}))
	p.MarkDirty(types.SUDPH, late)
	waitFor(t, func() bool {
		blob, ok := sub.Get(arfeed.BucketPath(late))
		if !ok {
			return false
		}
		recs, err := arfeed.DecodeBucket(blob)
		if err != nil {
			return false
		}
		vd := recs[late.Hex()].Get(types.SUDPH)
		return vd != nil && vd.RemoteAddr == "9.9.9.9:1234"
	}, "an incremental bind must reach the feed without waiting for a resync")

	// And a DelBind must remove it again.
	require.NoError(t, st.DelBind(ctx, types.SUDPH, late))
	p.MarkDirty(types.SUDPH, late)
	waitFor(t, func() bool {
		blob, ok := sub.Get(arfeed.BucketPath(late))
		if !ok {
			return false
		}
		recs, err := arfeed.DecodeBucket(blob)
		if err != nil {
			return false
		}
		return recs[late.Hex()] == nil
	}, "a DelBind must drop the peer from its bucket")
}

func waitFor(t *testing.T, cond func() bool, msg string, args ...interface{}) {
	t.Helper()
	// Generous: this waits on a real CXO publish + fill, and the budget is a
	// deadlock backstop, not an expected duration. Under -race on a loaded
	// runner the first Root of a 256-leaf tree took over 30s.
	end := time.Now().Add(2 * time.Minute)
	for {
		if cond() {
			return
		}
		if time.Now().After(end) {
			t.Fatalf(msg, args...)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// toggleStore makes Resolve fail on demand with an error that is NOT
// store.ErrNoEntry — a redis blip, a context that ran out — so the publisher's
// handling of "cannot say" can be told apart from "not there".
type toggleStore struct {
	store.Store
	failing atomic.Bool
}

func (s *toggleStore) Resolve(ctx context.Context, netType types.Type, pk cipher.PubKey) (addrresolver.VisorData, error) {
	if s.failing.Load() {
		return addrresolver.VisorData{}, errors.New("transient store failure")
	}
	return s.Store.Resolve(ctx, netType, pk)
}

// TestTransientStoreErrorDoesNotPublishAbsence is the correctness case the
// publisher exists to get right: a reader cannot distinguish a peer the AR
// genuinely has no binding for from one the publisher dropped because redis
// blinked. So a Resolve failure that is not a definite "no entry" must leave
// the published record alone.
func TestTransientStoreErrorDoesNotPublishAbsence(t *testing.T) {
	ctx := context.Background()
	log := logging.MustGetLogger("t-transient")

	base, err := store.New(ctx, storeconfig.Config{Type: storeconfig.Memory}, time.Hour, log)
	require.NoError(t, err)
	st := &toggleStore{Store: base}

	pk, _ := cipher.GenerateKeyPair()
	require.NoError(t, base.Bind(ctx, types.STCPR, pk, addrresolver.VisorData{RemoteAddr: "192.0.2.10:5000"}))

	pkAR, skAR := cipher.GenerateKeyPair()
	pub, err := treestore.NewWithTCP("127.0.0.1:0", skAR, treestore.PubConfig{
		InMemoryDB:  true,
		BatchWindow: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	pub.SetAllowlist(nil)

	p := newBindingsCXOPublisher(pub, st, log, 50*time.Millisecond, time.Hour)
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck

	sub, err := treestore.NewSubscriberTCP("", pkAR, treestore.SubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(cctx, pub.Node().TCP().Address()))

	hasBinding := func() bool {
		blob, ok := sub.Get(arfeed.BucketPath(pk))
		if !ok {
			return false
		}
		recs, derr := arfeed.DecodeBucket(blob)
		return derr == nil && recs[pk.Hex()].Get(types.STCPR) != nil
	}
	waitFor(t, hasBinding, "the seeded peer must reach the feed")

	// Now the store cannot answer. Several flush windows of dirty marks must
	// not remove the peer.
	st.failing.Store(true)
	for i := 0; i < 10; i++ {
		p.MarkDirty(types.STCPR, pk)
		time.Sleep(60 * time.Millisecond)
	}
	require.True(t, hasBinding(), "a transient store error must not publish an absence")

	// A definite absence still removes it.
	st.failing.Store(false)
	require.NoError(t, base.DelBind(ctx, types.STCPR, pk))
	p.MarkDirty(types.STCPR, pk)
	waitFor(t, func() bool { return !hasBinding() }, "a real DelBind must still drop the peer")
}
