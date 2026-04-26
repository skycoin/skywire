package dht

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestDiscAdapter(t *testing.T) {
	const timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	net := NewMockNetwork()
	log := logging.MustGetLogger("dht_disc_test")

	// Create 3 nodes.
	pks := make([]cipher.PubKey, 3)
	sks := make([]cipher.SecKey, 3)
	nodes := make([]*Node, 3)

	for i := 0; i < 3; i++ {
		pks[i], sks[i] = cipher.GenerateKeyPair()
		cfg := Config{ItemTTL: time.Hour, RefreshInterval: time.Minute}
		if i > 0 {
			cfg.BootstrapPKs = []cipher.PubKey{pks[0]}
		}
		nodes[i] = New(cfg, pks[i], sks[i], net.Transport(pks[i]), log)
		require.NoError(t, nodes[i].Start(ctx))
	}
	defer func() {
		for _, n := range nodes {
			n.Stop() //nolint:errcheck,gosec
		}
	}()

	time.Sleep(300 * time.Millisecond) // let bootstrap settle

	// Create discovery adapter on node 0.
	adapter := NewDiscAdapter(nodes[0], log)

	// Node 1 publishes a client entry via its own adapter.
	adapter1 := NewDiscAdapter(nodes[1], log)

	srvPK, _ := cipher.GenerateKeyPair()
	entry := &disc.Entry{
		Version:  "0.0.1",
		Sequence: 1,
		Static:   pks[1],
		Client: &disc.Client{
			DelegatedServers: []cipher.PubKey{srvPK},
		},
	}

	err := adapter1.PostEntry(ctx, entry)
	require.NoError(t, err)

	// Node 2's adapter should be able to find the entry.
	adapter2 := NewDiscAdapter(nodes[2], log)
	got, err := adapter2.Entry(ctx, pks[1])
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, pks[1], got.Static)
	assert.Equal(t, uint64(1), got.Sequence)
	assert.NotNil(t, got.Client)
	assert.Equal(t, srvPK, got.Client.DelegatedServers[0])

	// Update the entry with higher seq.
	entry.Sequence = 2
	err = adapter1.PutEntry(ctx, sks[1], entry)
	require.NoError(t, err)

	// Node 0 should see the updated entry.
	got2, err := adapter.Entry(ctx, pks[1])
	require.NoError(t, err)
	assert.Equal(t, uint64(2), got2.Sequence)

	// Lookup a nonexistent entry.
	unknownPK, _ := cipher.GenerateKeyPair()
	_, err = adapter.Entry(ctx, unknownPK)
	assert.Error(t, err)
}

func TestDiscAdapterServerCache(t *testing.T) {
	const timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	net := NewMockNetwork()
	log := logging.MustGetLogger("dht_disc_server_test")

	pk1, sk1 := cipher.GenerateKeyPair()
	pk2, sk2 := cipher.GenerateKeyPair()

	cfg1 := Config{ItemTTL: time.Hour, RefreshInterval: time.Minute}
	cfg2 := Config{ItemTTL: time.Hour, RefreshInterval: time.Minute, BootstrapPKs: []cipher.PubKey{pk1}}

	n1 := New(cfg1, pk1, sk1, net.Transport(pk1), log)
	n2 := New(cfg2, pk2, sk2, net.Transport(pk2), log)
	require.NoError(t, n1.Start(ctx))
	require.NoError(t, n2.Start(ctx))
	defer n1.Stop() //nolint:errcheck,gosec
	defer n2.Stop() //nolint:errcheck,gosec

	time.Sleep(300 * time.Millisecond)

	// Node 1 publishes a server entry.
	adapter1 := NewDiscAdapter(n1, log)
	serverEntry := &disc.Entry{
		Version:  "0.0.1",
		Sequence: 1,
		Static:   pk1,
		Server: &disc.Server{
			Address:           "1.2.3.4:30080",
			AvailableSessions: 100,
		},
	}
	require.NoError(t, adapter1.PostEntry(ctx, serverEntry))

	// Node 2 fetches it — should populate server cache.
	adapter2 := NewDiscAdapter(n2, log)
	_, err := adapter2.Entry(ctx, pk1)
	require.NoError(t, err)

	servers, err := adapter2.AvailableServers(ctx)
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.Equal(t, pk1, servers[0].Static)
	assert.Equal(t, "1.2.3.4:30080", servers[0].Server.Address)
}
