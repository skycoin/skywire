package dht

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

func TestNodeID(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	id1 := NodeIDFromPubKey(pk1)
	id2 := NodeIDFromPubKey(pk2)

	assert.False(t, id1.IsZero())
	assert.NotEqual(t, id1, id2)

	// Distance is symmetric.
	d12 := Distance(id1, id2)
	d21 := Distance(id2, id1)
	assert.Equal(t, d12, d21)

	// Distance to self is zero.
	d11 := Distance(id1, id1)
	assert.True(t, d11.IsZero())

	// CommonPrefixLen of self is max.
	assert.Equal(t, IDSize*8, CommonPrefixLen(id1, id1))

	// CPL of different IDs is less than max.
	cpl := CommonPrefixLen(id1, id2)
	assert.True(t, cpl < IDSize*8)
}

func TestRoutingTable(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	rt := NewRoutingTable(NodeIDFromPubKey(pk))

	// Add K+5 peers to force bucket saturation.
	var peers []Peer
	for i := 0; i < K+5; i++ {
		ppk, _ := cipher.GenerateKeyPair()
		peers = append(peers, Peer{ID: NodeIDFromPubKey(ppk), PK: ppk})
	}
	for _, p := range peers {
		rt.Update(p)
	}

	// Should have at most K*256 but realistically all fit in various buckets.
	assert.True(t, rt.Size() > 0)

	// FindClosest returns sorted results.
	target, _ := cipher.GenerateKeyPair()
	closest := rt.FindClosest(NodeIDFromPubKey(target), 5)
	assert.True(t, len(closest) <= 5)

	// Verify sort order.
	targetID := NodeIDFromPubKey(target)
	for i := 1; i < len(closest); i++ {
		di := Distance(closest[i-1].ID, targetID)
		dj := Distance(closest[i].ID, targetID)
		assert.True(t, Less(di, dj) || di == dj)
	}
}

func TestMutableItem(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	item := MutableItem{
		K:    pk,
		Seq:  1,
		V:    []byte("hello world"),
		Salt: []byte("test"),
	}

	require.NoError(t, item.Sign(sk))
	require.NoError(t, item.Verify())

	// Tamper with value — should fail.
	item.V = []byte("tampered")
	assert.Error(t, item.Verify())
}

func TestStore(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	store := NewStore(100, time.Hour)

	item1 := MutableItem{K: pk, Seq: 1, V: []byte("v1"), Salt: []byte("s")}
	require.NoError(t, item1.Sign(sk))
	require.NoError(t, store.Put(item1))

	// Get should return the item.
	got := store.Get(item1.Target())
	require.NotNil(t, got)
	assert.Equal(t, []byte("v1"), got.V)

	// Update with higher seq should work.
	item2 := MutableItem{K: pk, Seq: 2, V: []byte("v2"), Salt: []byte("s")}
	require.NoError(t, item2.Sign(sk))
	require.NoError(t, store.Put(item2))

	got = store.Get(item2.Target())
	require.NotNil(t, got)
	assert.Equal(t, []byte("v2"), got.V)

	// Update with lower seq should fail.
	item0 := MutableItem{K: pk, Seq: 1, V: []byte("v0"), Salt: []byte("s")}
	require.NoError(t, item0.Sign(sk))
	assert.ErrorIs(t, store.Put(item0), ErrSeqNotMonotonic)
}

func TestDHTNetwork(t *testing.T) {
	const numNodes = 5
	const timeout = 10 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	net := NewMockNetwork()
	log := logging.MustGetLogger("dht_test")

	// Create nodes.
	var nodes []*Node
	var pks []cipher.PubKey
	for i := 0; i < numNodes; i++ {
		pk, sk := cipher.GenerateKeyPair()
		pks = append(pks, pk)
		cfg := Config{
			ItemTTL:         time.Hour,
			RefreshInterval: time.Minute,
		}
		// Each node bootstraps from the first node (except the first).
		if i > 0 {
			cfg.BootstrapPKs = []cipher.PubKey{pks[0]}
		}
		n := New(cfg, pk, sk, net.Transport(pk), log)
		nodes = append(nodes, n)
	}

	// Start all nodes. Node 0 starts first so others can bootstrap.
	for _, n := range nodes {
		require.NoError(t, n.Start(ctx))
	}
	defer func() {
		for _, n := range nodes {
			n.Stop() //nolint:errcheck,gosec
		}
	}()

	// Give bootstrap time to settle.
	time.Sleep(500 * time.Millisecond)

	// Verify routing tables are populated.
	for i, n := range nodes {
		if i == 0 {
			continue
		}
		assert.True(t, n.RoutingTable().Size() > 0, "node %d has empty routing table", i)
	}

	// Put a value from node 1.
	err := nodes[1].Put(ctx, []byte("test-value"), 1, []byte("salt"))
	require.NoError(t, err)

	// Get from node 3 (should find it via the network).
	item, err := nodes[3].Get(ctx, nodes[1].PK(), []byte("salt"))
	require.NoError(t, err)
	require.NotNil(t, item)
	assert.Equal(t, []byte("test-value"), item.V)
	assert.Equal(t, uint64(1), item.Seq)

	// Update from node 1 with higher seq.
	err = nodes[1].Put(ctx, []byte("updated"), 2, []byte("salt"))
	require.NoError(t, err)

	// Get from node 4 — should see the update.
	item, err = nodes[4].Get(ctx, nodes[1].PK(), []byte("salt"))
	require.NoError(t, err)
	require.NotNil(t, item)
	assert.Equal(t, []byte("updated"), item.V)
	assert.Equal(t, uint64(2), item.Seq)
}
