package router

import (
	"context"
	"testing"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

type stubDialHook struct{}

func (stubDialHook) BeforeDial(_ context.Context, _ DialInfo) (DialAdjustment, error) {
	return DialAdjustment{}, nil
}

// TestEffectiveDialHook: control-plane ports bypass the policy by default, opt
// back in via PolicyOnControlPorts; data ports always get the hook; a nil hook
// is always nil.
func TestEffectiveDialHook(t *testing.T) {
	hook := stubDialHook{}
	cases := []struct {
		name      string
		dialHook  DialHook
		onControl bool
		port      uint16
		wantHook  bool
	}{
		{"no hook configured", nil, false, skyenv.DmsgPtyPort, false},
		{"control port default-exempt", hook, false, skyenv.DmsgPtyPort, false},
		{"control port opted-in", hook, true, skyenv.DmsgPtyPort, true},
		{"data port always hooked", hook, false, 3, true},
		{"data port opted-in still hooked", hook, true, 3, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &router{conf: &Config{DialHook: c.dialHook, PolicyOnControlPorts: c.onControl}}
			if got := r.effectiveDialHook(routing.Port(c.port)); (got != nil) != c.wantHook {
				t.Errorf("effectiveDialHook(port=%d, onControl=%v): got non-nil=%v, want %v",
					c.port, c.onControl, got != nil, c.wantHook)
			}
		})
	}
}

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
