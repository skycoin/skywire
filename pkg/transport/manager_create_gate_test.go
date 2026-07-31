// Package transport pkg/transport/manager_create_gate_test.go c1-net-transport
package transport

import (
	"testing"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestTransportCreateGate exercises the transport-creation policy predicate that
// saveTransportInternal enforces: NoDirectTransports blocks direct p2p types but
// exempts dmsg, and TransportCreateDeny blocks exactly the listed types
// (including dmsg), alias-aware.
func TestTransportCreateGate(t *testing.T) {
	directTypes := []types.Type{types.STCPR, types.SUDPH, types.STCP, types.QUIC, types.WS, types.WT, types.WEBRTC}

	t.Run("default allows everything", func(t *testing.T) {
		tm := &Manager{Conf: &ManagerConfig{}}
		for _, ty := range types.Known() {
			if !tm.CanCreateTransport(ty) {
				t.Errorf("default policy must allow %q", ty)
			}
		}
	})

	t.Run("NoDirectTransports blocks direct, exempts dmsg", func(t *testing.T) {
		tm := &Manager{Conf: &ManagerConfig{NoDirectTransports: true}}
		for _, ty := range directTypes {
			if tm.CanCreateTransport(ty) {
				t.Errorf("NoDirectTransports must block direct type %q", ty)
			}
		}
		if !tm.CanCreateTransport(types.DMSG) {
			t.Error("NoDirectTransports must still allow dmsg (relay, not direct p2p)")
		}
		// Alias-aware: legacy "quic"/"ws"/"wt" are direct and blocked too.
		if tm.CanCreateTransport(types.QUICLegacy) || tm.CanCreateTransport(types.WSLegacy) {
			t.Error("NoDirectTransports must block legacy direct aliases")
		}
	})

	t.Run("TransportCreateDeny blocks listed types including dmsg", func(t *testing.T) {
		tm := &Manager{Conf: &ManagerConfig{TransportCreateDeny: []types.Type{types.DMSG, types.SUDPH}}}
		if tm.CanCreateTransport(types.DMSG) {
			t.Error("explicit deny must block dmsg (the advanced/testing case)")
		}
		if tm.CanCreateTransport(types.SUDPH) {
			t.Error("explicit deny must block sudph")
		}
		// Not listed → still allowed.
		if !tm.CanCreateTransport(types.STCPR) {
			t.Error("stcpr not in deny list must remain allowed")
		}
	})

	t.Run("deny list is alias-aware", func(t *testing.T) {
		// A deny entry given as the legacy alias must block the canonical type.
		tm := &Manager{Conf: &ManagerConfig{TransportCreateDeny: []types.Type{types.QUICLegacy}}}
		if tm.CanCreateTransport(types.QUIC) {
			t.Error("deny of legacy \"quic\" must block canonical squicr")
		}
	})
}
