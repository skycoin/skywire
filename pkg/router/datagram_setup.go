// Package router pkg/router/datagram_setup.go: route-setup-side
// construction of the faithful-UDP DatagramRouteGroup sibling. Stage
// 4b of #2607.
//
// A datagram route is NOT a separate route. The reliable route is set
// up exactly as usual (saveRouteGroupRules: rules + transport +
// one-time Noise KK handshake → RouteGroup in rgsNs). When BOTH ends
// locally intend datagram mode for the route, each additionally builds
// a DatagramRouteGroup over the *same* forward/reverse rules and
// transport, keyed off the reliable session's ChannelBinding — no
// second handshake. DataPackets then flow through the reliable group,
// DatagramPackets through the sibling, dispatched by packet type
// (router_packet.go).
//
// "Both ends locally intend it" (on-demand-by-local-intent, not a wire
// negotiation):
//   - dial side: DialOptions.Datagram (the DialPacket / forwarded-UDP
//     dialer sets it);
//   - accept side: RegisterDatagramPort marks the local serving port
//     (a forwarded_ports.udp port, or a packet-mode app listener).
//
// If only one side intends it, that side's sibling exists but the peer
// drops its DatagramPackets — no reliable traffic is affected.
package router

import (
	"net"

	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// channelBinder is satisfied by the Noise-encrypted conn returned by
// network.EncryptConn (concrete *noise.Conn). It exposes the completed
// handshake's channel binding so the datagram sibling can key off the
// reliable session without a second handshake.
type channelBinder interface {
	ChannelBinding() []byte
}

// RegisterDatagramPort marks a local port as faithful-UDP-capable, so
// the accept side builds a DatagramRouteGroup sibling for routes whose
// local (destination) port is this one. Idempotent. Call when binding
// a forwarded_ports.udp port or a packet-mode app listener; pair with
// UnregisterDatagramPort on teardown.
func (r *router) RegisterDatagramPort(port routing.Port) {
	r.mx.Lock()
	defer r.mx.Unlock()
	r.datagramPorts[port] = struct{}{}
}

// UnregisterDatagramPort clears a port's faithful-UDP intent.
func (r *router) UnregisterDatagramPort(port routing.Port) {
	r.mx.Lock()
	defer r.mx.Unlock()
	delete(r.datagramPorts, port)
}

// isDatagramPort reports whether port carries faithful-UDP intent.
func (r *router) isDatagramPort(port routing.Port) bool {
	r.mx.Lock()
	defer r.mx.Unlock()
	_, ok := r.datagramPorts[port]
	return ok
}

// closeDatagramSibling closes + de-registers the datagram route group for
// desc, if one exists. The sibling's lifetime is coupled to the reliable
// route's: it has no keep-alive of its own (a receive-only datagram group
// never writes, so it can't detect a dead route), so it must be reaped
// whenever the reliable route for the same descriptor is torn down — the
// close-packet path and the GC. Safe to call when no sibling exists.
func (r *router) closeDatagramSibling(desc routing.RouteDescriptor) {
	r.mx.Lock()
	dg, ok := r.rgsDatagrams[desc]
	if ok {
		delete(r.rgsDatagrams, desc)
	}
	r.mx.Unlock()
	if ok && dg != nil {
		dg.Close() //nolint:errcheck,gosec // idempotent, always returns nil
	}
}

// buildDatagramSibling constructs + registers a DatagramRouteGroup over
// the same rules/transport as the just-established reliable route,
// keyed off the reliable session's ChannelBinding. Best-effort: any
// failure (no channel binding because the route is unencrypted, key
// derivation, cipher construction) logs and returns without a sibling —
// the reliable route is unaffected, and a missing sibling just means
// datagrams for this route are dropped (faithful-UDP loss tolerance).
//
// wrapped is the encrypted conn from network.EncryptConn (nrg.Conn on
// the encrypt path). tp is the first-hop transport for the route.
func (r *router) buildDatagramSibling(rules routing.EdgeRules, nsConf noise.Config, wrapped net.Conn, tp *transport.ManagedTransport) {
	binder, ok := wrapped.(channelBinder)
	if !ok {
		// Unencrypted route (legacy/no-noise) — the datagram AEAD has
		// no key to derive. Modern visors always encrypt, so this is a
		// genuine skip, not a downgrade.
		r.logger.WithField("desc", rules.Desc.String()).
			Warn("datagram sibling requested but route is not noise-encrypted; skipping")
		return
	}
	master, err := deriveDatagramMasterKey(binder.ChannelBinding())
	if err != nil {
		r.logger.WithError(err).WithField("desc", rules.Desc.String()).
			Warn("datagram sibling: master-key derivation failed; skipping")
		return
	}

	// Direction keying: my outbound cipher uses my role's subkey and
	// binds AAD to the receiver (the remote); my inbound cipher uses
	// the peer's role subkey and binds AAD to the receiver (me). See
	// datagram_keys_test.go for the round-trip invariant.
	myRole, peerRole := RoleResponder, RoleInitiator
	if nsConf.Initiator {
		myRole, peerRole = RoleInitiator, RoleResponder
	}
	outCipher, err := NewDatagramCipher(master, myRole, nsConf.RemotePK, nil)
	if err != nil {
		r.logger.WithError(err).WithField("desc", rules.Desc.String()).
			Warn("datagram sibling: out-cipher construction failed; skipping")
		return
	}
	inCipher, err := NewDatagramCipher(master, peerRole, nsConf.LocalPK, nil)
	if err != nil {
		r.logger.WithError(err).WithField("desc", rules.Desc.String()).
			Warn("datagram sibling: in-cipher construction failed; skipping")
		return
	}

	dg := NewDatagramRouteGroup(DefaultRouteGroupConfig(), r.rt, rules.Desc, r.mLogger)
	dg.AddRule(rules.Forward, tp, rules.Reverse)
	dg.SetCiphers(outCipher, inCipher)
	r.setDatagramRouteGroup(rules.Desc, dg)

	r.logger.WithField("desc", rules.Desc.String()).
		Debugf("datagram sibling registered (role=%d)", myRole)
}
