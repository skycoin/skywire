// Package visor pkg/visor/relay_hub_nominee_test.go c3-visor-core
package visor

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func hubEntry(pk cipher.PubKey) *dmsgdisc.Entry {
	e := dmsgdisc.NewClientEntry(pk, 0, nil)
	e.Server = &dmsgdisc.Server{Address: "1.2.3.4:8081", AvailableSessions: 10}
	return e
}

func serverOnlyEntry(pk cipher.PubKey) *dmsgdisc.Entry {
	return dmsgdisc.NewServerEntry(pk, 0, "1.2.3.4:8081", 10)
}

// A hub is a visor that ALSO runs a dmsg server on its own key, which shows up
// as a discovery entry carrying both sections. Those get nominated; a plain
// server (no visor behind it, so no relay acceptor) and a plain client do not.
func TestHubRelayNominees(t *testing.T) {
	self, _ := cipher.GenerateKeyPair()
	cache := NewDmsgServersCache(filepath.Join(t.TempDir(), "dmsg_servers.json"))

	var hubs []cipher.PubKey
	for i := 0; i < 6; i++ {
		pk, _ := cipher.GenerateKeyPair()
		hubs = append(hubs, pk)
		require.NoError(t, cache.Set(hubEntry(pk)))
	}
	plainServer, _ := cipher.GenerateKeyPair()
	require.NoError(t, cache.Set(serverOnlyEntry(plainServer)))
	// The cache only ever holds server entries — it rejects anything without a
	// server address — so the client section is the discriminator AMONG servers.
	plainClient, _ := cipher.GenerateKeyPair()
	require.Error(t, cache.Set(dmsgdisc.NewClientEntry(plainClient, 0, nil)))
	require.NoError(t, cache.Set(hubEntry(self))) // this visor itself

	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: self}}, dmsgServersCache: cache}
	got := v.hubRelayNominees(make(map[cipher.PubKey]struct{}))

	require.Len(t, got, relayHubLimit, "the nominee set is bounded")
	for _, pk := range got {
		require.NotEqual(t, self, pk, "a visor must not nominate itself")
		require.NotEqual(t, plainServer, pk, "a plain dmsg server has no relay acceptor")
		require.NotEqual(t, plainClient, pk, "a plain client is not a hub")
		require.Contains(t, hubs, pk)
	}

	// Stable across calls: a reshuffling set would restart the client's serve
	// pass on every nomination tick.
	require.Equal(t, got, v.hubRelayNominees(make(map[cipher.PubKey]struct{})))
	sorted := append([]cipher.PubKey(nil), got...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Hex() < sorted[j].Hex() })
	require.Equal(t, sorted, got, "nominees are emitted in a deterministic order")
}

// A key already nominated as an operator pin is not nominated twice.
func TestHubRelayNominees_SkipsAlreadySeen(t *testing.T) {
	self, _ := cipher.GenerateKeyPair()
	pinned, _ := cipher.GenerateKeyPair()
	cache := NewDmsgServersCache(filepath.Join(t.TempDir(), "dmsg_servers.json"))
	require.NoError(t, cache.Set(hubEntry(pinned)))

	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: self}}, dmsgServersCache: cache}
	seen := map[cipher.PubKey]struct{}{pinned: {}}
	require.Empty(t, v.hubRelayNominees(seen))
}

// With no cache and no pins there is nothing to nominate, and nothing panics.
func TestRelayNominees_Empty(t *testing.T) {
	self, _ := cipher.GenerateKeyPair()
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: self}}}
	require.Empty(t, v.relayNominees())
	require.Empty(t, (&Visor{}).relayNominees())
}
