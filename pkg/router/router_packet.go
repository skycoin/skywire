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

func (r *router) handleTransportPacket(ctx context.Context, packet routing.Packet) error {
	switch packet.Type() {
	case routing.DataPacket, routing.HandshakePacket:
		return r.handleDataHandshakePacket(ctx, packet)
	case routing.ClosePacket:
		return r.handleClosePacket(ctx, packet)
	case routing.KeepAlivePacket:
		return r.handleKeepAlivePacket(ctx, packet)
	case routing.PingPacket:
		return r.handlePingPacket(ctx, packet)
	case routing.PongPacket:
		return r.handlePongPacket(ctx, packet)
	case routing.ErrorPacket:
		return r.handleErrorPacket(ctx, packet)
	case routing.SACKPacket:
		return r.handleSACKRouterPacket(ctx, packet)
	default:
		return ErrUnknownPacketType
	}
}

func (r *router) handleDataHandshakePacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}
	log := r.logger.WithField("func", "router.handleDataHandshakePacket")
	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
		return r.forwardPacket(ctx, packet, rule)
	}

	log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)

	log.Tracef("Handling packet with descriptor %s", &desc)

	if ok {
		if nrg == nil {
			return errors.New("noiseRouteGroup is nil")
		}

		// in this case we have already initialized nrg and may use it straightforward
		log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
			len(packet.Payload()), packet.RouteID(), rule)

		return nrg.handlePacket(packet)
	}

	// we don't have nrg for this packet. it's either handshake message or
	// we don't have route for this one completely

	rg, ok := r.initializingRouteGroup(desc)
	if !ok {
		// no route, just return error
		log.Tracef("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	if rg == nil {
		return errors.New("initializing RouteGroup is nil")
	}

	// handshake packet, handling with the raw rg
	log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	return rg.handlePacket(packet)
}

func (r *router) handleClosePacket(ctx context.Context, packet routing.Packet) error {
	routeID := packet.RouteID()

	log := r.logger.WithField("func", "router.handleClosePacket")
	log.Tracef("Received close packet for route ID %v", routeID)

	rule, err := r.GetRule(routeID)
	if err != nil {
		return err
	}

	if rule.Type() == routing.RuleReverse {
		log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())
	} else {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
	}

	defer func() {
		routeIDs := []routing.RouteID{routeID}
		r.rt.DelRules(routeIDs)
	}()

	if t := rule.Type(); t == routing.RuleIntermediary {
		log.Traceln("Handling intermediary close packet")
		return r.forwardPacket(ctx, packet, rule)
	}

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)

	log.Tracef("Handling close packet with descriptor %s", &desc)

	if !ok {
		log.Tracef("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	defer r.removeNoiseRouteGroup(desc)

	if nrg == nil {
		return errors.New("noiseRouteGroup is nil")
	}

	log.Tracef("Got new remote close packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	closeCode := routing.CloseCode(packet.Payload()[0])

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
	routeID := packet.RouteID()

	log := r.logger.WithField("func", "router.handleKeepAlivePacket")
	log.Tracef("Received keepalive packet for route ID %v", routeID)

	rule, err := r.GetRule(routeID)
	if err != nil {
		return err
	}

	if rule.Type() == routing.RuleReverse {
		log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())
	} else {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
	}

	// propagate packet only for intermediary rule. forward rule workflow doesn't get here,
	// consume rules should be omitted, activity is already updated
	if t := rule.Type(); t == routing.RuleIntermediary {
		log.Traceln("Handling intermediary keep-alive packet")
		return r.forwardPacket(ctx, packet, rule)
	}

	log.Tracef("Route ID %v found, updated activity", routeID)

	return nil
}

func (r *router) handlePingPacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}
	log := r.logger.WithField("func", "router.handlePingPacket")

	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
		return r.forwardPacket(ctx, packet, rule)
	}

	log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)

	log.Tracef("Handling packet with descriptor %s", &desc)

	if ok {
		if nrg == nil {
			return errors.New("noiseRouteGroup is nil")
		}

		// in this case we have already initialized nrg and may use it straightforward
		log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
			len(packet.Payload()), packet.RouteID(), rule)

		return nrg.handlePacket(packet)
	}

	// we don't have nrg for this packet. it's either handshake message or
	// we don't have route for this one completely

	rg, ok := r.initializingRouteGroup(desc)
	if !ok {
		// no route, just return error
		log.Tracef("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	if rg == nil {
		return errors.New("initializing RouteGroup is nil")
	}

	// handshake packet, handling with the raw rg
	log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	return rg.handlePacket(packet)
}

func (r *router) handlePongPacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}
	log := r.logger.WithField("func", "router.handlePongPacket")

	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
		return r.forwardPacket(ctx, packet, rule)
	}

	log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)

	log.Tracef("Handling packet with descriptor %s", &desc)

	if ok {
		if nrg == nil {
			return errors.New("noiseRouteGroup is nil")
		}

		// in this case we have already initialized nrg and may use it straightforward
		log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
			len(packet.Payload()), packet.RouteID(), rule)

		return nrg.handlePacket(packet)
	}

	// we don't have nrg for this packet. it's either handshake message or
	// we don't have route for this one completely

	rg, ok := r.initializingRouteGroup(desc)
	if !ok {
		// no route, just return error
		log.Tracef("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	if rg == nil {
		return errors.New("initializing RouteGroup is nil")
	}

	// handshake packet, handling with the raw rg
	log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	return rg.handlePacket(packet)
}

func (r *router) handleErrorPacket(ctx context.Context, packet routing.Packet) error {
	rule, err := r.GetRule(packet.RouteID())
	if err != nil {
		return err
	}
	log := r.logger.WithField("func", "router.handleErrorPacket")
	if rt := rule.Type(); rt == routing.RuleForward || rt == routing.RuleIntermediary {
		log.Tracef("Handling packet of type %s with route ID %d and next ID %d", packet.Type(),
			packet.RouteID(), rule.NextRouteID())
		return r.forwardPacket(ctx, packet, rule)
	}

	log.Tracef("Handling packet of type %s with route ID %d", packet.Type(), packet.RouteID())

	desc := rule.RouteDescriptor()
	nrg, ok := r.noiseRouteGroup(desc)

	log.Tracef("Handling packet with descriptor %s", &desc)

	if ok {
		if nrg == nil {
			return errors.New("noiseRouteGroup is nil")
		}

		// in this case we have already initialized nrg and may use it straightforward
		log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
			len(packet.Payload()), packet.RouteID(), rule)

		return nrg.handlePacket(packet)
	}

	// we don't have nrg for this packet and we don't have route for this one completely

	rg, ok := r.initializingRouteGroup(desc)
	if !ok {
		// no route, just return error
		log.Tracef("Descriptor not found for rule with type %s, descriptor: %s", rule.Type(), &desc)
		return errors.New("route descriptor does not exist")
	}

	if rg == nil {
		return errors.New("initializing RouteGroup is nil")
	}

	// handshake packet, handling with the raw rg
	log.Tracef("Got new remote packet with size %d and route ID %d. Using rule: %s",
		len(packet.Payload()), packet.RouteID(), rule)

	return rg.handlePacket(packet)
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

func (r *router) forwardPacket(ctx context.Context, packet routing.Packet, rule routing.Rule) error {
	tp := r.tm.Transport(rule.NextTransportID())
	if tp == nil {
		return errors.New("unknown transport")
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
		// Forward the full handshake payload (preserves capability bitmap for extended handshakes)
		p = routing.MakeHandshakePacketRaw(rule.NextRouteID(), packet.Payload())
	case routing.KeepAlivePacket:
		p = routing.MakeKeepAlivePacket(rule.NextRouteID())
	case routing.ClosePacket:
		p = routing.MakeClosePacket(rule.NextRouteID(), routing.CloseCode(packet.Payload()[0]))
	case routing.PingPacket:
		timestamp := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset:]))    //nolint: gosec
		throughput := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset+8:])) //nolint: gosec
		p = routing.MakePingPacket(rule.NextRouteID(), timestamp, throughput)
	case routing.PongPacket:
		timestamp := int64(binary.BigEndian.Uint64(packet[routing.PacketPayloadOffset:])) //nolint: gosec
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

	// successfully forwarded packet, may update the rule activity now
	if err := r.UpdateRuleActivity(rule.KeyRouteID()); err != nil {
		r.logger.Errorf("Failed to update activity for rule with route ID %d: %v", rule.KeyRouteID(), err)
	}

	r.logger.Debugf("Forwarded packet via Transport %s using rule %d", rule.NextTransportID(), rule.KeyRouteID())

	return nil
}

// GetRule gets routing rule.
func (r *router) GetRule(routeID routing.RouteID) (routing.Rule, error) {
	rule, err := r.rt.Rule(routeID)
	if err != nil {
		return nil, fmt.Errorf("routing table: %w", err)
	}

	if rule == nil {
		return nil, errors.New("unknown RouteID")
	}

	// TODO(evanlinjin): This is a workaround for ensuring the read-in rule is of the correct size.
	// Sometimes it is not, causing a segfault later down the line.
	if len(rule) < routing.RuleHeaderSize {
		return nil, errors.New("corrupted rule")
	}

	return rule, nil
}

// UpdateRuleActivity updates routing rule activity
func (r *router) UpdateRuleActivity(routeID routing.RouteID) error {
	err := r.rt.UpdateActivity(routeID)
	if err != nil {
		return fmt.Errorf("error updating activity for route ID %d: %w", routeID, err)
	}

	return nil
}
