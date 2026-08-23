package router

import (
	"testing"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// TestIsControlPlanePort locks the no-mux port set: control/telemetry ports are
// control-plane (single-route), bulk-data app ports are not (keep their mux).
func TestIsControlPlanePort(t *testing.T) {
	control := []uint16{
		skyenv.DmsgPtyPort, skyenv.DmsgCtrlPort, skyenv.DmsgPingPort,
		skyenv.DmsgSetupPort, skyenv.DmsgHypervisorPort, skyenv.DmsgGRPCPort,
		skyenv.DmsgVisorRPCPort, skyenv.DmsgTransportSetupPort,
	}
	for _, p := range control {
		if !isControlPlanePort(routing.Port(p)) {
			t.Errorf("port %d should be control-plane (no mux)", p)
		}
	}
	// Bulk-data app ports MUST keep their mux (not control-plane).
	dataPorts := []uint16{
		1,  // skychat
		3,  // skysocks
		13, // skysocks-client
		43, // vpn-client
		44, // vpn-server
		59, // skynet-client forward pool (bulk mux carry)
	}
	for _, p := range dataPorts {
		if isControlPlanePort(routing.Port(p)) {
			t.Errorf("data port %d must NOT be treated as control-plane (would kill its mux)", p)
		}
	}
}
