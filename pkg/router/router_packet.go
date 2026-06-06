// router_packet.go contains all packet handling logic.
package router

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

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

	return errRouteDescNotExist
}

// handleDataHandshakePacket handles data and handshake packets.
// Same logic as dispatchToRouteGroup but kept separate for the
// distinct code path in handleTransportPacket.
func (r *router) handleDataHandshakePacket(ctx context.Context, packet routing.Packet) error {
	return r.dispatchToRouteGroup(ctx, packet)
}

func (r *router) handleClosePacket(ctx context.Context, packet routing.Packet) error {
	routeID := packet.RouteID()

	rule, err := r.GetRule(routeID)
	if err != nil {
		return err
	}

	defer func() {
		r.rt.DelRules([]routing.RouteID{routeID})
	}()

	if t := rule.Type(); t == routing.RuleIntermediary {
		return r.forwardPacket(ctx, packet, rule)
	}

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)
	if !ok {
		return errRouteDescNotExist
	}

	defer r.removeNoiseRouteGroup(desc)

	if nrg == nil {
		return errNilNoiseRG
	}

	closeCode, err := closeCodeFromPacket(packet)
	if err != nil {
		return err
	}

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
