// Package visor pkg/visor/api_state_roles_test.go c3-vis-api
package visor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgspec "github.com/skycoin/skywire/pkg/dmsg/dmsgc/spec"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func testConf(pk cipher.PubKey, srv *dmsgspec.DmsgServerConfig) *visorconfig.V1 {
	return &visorconfig.V1{
		Common: &visorconfig.Common{PK: pk},
		Dmsg:   &dmsgspec.DmsgConfig{Server: srv},
	}
}

// A visor with no in-process dmsg server says so, and names no key: the
// absence is the answer an operator is asking for.
func TestDmsgServerRole_None(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	for name, conf := range map[string]*visorconfig.V1{
		"no config":    nil,
		"no dmsg":      {Common: &visorconfig.Common{PK: pk}},
		"no server":    testConf(pk, nil),
		"not enabled":  testConf(pk, &dmsgspec.DmsgServerConfig{}),
		"pinned addr":  testConf(pk, &dmsgspec.DmsgServerConfig{LocalAddress: ":8081"}),
		"config path ": testConf(pk, &dmsgspec.DmsgServerConfig{ConfigPath: "/etc/skywire-dmsg.json"}),
	} {
		role := dmsgServerRole(conf, nil)
		require.False(t, role.Running, name)
		require.False(t, role.Enabled, name)
		require.Empty(t, role.Mode, name)
		require.True(t, role.PK.Null(), name)
	}
}

// Enabled in the config but not started (e.g. no dmsg discovery PK to register
// with) is the case worth seeing: enabled true, running false.
func TestDmsgServerRole_EnabledNotRunning(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	role := dmsgServerRole(testConf(pk, &dmsgspec.DmsgServerConfig{Enabled: true}), nil)
	require.True(t, role.Enabled)
	require.False(t, role.Running)
	require.Equal(t, dmsgServerModeOwnKey, role.Mode)
	require.Equal(t, pk, role.PK)
}

// An own-key server sharing the visor's transport TCP port: same key as the
// visor, no address of its own in the config, and the address it actually
// listens on comes from the running server.
func TestDmsgServerRole_OwnKeySharedPort(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	started := time.Now()

	role := dmsgServerRole(testConf(pk, &dmsgspec.DmsgServerConfig{Enabled: true}), &DmsgServerRole{
		Mode:                dmsgServerModeOwnKey,
		PK:                  pk,
		OwnKey:              true,
		SharedTransportPort: true,
		LocalAddress:        "127.0.0.1:7777",
		StartedAt:           started,
	})

	require.True(t, role.Enabled)
	require.True(t, role.Running)
	require.Equal(t, dmsgServerModeOwnKey, role.Mode)
	require.True(t, role.OwnKey, "an own-key server registers under the visor's identity")
	require.Equal(t, pk, role.PK)
	require.True(t, role.SharedTransportPort, "it takes the transport port's dmsg branch")
	require.Equal(t, "127.0.0.1:7777", role.LocalAddress)
	require.Equal(t, started, role.StartedAt)
	require.Empty(t, role.ConfigPath)
}

// An own-key server pinned to its own address does NOT share the transport port.
func TestDmsgServerRole_OwnKeyOwnAddress(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	role := dmsgServerRole(testConf(pk, &dmsgspec.DmsgServerConfig{
		Enabled: true, LocalAddress: ":8081", PublicAddress: "1.2.3.4:8081",
	}), &DmsgServerRole{
		Mode: dmsgServerModeOwnKey, PK: pk, OwnKey: true,
		LocalAddress: ":8081", PublicAddress: "1.2.3.4:8081",
	})

	require.True(t, role.Running)
	require.False(t, role.SharedTransportPort)
	require.Equal(t, ":8081", role.LocalAddress)
	require.Equal(t, "1.2.3.4:8081", role.PublicAddress)
}

// A config_path server is the standalone dmsg-server service in-process: its
// OWN key, not the visor's, and its own addresses.
func TestDmsgServerRole_ConfigPath(t *testing.T) {
	visorPK, _ := cipher.GenerateKeyPair()
	srvPK, _ := cipher.GenerateKeyPair()

	role := dmsgServerRole(testConf(visorPK, &dmsgspec.DmsgServerConfig{
		Enabled: true, ConfigPath: "/etc/skywire-dmsg.json",
	}), &DmsgServerRole{
		Mode:          dmsgServerModeConfigPath,
		ConfigPath:    "/etc/skywire-dmsg.json",
		PK:            srvPK,
		LocalAddress:  ":8081",
		PublicAddress: "1.2.3.4:8081",
	})

	require.True(t, role.Running)
	require.Equal(t, dmsgServerModeConfigPath, role.Mode)
	require.False(t, role.OwnKey, "a config_path server runs on a key of its own")
	require.Equal(t, srvPK, role.PK)
	require.NotEqual(t, visorPK, role.PK)
	require.Equal(t, "/etc/skywire-dmsg.json", role.ConfigPath)
	require.False(t, role.SharedTransportPort, "its own listener, its own addresses")
}

// no_transit and the visor's identity come straight from the config.
func TestDmsgServerRole_NoTransitAndPK(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	conf := testConf(pk, nil)
	conf.Routing = &visorconfig.Routing{NoTransit: true}

	require.Equal(t, pk, confPK(conf))
	require.True(t, conf.Routing.NoTransit)
	require.True(t, confPK(nil).Null(), "a half-built config must not panic")
}

// A visor that nominates nobody and relays for nobody reports empty lists and
// the cap it would enforce if anyone attached.
func TestDmsgRelayRole_Idle(t *testing.T) {
	role := dmsgRelayRole(nil, nil, nil, 0, 4096, 0)

	require.Equal(t, skyenv.DmsgRelayPort, role.Port)
	require.Empty(t, role.RelayPeers)
	require.Empty(t, role.Attached)
	require.Empty(t, role.RelayClients)
	require.Zero(t, role.RelayedStreams)
	require.Equal(t, 4096, role.MaxRelayedStreams)
	require.Zero(t, role.RelayRefused, "an idle hub has refused nothing")
}

// Nominees are reported whether or not they were reached; only the ones with a
// live session show up as attached, with the carrier that session rides.
func TestDmsgRelayRole_AttachedNominees(t *testing.T) {
	hub, _ := cipher.GenerateKeyPair()
	unreached, _ := cipher.GenerateKeyPair()
	server, _ := cipher.GenerateKeyPair()

	role := dmsgRelayRole(
		[]cipher.PubKey{hub, unreached},
		[]dmsgSessionView{
			{PK: hub, Carrier: "skynet", Streams: 3, LatencyMS: 42},
			{PK: server, Carrier: "tcp", Streams: 9}, // a plain server, not a nominee
		}, nil, 0, 4096, 0)

	require.Len(t, role.RelayPeers, 2, "both nominees are reported")
	require.Len(t, role.Attached, 1, "only the nominee with a session is attached")
	require.Equal(t, hub, role.Attached[0].PK)
	require.Equal(t, "skynet", role.Attached[0].Carrier)
	require.Equal(t, 3, role.Attached[0].Streams)
	require.InDelta(t, 42.0, role.Attached[0].LatencyMS, 0.001)
	for _, a := range role.Attached {
		require.NotEqual(t, server, a.PK, "a plain server session is not a relay attachment")
	}
}

// The serving side: the peers attached to this visor, their streams, and the
// total charged against the configured cap.
func TestDmsgRelayRole_RelayingForPeers(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()

	role := dmsgRelayRole(nil, nil,
		map[cipher.PubKey]int{a: 2, b: 5}, 7, 4096, 3)

	require.Len(t, role.RelayClients, 2)
	require.Equal(t, 7, role.RelayedStreams)
	require.Equal(t, 4096, role.MaxRelayedStreams)
	require.Equal(t, 3, role.RelayRefused, "refusals are reported from the hub that made them")

	streams := map[cipher.PubKey]int{}
	for _, c := range role.RelayClients {
		streams[c.PK] = c.Streams
	}
	require.Equal(t, 2, streams[a])
	require.Equal(t, 5, streams[b])

	// Stable order: the snapshot is diffed tick to tick under --watch, so map
	// iteration order must never leak into it.
	require.Less(t, role.RelayClients[0].PK.String(), role.RelayClients[1].PK.String())
}

// A negative cap is the "never relay for anyone" setting, and stays visible as
// such rather than being normalized away.
func TestDmsgRelayRole_RefusesToRelay(t *testing.T) {
	role := dmsgRelayRole(nil, nil, nil, 0, -1, 0)
	require.Equal(t, -1, role.MaxRelayedStreams)
	require.Empty(t, role.RelayClients)
}

// roles is its own --select subtree: asking for it builds nothing else (the
// full snapshot is ~900 KB and a projected select must stay cheap).
func TestStateFieldSet_RolesProjection(t *testing.T) {
	set := newStateFieldSet([]string{SelectRoles})
	require.True(t, set.has(SelectRoles))
	for _, k := range []string{SelectSummary, SelectHealth, SelectRouting, SelectMux,
		SelectApps, SelectTransports, SelectModules, SelectCXO, SelectProxy, SelectDiag} {
		require.False(t, set.has(k), "--select roles must not build %q", k)
	}
	require.True(t, newStateFieldSet(nil).has(SelectRoles), "roles is in the default snapshot")
	require.Contains(t, StateSelectKeys, SelectRoles, "the key must be documented in --select help")
}
