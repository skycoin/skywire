package treestore

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestPublisherCleanupBoundedWithSubscriber reproduces the production TPD
// leak: a publisher re-publishing a large changing value to a FIXED path
// (the all-transports / metrics / uptime snapshot pattern) while a real
// subscriber is connected and syncing.
//
// TestPublisherAsyncCleanupBounded covers the same publish pattern with NO
// subscriber and stays bounded. Production has 179 subscribers, and the heap
// shows the CXDS growing ~80 MB/min — so the subscriber path is the trigger.
// This asserts the publisher's CXDS stays bounded with a subscriber attached.
func TestPublisherCleanupBoundedWithSubscriber(t *testing.T) {
	// Re-enabled: the Close hang was a blocking failureq/successq send in
	// fillHead.request (now guarded by <-f.closeq, pkg/cxo/node/head.go) plus a
	// leaked indexStat.secondLoop goroutine (Container.Close now closes the
	// embedded Index, pkg/cxo/skyobject/container.go). With both fixed,
	// Node.Close no longer deadlocks and this validates the #3047 fix again.
	pkA, skA := cipher.GenerateKeyPair()

	pub, err := NewWithTCP("127.0.0.1:0", skA, PubConfig{
		InMemoryDB:  true,
		BatchWindow: 5 * time.Millisecond,
	})
	require.NoError(t, err, "NewWithTCP publisher")
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	addr := pub.Node().TCP().Address()
	require.NotEmpty(t, addr)

	sub, err := NewSubscriberTCP("", pkA, SubConfig{InMemoryDB: true})
	require.NoError(t, err, "NewSubscriberTCP")
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(ctx, addr), "ConnectTCP")

	mkRandom := func(size int) []byte {
		b := make([]byte, size)
		_, _ = rand.Read(b)
		return b
	}

	// all-transports pattern: one fixed path, a fresh large value every tick.
	for i := 0; i < 40; i++ {
		require.NoError(t, pub.Put("all-transports", mkRandom(2*1024*1024)))
		require.NoError(t, pub.Flush())
		time.Sleep(20 * time.Millisecond)
	}

	cxds := pub.cxoNode.Container().DB().CXDS()
	all, _ := cxds.Amount()
	vol, _ := cxds.Volume()
	rcHist := map[uint32]int{}
	if err := cxds.Iterate(func(_ skycipher.SHA256, rc uint32, _ []byte) error { rcHist[rc]++; return nil }); err != nil {
		t.Fatalf("Iterate: %v", err)
	}
	t.Logf("After 40 ticks with subscriber: amount=%d volume=%d rcHist=%v", all, vol, rcHist)

	// One ~2 MB leaf + a handful of small structural objects per kept Root.
	// keepLast=1 means ~4 MB steady state. 40 ticks × 2 MB = 80 MB if the
	// per-Root cleanup is fully defeated; anything past 16 MB is the leak.
	const limit = 16 * 1024 * 1024
	if vol = awaitVolumeBound(t, cxds, limit, 30*time.Second); vol > limit {
		t.Errorf("CXDS volume %d > 16 MB — leak with a subscriber connected (per-Root retention should be ~4 MB)", vol)
	}
}

// TestSubscriberCXDSBounded is the subscriber-side mirror of
// TestPublisherCleanupBoundedWithSubscriber.
//
// CXO never reclaims anything on its own: every filled Root and every object
// it references keeps rc>=1 forever unless something calls DelRoot +
// IterateDel. The Publisher has run that sweep since #3047
// (Publisher.runCleanupLoop → cxoutils.RemoveRootObjects/RemoveObjects); the
// Subscriber never did. So a subscriber that owns its node accumulates every
// version of every leaf it has ever filled — with InMemoryDB (what
// cxosub.Manager uses on every visor) that is straight, unbounded heap
// growth, and because memoryCXDS.Set stores the caller's slice by reference
// the retained bytes are the msg.Decode buffers the fill decoded them from.
//
// One fixed path re-published with a fresh 2 MB body 40 times: steady state
// is ~one kept Root (~4 MB with structural objects); 80 MB is "nothing is
// ever released".
func TestSubscriberCXDSBounded(t *testing.T) {
	pkA, skA := cipher.GenerateKeyPair()

	pub, err := NewWithTCP("127.0.0.1:0", skA, PubConfig{
		InMemoryDB:  true,
		BatchWindow: 5 * time.Millisecond,
	})
	require.NoError(t, err, "NewWithTCP publisher")
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	addr := pub.Node().TCP().Address()
	require.NotEmpty(t, addr)

	sub, err := NewSubscriberTCP("", pkA, SubConfig{InMemoryDB: true})
	require.NoError(t, err, "NewSubscriberTCP")
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(ctx, addr), "ConnectTCP")

	mkRandom := func(size int) []byte {
		b := make([]byte, size)
		_, _ = rand.Read(b)
		return b
	}

	const ticks = 40
	for i := 0; i < ticks; i++ {
		require.NoError(t, pub.Put("all-transports", mkRandom(2*1024*1024)))
		require.NoError(t, pub.Flush())
		time.Sleep(20 * time.Millisecond)
	}

	// Let the last fills land and the sweep run.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := sub.Get("all-transports"); ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)

	cxds := sub.cxoNode.Container().DB().CXDS()
	amount, _ := cxds.Amount()
	vol, _ := cxds.Volume()
	t.Logf("subscriber CXDS after %d republishes: amount=%d volume=%d", ticks, amount, vol)

	if vol > 16*1024*1024 {
		t.Errorf("subscriber CXDS volume %d > 16 MB after %d republishes of a single 2 MB leaf — "+
			"superseded Roots and their objects are never released", vol, ticks)
	}
}
