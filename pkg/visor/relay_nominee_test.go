// Package visor pkg/visor/relay_nominee_test.go c3-visor-core
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

// A hub is a visor that ALSO runs a dmsg server on its own key, which shows up
// as a discovery entry carrying both sections. Those get nominated; a plain
// server, having no visor behind it and so no relay acceptor, does not.
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
	require.NoError(t, cache.Set(dmsgdisc.NewServerEntry(plainServer, 0, "1.2.3.4:8081", 10)))
	// The cache only ever holds server entries — it rejects anything without a
	// server address — so the client section is the discriminator AMONG servers.
	other, _ := cipher.GenerateKeyPair()
	require.Error(t, cache.Set(dmsgdisc.NewClientEntry(other, 0, nil)))

	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: self}}, dmsgServersCache: cache}
	got := v.hubRelayNominees(make(map[cipher.PubKey]struct{}))

	require.Len(t, got, relayHubLimit, "the hub set is bounded")
	for _, pk := range got {
		require.NotEqual(t, plainServer, pk, "a plain dmsg server has no relay acceptor")
		require.Contains(t, hubs, pk)
	}
	require.Equal(t, got, v.hubRelayNominees(make(map[cipher.PubKey]struct{})), "stable across calls")
	sorted := append([]cipher.PubKey(nil), got...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Hex() < sorted[j].Hex() })
	require.Equal(t, sorted, got, "deterministic order")
}

// Nomination must not require any configuration. A visor that has pinned
// nothing still nominates the hubs it can see.
func TestRelayNominees_NeedNoConfiguration(t *testing.T) {
	self, _ := cipher.GenerateKeyPair()
	hub, _ := cipher.GenerateKeyPair()
	cache := NewDmsgServersCache(filepath.Join(t.TempDir(), "dmsg_servers.json"))
	require.NoError(t, cache.Set(hubEntry(hub)))

	v := &Visor{
		conf:             &visorconfig.V1{Common: &visorconfig.Common{PK: self}},
		dmsgServersCache: cache,
	}
	require.Empty(t, v.conf.PersistentTransports, "no pins configured")
	require.Equal(t, []cipher.PubKey{hub}, v.relayNominees())
}

// This visor's hypervisors are never nominated: on a hypervisor that list
// includes the desk tabs it serves, and a host must not carry its dmsg through
// a browser it is hosting.
func TestRelayNominees_NeverRelayThroughAHypervisor(t *testing.T) {
	self, _ := cipher.GenerateKeyPair()
	hv, _ := cipher.GenerateKeyPair()
	cache := NewDmsgServersCache(filepath.Join(t.TempDir(), "dmsg_servers.json"))
	require.NoError(t, cache.Set(hubEntry(hv))) // even if it looks like a hub

	v := &Visor{
		conf: &visorconfig.V1{
			Common:      &visorconfig.Common{PK: self},
			Hypervisors: []cipher.PubKey{hv},
		},
		dmsgServersCache: cache,
	}
	require.Empty(t, v.relayNominees())
}

// A visor never nominates itself, even when it is a hub.
func TestRelayNominees_NeverSelf(t *testing.T) {
	self, _ := cipher.GenerateKeyPair()
	cache := NewDmsgServersCache(filepath.Join(t.TempDir(), "dmsg_servers.json"))
	require.NoError(t, cache.Set(hubEntry(self)))

	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: self}}, dmsgServersCache: cache}
	require.Empty(t, v.relayNominees())
}

// Nothing configured, nothing reachable, nothing nominated, no panic.
func TestRelayNominees_Empty(t *testing.T) {
	self, _ := cipher.GenerateKeyPair()
	require.Empty(t, (&Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: self}}}).relayNominees())
	require.Empty(t, (&Visor{}).relayNominees())
}
