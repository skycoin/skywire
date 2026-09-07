// Package cliconfig cmd/skywire-cli/commands/config/update_endpoints_test.go c4-vis-cli
package cliconfig

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// saveEndpointFetchState snapshots the package-level state the fetch/apply
// helpers read and restores it when the test ends.
func saveEndpointFetchState(t *testing.T) {
	t.Helper()
	oldServices, oldNoFetch, oldDmsgHTTP := services, noFetch, isDmsgHTTP
	oldPath, oldURL, oldTest, oldStdout := configServicePath, serviceConfURL, isTestEnv, isStdout
	t.Cleanup(func() {
		services, noFetch, isDmsgHTTP = oldServices, oldNoFetch, oldDmsgHTTP
		configServicePath, serviceConfURL, isTestEnv, isStdout = oldPath, oldURL, oldTest, oldStdout
	})
}

// TestFetchServiceConfigFallsBackToEmbedded asserts that the "falling back on
// embedded config" log line corresponds to an actual fallback: `services` must
// come out populated. The old visorconfig.Fetch logged that message and then
// returned a nil *Services, which segfaulted its caller.
func TestFetchServiceConfigFallsBackToEmbedded(t *testing.T) {
	saveEndpointFetchState(t)

	services = visorconfig.Services{}
	noFetch, isDmsgHTTP, isStdout = true, false, false
	configServicePath = ""
	isTestEnv = false

	fetchServiceConfig(logging.MustGetLogger("test"))

	require.True(t, services.HasDmsgEndpoints(),
		"embedded fallback must populate the dmsg service endpoints")
	require.NotEmpty(t, services.RouteFinderDmsg)
	require.NotEmpty(t, services.AddressResolverDmsg)
}

// TestApplyServiceEndpointsNoPanicOnEmptyFetch is the direct regression for the
// nil-dereference at update.go:128: a fetch that yields nothing must leave the
// config untouched rather than crash the CLI.
func TestApplyServiceEndpointsNoPanicOnEmptyFetch(t *testing.T) {
	saveEndpointFetchState(t)
	services = visorconfig.Services{}

	// Every endpoint-holding sub-struct nil — the worst case for a config the
	// user hands us.
	require.NotPanics(t, func() { applyServiceEndpoints(&visorconfig.V1{}) })

	conf := &visorconfig.V1{
		Dmsg:      &dmsgc.DmsgConfig{Discovery: "dmsg://keep-me:80", SessionsCount: 3},
		Transport: &visorconfig.Transport{Discovery: "dmsg://keep-tpd:80", StcprPort: 7777},
		Routing:   &visorconfig.Routing{RouteFinder: "dmsg://keep-rf:80", MinHops: 2},
		Launcher:  &visorconfig.Launcher{ServiceDisc: "dmsg://keep-sd:80", BinPath: "/apps"},
	}
	require.NotPanics(t, func() { applyServiceEndpoints(conf) })

	require.Equal(t, "dmsg://keep-me:80", conf.Dmsg.Discovery)
	require.Equal(t, "dmsg://keep-tpd:80", conf.Transport.Discovery)
	require.Equal(t, "dmsg://keep-rf:80", conf.Routing.RouteFinder)
	require.Equal(t, "dmsg://keep-sd:80", conf.Launcher.ServiceDisc)
}

// TestApplyServiceEndpointsPreservesUnrelatedState checks that -a updates the
// endpoints and nothing else. The previous implementation replaced conf.Dmsg /
// conf.Transport / conf.Routing / conf.Launcher wholesale, silently discarding
// launcher apps, transport ports, dmsg session count and min-hops.
func TestApplyServiceEndpointsPreservesUnrelatedState(t *testing.T) {
	saveEndpointFetchState(t)
	services = deployment.Prod

	conf := &visorconfig.V1{
		Dmsg: &dmsgc.DmsgConfig{
			Discovery:     "dmsg://stale:80",
			SessionsCount: 4,
			Protocol:      "yamux",
			Carriers:      []string{"tcp"},
		},
		Transport: &visorconfig.Transport{
			Discovery:       "dmsg://stale:80",
			AddressResolver: "dmsg://stale:80",
			StcprPort:       7070,
			SudphPort:       7071,
			LogStore:        &visorconfig.LogStore{Type: visorconfig.MemoryLogStore},
		},
		Routing: &visorconfig.Routing{
			RouteFinder: "dmsg://stale:80",
			MinHops:     3,
		},
		Launcher: &visorconfig.Launcher{
			ServiceDisc: "dmsg://stale:80",
			BinPath:     "/opt/skywire/apps",
		},
	}

	launcher := conf.Launcher

	applyServiceEndpoints(conf)

	// Endpoints refreshed from the deployment...
	require.Equal(t, deployment.Prod.DmsgDiscoveryDmsg, conf.Dmsg.Discovery)
	require.Equal(t, deployment.Prod.TransportDiscoveryDmsg, conf.Transport.Discovery)
	require.Equal(t, deployment.Prod.AddressResolverDmsg, conf.Transport.AddressResolver)
	require.Equal(t, deployment.Prod.RouteFinderDmsg, conf.Routing.RouteFinder)
	require.Equal(t, deployment.Prod.ServiceDiscoveryDmsg, conf.Launcher.ServiceDisc)
	require.NotEmpty(t, conf.Dmsg.Servers)

	// ...everything else left alone.
	require.Equal(t, 4, conf.Dmsg.SessionsCount)
	require.Equal(t, "yamux", conf.Dmsg.Protocol)
	require.Equal(t, []string{"tcp"}, conf.Dmsg.Carriers)
	require.Equal(t, 7070, conf.Transport.StcprPort)
	require.Equal(t, 7071, conf.Transport.SudphPort)
	require.NotNil(t, conf.Transport.LogStore)
	require.Equal(t, uint16(3), conf.Routing.MinHops)
	require.Equal(t, "/opt/skywire/apps", conf.Launcher.BinPath)
	require.Same(t, launcher, conf.Launcher, "launcher struct must be mutated in place, not replaced")

	// No uptime_tracker block is invented for a config that lacks one.
	require.Nil(t, conf.UptimeTracker)
}
