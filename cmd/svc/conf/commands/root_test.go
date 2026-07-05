// Package commands cmd/svc/conf/commands/root_test.go: unit tests for the conf
// root command — subcommand registration / metadata, the scheme filtering
// (filterServices), the JSON-printing helpers and the three subcommand Run
// closures (output captured from stdout), and Execute's --help path.
package commands

import (
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/deployment"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

// sampleServices is a fully-populated Services value so filterServices can be
// asserted independently of the embedded deployment config content.
func sampleServices() deployment.Services {
	return deployment.Services{
		DmsgDiscovery:          "http://dmsgd",
		TransportDiscovery:     "http://tpd",
		AddressResolver:        "http://ar",
		RouteFinder:            "http://rf",
		UptimeTracker:          "http://ut",
		ServiceDiscovery:       "http://sd",
		GeoIP:                  "http://geo",
		RewardSystem:           "http://reward",
		StunServers:            []string{"stun:3478"},
		ConfDmsg:               "dmsg://conf",
		DmsgServers:            []deployment.DmsgServerEntry{{}},
		DmsgDiscoveryDmsg:      "dmsg://dmsgd",
		TransportDiscoveryDmsg: "dmsg://tpd",
		AddressResolverDmsg:    "dmsg://ar",
		RouteFinderDmsg:        "dmsg://rf",
		UptimeTrackerDmsg:      "dmsg://ut",
		ServiceDiscoveryDmsg:   "dmsg://sd",
		RewardSystemDmsg:       "dmsg://reward",
	}
}

// ---- filterServices --------------------------------------------------------

func TestFilterServices_HTTPOnly(t *testing.T) {
	out := filterServices(sampleServices(), true, false)

	// HTTP fields kept.
	require.Equal(t, "http://dmsgd", out.DmsgDiscovery)
	require.Equal(t, "http://geo", out.GeoIP)
	require.Equal(t, "http://reward", out.RewardSystem)
	// DMSG fields stripped.
	require.Empty(t, out.ConfDmsg)
	require.Nil(t, out.DmsgServers)
	require.Empty(t, out.DmsgDiscoveryDmsg)
	require.Empty(t, out.RewardSystemDmsg)
	// Scheme-neutral fields kept.
	require.Equal(t, []string{"stun:3478"}, out.StunServers)
}

func TestFilterServices_DmsgOnly(t *testing.T) {
	out := filterServices(sampleServices(), false, true)

	// HTTP fields stripped.
	require.Empty(t, out.DmsgDiscovery)
	require.Empty(t, out.GeoIP)
	require.Empty(t, out.RewardSystem)
	// DMSG fields kept.
	require.Equal(t, "dmsg://conf", out.ConfDmsg)
	require.Equal(t, "dmsg://dmsgd", out.DmsgDiscoveryDmsg)
	require.Len(t, out.DmsgServers, 1)
	// Scheme-neutral fields kept.
	require.Equal(t, []string{"stun:3478"}, out.StunServers)
}

func TestFilterServices_KeepBoth(t *testing.T) {
	in := sampleServices()
	out := filterServices(in, true, true)
	require.Equal(t, in, out) // nothing stripped
}

// ---- printFilteredJSON / subcommand Run closures ---------------------------

func TestPrintFilteredJSON_ValidWrapper(t *testing.T) {
	out := captureStdout(t, func() { printFilteredJSON(true, false) })

	var wrapper struct {
		Prod deployment.Services `json:"prod"`
		Test deployment.Services `json:"test"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &wrapper))
	// dmsg fields stripped in the HTTP-only view.
	require.Empty(t, wrapper.Prod.ConfDmsg)
}

func TestServicesConfCmd_Run(t *testing.T) {
	out := captureStdout(t, func() { ServicesConfCmd.Run(ServicesConfCmd, nil) })
	require.Equal(t, string(deployment.ServicesJSON)+"\n", out)
}

func TestDmsghttpConfCmd_Run(t *testing.T) {
	out := captureStdout(t, func() { DmsghttpConfCmd.Run(DmsghttpConfCmd, nil) })
	require.True(t, json.Valid([]byte(out)))
}

func TestHTTPConfCmd_Run(t *testing.T) {
	out := captureStdout(t, func() { HTTPConfCmd.Run(HTTPConfCmd, nil) })
	require.True(t, json.Valid([]byte(out)))
}

// ---- metadata / registration / Execute -------------------------------------

func TestRootCmd_Metadata(t *testing.T) {
	require.Equal(t, "conf", RootCmd.Use)
	require.Equal(t, "print json config files", RootCmd.Short)

	got := map[string]bool{}
	for _, c := range RootCmd.Commands() {
		got[c.Use] = true
	}
	for _, name := range []string{"svcconf", "dmsgconf", "httpconf"} {
		require.True(t, got[name], "subcommand %q should be registered", name)
	}
}

func TestExecute_Help(t *testing.T) {
	defer RootCmd.SetArgs(nil)
	RootCmd.SetArgs([]string{"--help"})
	RootCmd.SetOut(os.NewFile(0, os.DevNull))
	require.NotPanics(t, Execute)
}
