package direct

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

func TestDirectClient_Entry(t *testing.T) {
	log := logging.MustGetLogger("direct_test")

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	pkMissing, _ := cipher.GenerateKeyPair()

	entries := []*disc.Entry{
		{Static: pk1, Server: &disc.Server{Address: "addr1"}},
		{Static: pk2, Server: &disc.Server{Address: "addr2"}},
	}

	client := NewClient(entries, log)
	ctx := context.Background()

	// Existing entries should be found.
	e1, err := client.Entry(ctx, pk1)
	require.NoError(t, err)
	assert.Equal(t, pk1, e1.Static)

	e2, err := client.Entry(ctx, pk2)
	require.NoError(t, err)
	assert.Equal(t, pk2, e2.Static)

	// Non-existent entry should return ErrKeyNotFound.
	_, err = client.Entry(ctx, pkMissing)
	assert.ErrorIs(t, err, disc.ErrKeyNotFound)
}

func TestDirectClient_PostAndDelete(t *testing.T) {
	log := logging.MustGetLogger("direct_test")

	client := NewClient(nil, log)
	ctx := context.Background()

	pk, _ := cipher.GenerateKeyPair()
	entry := &disc.Entry{
		Static: pk,
		Server: &disc.Server{Address: "addr1"},
	}

	// Entry should not exist yet.
	_, err := client.Entry(ctx, pk)
	assert.ErrorIs(t, err, disc.ErrKeyNotFound)

	// Post entry.
	require.NoError(t, client.PostEntry(ctx, entry))

	// Entry should now be retrievable.
	got, err := client.Entry(ctx, pk)
	require.NoError(t, err)
	assert.Equal(t, pk, got.Static)

	// Delete entry.
	require.NoError(t, client.DelEntry(ctx, entry))

	// Entry should be gone.
	_, err = client.Entry(ctx, pk)
	assert.ErrorIs(t, err, disc.ErrKeyNotFound)
}

func TestDirectClient_PutEntry(t *testing.T) {
	log := logging.MustGetLogger("direct_test")

	pk, sk := cipher.GenerateKeyPair()
	original := &disc.Entry{
		Static: pk,
		Server: &disc.Server{Address: "old-addr"},
	}

	client := NewClient([]*disc.Entry{original}, log)
	ctx := context.Background()

	// Verify original.
	got, err := client.Entry(ctx, pk)
	require.NoError(t, err)
	assert.Equal(t, "old-addr", got.Server.Address)

	// Update via PutEntry.
	updated := &disc.Entry{
		Static: pk,
		Server: &disc.Server{Address: "new-addr"},
	}
	require.NoError(t, client.PutEntry(ctx, sk, updated))

	// Verify update persists.
	got, err = client.Entry(ctx, pk)
	require.NoError(t, err)
	assert.Equal(t, "new-addr", got.Server.Address)
}

func TestDirectClient_AvailableServers(t *testing.T) {
	log := logging.MustGetLogger("direct_test")

	pkSrv1, _ := cipher.GenerateKeyPair()
	pkSrv2, _ := cipher.GenerateKeyPair()
	pkClient, _ := cipher.GenerateKeyPair()

	entries := []*disc.Entry{
		{Static: pkSrv1, Server: &disc.Server{Address: "srv1"}},
		{Static: pkSrv2, Server: &disc.Server{Address: "srv2"}},
		{Static: pkClient, Client: &disc.Client{DelegatedServers: []cipher.PubKey{pkSrv1}}},
	}

	client := NewClient(entries, log)
	ctx := context.Background()

	// AvailableServers should return only server entries.
	servers, err := client.AvailableServers(ctx)
	require.NoError(t, err)
	assert.Len(t, servers, 2)
	for _, s := range servers {
		assert.NotNil(t, s.Server)
	}

	// AllServers should return the same set.
	allSrv, err := client.AllServers(ctx)
	require.NoError(t, err)
	assert.Len(t, allSrv, 2)
	for _, s := range allSrv {
		assert.NotNil(t, s.Server)
	}
}

func TestDirectClient_AllEntries(t *testing.T) {
	log := logging.MustGetLogger("direct_test")

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	pk3, _ := cipher.GenerateKeyPair()

	entries := []*disc.Entry{
		{Static: pk1, Server: &disc.Server{Address: "srv1"}},
		{Static: pk2, Client: &disc.Client{}},
		{Static: pk3, Server: &disc.Server{Address: "srv2"}},
	}

	client := NewClient(entries, log)
	ctx := context.Background()

	all, err := client.AllEntries(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 3)

	// Verify all PKs are present.
	hexSet := make(map[string]bool)
	for _, h := range all {
		hexSet[h] = true
	}
	assert.True(t, hexSet[pk1.Hex()])
	assert.True(t, hexSet[pk2.Hex()])
	assert.True(t, hexSet[pk3.Hex()])
}

func TestDirectClient_ClientsByServer(t *testing.T) {
	log := logging.MustGetLogger("direct_test")

	pkSrv1, _ := cipher.GenerateKeyPair()
	pkSrv2, _ := cipher.GenerateKeyPair()
	pkC1, _ := cipher.GenerateKeyPair()
	pkC2, _ := cipher.GenerateKeyPair()
	pkC3, _ := cipher.GenerateKeyPair()

	entries := []*disc.Entry{
		{Static: pkSrv1, Server: &disc.Server{Address: "srv1"}},
		{Static: pkSrv2, Server: &disc.Server{Address: "srv2"}},
		{Static: pkC1, Client: &disc.Client{DelegatedServers: []cipher.PubKey{pkSrv1}}},
		{Static: pkC2, Client: &disc.Client{DelegatedServers: []cipher.PubKey{pkSrv1, pkSrv2}}},
		{Static: pkC3, Client: &disc.Client{DelegatedServers: []cipher.PubKey{pkSrv2}}},
	}

	client := NewClient(entries, log)
	ctx := context.Background()

	// ClientsByServer for srv1 should return pkC1 and pkC2.
	srv1Clients, err := client.ClientsByServer(ctx, pkSrv1)
	require.NoError(t, err)
	assert.Len(t, srv1Clients, 2)
	pks := make(map[cipher.PubKey]bool)
	for _, e := range srv1Clients {
		pks[e.Static] = true
	}
	assert.True(t, pks[pkC1])
	assert.True(t, pks[pkC2])

	// ClientsByServer for srv2 should return pkC2 and pkC3.
	srv2Clients, err := client.ClientsByServer(ctx, pkSrv2)
	require.NoError(t, err)
	assert.Len(t, srv2Clients, 2)
	pks = make(map[cipher.PubKey]bool)
	for _, e := range srv2Clients {
		pks[e.Static] = true
	}
	assert.True(t, pks[pkC2])
	assert.True(t, pks[pkC3])

	// AllClientsByServer should group correctly.
	allByServer, err := client.AllClientsByServer(ctx)
	require.NoError(t, err)
	assert.Len(t, allByServer[pkSrv1.Hex()], 2)
	assert.Len(t, allByServer[pkSrv2.Hex()], 2)
}

func TestGetClientEntry(t *testing.T) {
	pkSrv1, _ := cipher.GenerateKeyPair()
	pkSrv2, _ := cipher.GenerateKeyPair()

	servers := []*disc.Entry{
		{Static: pkSrv1, Server: &disc.Server{Address: "srv1"}},
		{Static: pkSrv2, Server: &disc.Server{Address: "srv2"}},
	}

	pkC1, _ := cipher.GenerateKeyPair()
	pkC2, _ := cipher.GenerateKeyPair()
	pks := cipher.PubKeys{pkC1, pkC2}

	clients := GetClientEntry(pks, servers)
	require.Len(t, clients, 2)

	for _, c := range clients {
		require.NotNil(t, c.Client)
		assert.Len(t, c.Client.DelegatedServers, 2)
		assert.Contains(t, c.Client.DelegatedServers, pkSrv1)
		assert.Contains(t, c.Client.DelegatedServers, pkSrv2)
		assert.Equal(t, "0.0.1", c.Version)
	}

	// Verify the correct static keys are assigned.
	staticSet := make(map[cipher.PubKey]bool)
	for _, c := range clients {
		staticSet[c.Static] = true
	}
	assert.True(t, staticSet[pkC1])
	assert.True(t, staticSet[pkC2])
}

func TestGetAllEntries(t *testing.T) {
	pkSrv, _ := cipher.GenerateKeyPair()
	servers := []*disc.Entry{
		{Static: pkSrv, Server: &disc.Server{Address: "srv1"}},
	}

	pkC1, _ := cipher.GenerateKeyPair()
	pkC2, _ := cipher.GenerateKeyPair()
	pks := cipher.PubKeys{pkC1, pkC2}

	all := GetAllEntries(pks, servers)

	// Should contain 2 clients + 1 server = 3 entries.
	require.Len(t, all, 3)

	staticSet := make(map[cipher.PubKey]bool)
	for _, e := range all {
		staticSet[e.Static] = true
	}
	assert.True(t, staticSet[pkC1])
	assert.True(t, staticSet[pkC2])
	assert.True(t, staticSet[pkSrv])
}
