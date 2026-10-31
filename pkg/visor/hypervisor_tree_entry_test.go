package visor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// A visor behind a sub-hypervisor keeps its dmsg servers, running apps and
// public flags through the HVVisorEntry round trip, so the node list can
// fill its DMSG servers and services columns for it.
func TestEntryRoundTripKeepsServiceColumns(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	srv, _ := cipher.GenerateKeyPair()

	running := &appserver.AppState{Status: appserver.AppStatusRunning}
	running.Name = "skysocks"
	stopped := &appserver.AppState{Status: appserver.AppStatusStopped}
	stopped.Name = "vpn-server"

	sum := &visorapi.Summary{
		Overview: &visorapi.Overview{
			PubKey:    pk,
			BuildInfo: &buildinfo.Info{Version: "v1.3.104"},
			Apps:      []*appserver.AppState{running, stopped},
		},
		DMSGServers:       []visorapi.DMSGServerInfo{{PK: srv, Latency: 20 * time.Millisecond, Protocol: "tcp"}},
		PublicAutoconnect: true,
		IsPublic:          true,
	}

	entry := visorapi.HVVisorEntry{PK: pk, Online: true}
	populateEntryFromSummary(&entry, sum)
	require.Equal(t, []string{"skysocks"}, entry.RunningApps)

	got := projectEntryToSummary(entry)
	require.Equal(t, sum.DMSGServers, got.DMSGServers)
	require.True(t, got.PublicAutoconnect)
	require.True(t, got.IsPublic)
	require.Len(t, got.Overview.Apps, 1)
	require.Equal(t, "skysocks", got.Overview.Apps[0].Name)
	require.Equal(t, appserver.AppStatusRunning, got.Overview.Apps[0].Status)
}
