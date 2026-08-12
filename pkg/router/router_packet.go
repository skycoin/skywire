// Package router pkg/router/router_packet.go c2-net-routing
// router_packet.go contains all packet handling logic.
package router

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/skycoin/skywire/pkg/routing"
)

var (
	errRouteDescNotExist    = errors.New("route descriptor does not exist")
	errNilNoiseRG           = errors.New("noiseRouteGroup is nil")
	errNilInitRG            = errors.New("initializing RouteGroup is nil")
	errMalformedClosePacket = errors.New("close packet has empty payload")
)

// closeCodeFromPacket safely extracts the close code from a ClosePacket. A
// remote peer fully controls the on-wire payload length (managedTransport reads
// exactly the declared size), so a malformed/zero-length close payload must not
// be indexed blindly — doing so panics the router read loop, which has no
// recover and would take down all routing on the visor.
func closeCodeFromPacket(packet routing.Packet) (routing.CloseCode, error) {
	payload := packet.Payload()
	if len(payload) == 0 {
		return 0, errMalformedClosePacket
	}
	return routing.CloseCode(payload[0]), nil
}

func (r *router) handleTransportPacket(ctx context.Context, packet routing.Packet) error {
	switch packet.Type() {
	case routing.DataPacket, routing.HandshakePacket:
		return r.handleDataHandshakePacket(ctx, packet)
	case routing.ClosePacket:
		return r.handleClosePacket(ctx, packet)
	case routing.KeepAlivePacket:
		return r.handleKeepAlivePacket(ctx, packet)
	case routing.PingPacket:
		return r.dispatchToRouteGroup(ctx, packet)
	case routing.PongPacket:
		return r.dispatchToRouteGroup(ctx, packet)
	case routing.ErrorPacket:
		return r.dispatchToRouteGroup(ctx, packet)
	case routing.SACKPacket:
		return r.handleSACKRouterPacket(ctx, packet)
	case routing.DatagramPacket:
		return r.handleDatagramPacket(ctx, packet)
	case routing.TransportPingPacket, routing.TransportPongPacket,
		routing.CascadeSetupPacket, routing.CascadeAckPacket, routing.DHTPacket,
		routing.SetupRPCPacket, routing.VisorRPCPacket:
		// These should be intercepted at the transport layer (ManagedTransport.readLoop).
		// If they reach the router, something is wrong — drop silently.
		r.logger.Warn("Control-plane packet reached router (should be handled at transport layer)")
		return nil
	default:
		return ErrUnknownPacketType
	}
}

// dispatchToRouteGroup is the common handler for packets that follow the pattern:
// get rule → forward if intermediary → look up route group → handle packet.
// Used by ping, pong, error, and similar packet types.
func (r *router) dispatchToRouteGroup(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		// Surface the offending route ID + packet type. A bare "rule not
		// found" is useless for diagnosing multihop setup failures where a
		// handshake/data frame arrives stamped with a route ID this visor
		// never installed a rule for (e.g. a mis-stitched reverse chain).
		return fmt.Errorf("%w (routeID=%d, pktType=%s)", err, packet.RouteID(), packet.Type())
	}

	// Forward/intermediary rules get forwarded
	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		return r.forwardPacket(ctx, packet, rule)
	}

	// Look up the route group for this descriptor
	desc := rule.RouteDescriptor()

	// Try noise-wrapped route group first (fully initialized)
	if nrg, ok := r.noiseRouteGroup(desc); ok {
		if nrg == nil {
			return errNilNoiseRG
		}
		return nrg.handlePacket(packet)
	}

	// Try raw route group (still initializing, e.g., handshake in progress)
	if rg, ok := r.initializingRouteGroup(desc); ok {
		if rg == nil {
			return errNilInitRG
		}
		return rg.handlePacket(packet)
	}

	// The rule resolved but no route group is registered yet: this frame
	// arrived in the rule-save -> route-group-register window (the receive-side
	// setup race). Park it and re-dispatch on registration instead of dropping
	// it, which would otherwise stall the noise handshake. See router_pending.go.
	if r.pending.park(desc, packet, time.Now()) {
		return nil
	}
	return errRouteDescNotExist
}

// handleDataHandshakePacket handles data and handshake packets.
// Same logic as dispatchToRouteGroup but kept separate for the
// distinct code path in handleTransportPacket.
func (r *router) handleDataHandshakePacket(ctx context.Context, packet routing.Packet) error {
	return r.dispatchToRouteGroup(ctx, packet)
}

// handleDatagramPacket dispatches a faithful-UDP DatagramPacket (#2607). It
// mirrors dispatchToRouteGroup but routes to the datagram route-group map and,
// critically, does NOT park frames for an as-yet-unregistered descriptor: a
// datagram is loss-tolerant by definition, so a frame that races route-group
// registration is simply dropped rather than buffered. An intermediary rule
// forwards the datagram to the next hop unchanged.
func (r *router) handleDatagramPacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return fmt.Errorf("%w (routeID=%d, pktType=%s)", err, packet.RouteID(), packet.Type())
	}

	// Forward/intermediary rules relay the datagram to the next hop.
	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		return r.forwardPacket(ctx, packet, rule)
	}

	// Consume rule: deliver to the local datagram route group.
	desc := rule.RouteDescriptor()
	dg, ok := r.datagramRouteGroup(desc)
	if !ok || dg == nil {
		// No group registered (yet, or already torn down). Drop — faithful
		// UDP tolerates loss; parking/retransmit would violate the semantics.
		r.logger.WithField("desc", desc.String()).
			Debug("DatagramPacket for unregistered route group, dropping")
		return nil
	}

	if err := dg.Handle(packet); err != nil {
		// A closed group means the route should be torn down; de-register so
		// subsequent datagrams take the fast drop path above.
		if errors.Is(err, io.ErrClosedPipe) {
			r.removeDatagramRouteGroup(desc)
			return nil
		}
		return err
	}
	return nil
}

func (r *router) handleClosePacket(ctx context.Context, packet routing.Packet) error {
	routeID := packet.RouteID()

	rule, err := r.GetRule(routeID)
	if err != nil {
		return err
	}

	if t := rule.Type(); t == routing.RuleIntermediary {
		// Intermediary: reclaim this hop's rule and forward the close onward.
		// Identical for CloseRequested and CloseLegRetired — a transited relay
		// tears down the leg's rule either way, which is exactly the reclamation
		// CloseLegRetired exists to trigger without waiting out the idle GC.
		defer func() {
			r.rt.DelRules([]routing.RouteID{routeID})
		}()
		return r.forwardPacket(ctx, packet, rule)
	}

	// Endpoint (consume rule).
	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)
	if !ok {
		r.rt.DelRules([]routing.RouteID{routeID})
		return errRouteDescNotExist
	}
	if nrg == nil {
		r.rt.DelRules([]routing.RouteID{routeID})
		r.removeNoiseRouteGroup(desc)
		return errNilNoiseRG
	}

	closeCode, err := closeCodeFromPacket(packet)
	if err != nil {
		r.rt.DelRules([]routing.RouteID{routeID})
		return err
	}

	// CloseLegRetired: a remote peer retired ONE leg of a mux group. Prune only
	// the leg whose consume rule this is and keep the group (and its other legs)
	// live. pruneLegByConsumeRule itself reclaims the leg's forward+consume
	// rules, so we do NOT run the whole-group DelRules / removeNoiseRouteGroup
	// teardown here. If it returns false (this was the group's last leg) fall
	// through to a normal full close.
	if closeCode == routing.CloseLegRetired {
		if nrg.pruneLegByConsumeRule(routeID) {
			return nil
		}
	}

	defer func() {
		r.rt.DelRules([]routing.RouteID{routeID})
	}()
	defer r.removeNoiseRouteGroup(desc)
	// Reap the faithful-UDP sibling (if any) with the reliable route (#2607).
	defer r.closeDatagramSibling(desc)

	if nrg.isClosed() {
		return io.ErrClosedPipe
	}

	if err := nrg.handlePacket(packet); err != nil {
		return fmt.Errorf("error handling close packet with code %d by noise route group with descriptor %s: %v",
			closeCode, &desc, err)
	}

	return nil
}

func (r *router) handleKeepAlivePacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}

	// Propagate only for intermediary rules
	if t := rule.Type(); t == routing.RuleIntermediary {
		return r.forwardPacket(ctx, packet, rule)
	}

	// Consume rule — activity already updated by GetRule
	return nil
}

func (r *router) handleSACKRouterPacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}

	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		return r.forwardPacket(ctx, packet, rule)
	}

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)
	if !ok || nrg == nil {
		return nil // SACK for unknown route, ignore silently
	}

	return nrg.handlePacket(packet)
}

// GetRule fetches a rule and updates its activity.
func (r *router) GetRule(routeID routing.RouteID) (routing.Rule, error) {
	rule, err := r.rt.Rule(routeID)
	if err != nil {
		return nil, fmt.Errorf("routing table: %w", err)
	}

	if rule == nil {
		return nil, errors.New("unknown route ID")
	}

	// update the rule activity
	if err := r.rt.UpdateActivity(routeID); err != nil {
		return nil, fmt.Errorf("error updating activity of rule %d: %w", routeID, err)
	}

	return rule, nil
}

// UpdateRuleActivity updates the activity of a rule.
func (r *router) UpdateRuleActivity(routeID routing.RouteID) error {
	return r.rt.UpdateActivity(routeID)
}

// forwardPacket reconstructs a packet with the next-hop route ID and writes it
// to the appropriate transport.
func (r *router) forwardPacket(ctx context.Context, packet routing.Packet, rule routing.Rule) error {
	tp := r.tm.Transport(rule.NextTransportID())
	if tp == nil {
		return fmt.Errorf("transport %s not found for next-hop routing", rule.NextTransportID())
	}

	var p routing.Packet

	switch packet.Type() {
	case routing.DataPacket:
		var err error
		p, err = routing.MakeDataPacket(rule.NextRouteID(), packet.Payload())
		if err != nil {
			return err
		}
	case routing.HandshakePacket:
		p = routing.MakeHandshakePacketRaw(rule.NextRouteID(), packet.Payload())
	case routing.KeepAlivePacket:
		p = routing.MakeKeepAlivePacket(rule.NextRouteID())
	case routing.ClosePacket:
		closeCode, err := closeCodeFromPacket(packet)
		if err != nil {
			return err
		}
		p = routing.MakeClosePacket(rule.NextRouteID(), closeCode)
	case routing.PingPacket:
		timestamp := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset:]))    //nolint:gosec
		throughput := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset+8:])) //nolint:gosec
		p = routing.MakePingPacket(rule.NextRouteID(), timestamp, throughput)
	case routing.PongPacket:
		timestamp := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset:])) //nolint:gosec
		p = routing.MakePongPacket(rule.NextRouteID(), timestamp)
	case routing.ErrorPacket:
		var err error
		p, err = routing.MakeErrorPacket(rule.NextRouteID(), packet.Payload())
		if err != nil {
			return err
		}
	case routing.SACKPacket:
		p = routing.MakeSACKPacket(rule.NextRouteID(), packet.SACKLastContiguousSeq(), packet.SACKBitmap())
	case routing.DatagramPacket:
		// Faithful-UDP relay (#2607): re-stamp the next-hop route ID and pass
		// the opaque (AEAD-sealed) payload through unchanged — intermediaries
		// never decrypt, the seal is end-to-end.
		var err error
		p, err = routing.MakeDatagramPacket(rule.NextRouteID(), packet.Payload())
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("packet of type %s can't be forwarded", packet.Type())
	}

	if err := tp.WritePacket(ctx, p); err != nil {
		return err
	}

	if err := r.UpdateRuleActivity(rule.KeyRouteID()); err != nil {
		r.logger.Errorf("Failed to update activity for rule with route ID %d: %v", rule.KeyRouteID(), err)
	}

	return nil
}
