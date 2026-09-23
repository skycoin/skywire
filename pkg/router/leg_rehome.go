//go:build !tinygo || (js && wasm)

// Package router pkg/router/leg_rehome.go c2-net-routing
//
// Leg RE-HOME: an ACTIVE route group adopts the already-built route chain of a
// STANDBY route group as one of its packet-level mux legs, in place, with no
// setup-node dial.
//
// The chain's intermediate rules are pure routeID -> (routeID, transport)
// switches and carry no descriptor (routing.IntermediaryForwardRule), so they
// never learn the chain changed hands. What binds a chain to one route group is
// its single ConsumeRule at each edge, whose descriptor selects the group
// (router_packet.go: desc := rule.RouteDescriptor() -> r.rgsNs[desc]). Re-home
// REWRITES that one rule at each edge — same key route ID, new descriptor — and
// moves the leg object (its transport, its two rules, its recorded hops) into
// the target group's mux. The standby group is then legless and closes.
//
// This is the incremental half of the shared-trunk end state in
// docs/design/shared-warm-route-pool.md Phase 3: one chain still terminates in
// one consume rule, it just changes owner, so no demux is needed at the exit.
// It takes the capability bit that doc reserved — routing.CapLegRehome.
//
// See docs/design/leg-rehome.md for the packet, both sequences and the
// in-flight-frame rules.
package router

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// ErrRehomeUnsupported is returned when a chain cannot be re-homed in place:
// the peer did not advertise CapLegRehome, or a group involved has no mux. The
// caller's fallback is to spend the pooled tunnel the DIAL-based way — a
// pool-sourced leg through the setup node, reusing the pool's route plan and
// warm transport (see poolSourcedLegFallback).
var ErrRehomeUnsupported = errors.New("leg re-home unsupported (peer did not negotiate CapLegRehome)")

// ErrRehomeNoAck is the exit not answering a re-home request in time or
// refusing it: both groups are left intact, so the caller may fall back to
// dialing the leg on the pool's plan instead of giving up on the take.
var ErrRehomeNoAck = errors.New("re-home not acknowledged")

// legRehomeAckTimeout bounds the wait for the exit's ack (leg.rehome_ack_timeout).
// A re-home is two visors rewriting one rule each; if the ack does not come
// back within a few route RTTs the request is abandoned and BOTH groups are
// left exactly as they were — never half-moved.
func legRehomeAckTimeout() time.Duration { return routersettings.LegRehomeAckTimeout.Duration() }

// legRehomeHost is the narrow view of the router a route group needs to service
// an inbound re-home: the request arrives on THIS group's chain but names a
// DIFFERENT group by descriptor, and only the router holds the descriptor map.
type legRehomeHost interface {
	rehomeGroupFor(desc routing.RouteDescriptor) *RouteGroup
}

// rehomeGroupFor resolves a descriptor to its route group, live or still
// initializing. Implements legRehomeHost.
func (r *router) rehomeGroupFor(desc routing.RouteDescriptor) *RouteGroup {
	r.mx.Lock()
	defer r.mx.Unlock()
	if nrg, ok := r.rgsNs[desc]; ok && nrg != nil {
		return nrg.rg
	}
	if rg, ok := r.rgsRaw[desc]; ok {
		return rg
	}
	return nil
}

// rehomeWaiters parks the initiator's in-flight re-home requests by nonce. It
// is package-scoped rather than a router field because the key is a 64-bit
// crypto-random nonce minted per request and dropped on completion — two
// routers in one process cannot collide, and nothing survives the call.
var rehomeWaiters sync.Map // uint64 -> chan byte

// rehomeLeg is one chain detached from a group and not yet attached to another:
// its two edge rules, the first-hop transport it rides, and the forward path it
// was dialed with (so the adopting group keeps reporting real hops for it).
type rehomeLeg struct {
	fwd  routing.Rule
	rvs  routing.Rule
	tp   *transport.ManagedTransport
	hops []routing.Hop
}

// RehomeStandbyLeg moves the whole built chain of the STANDBY group `standby`
// into the ACTIVE group `target` as one more mux leg, with no setup-node dial.
// Implements the initiator half of docs/design/leg-rehome.md.
//
// On any refusal — an unsupported peer, a target that already holds the chain's
// transport, a timeout — both groups are left untouched and an error is
// returned; the caller then spends the pooled tunnel the dial-based way.
func (r *router) RehomeStandbyLeg(target, standby routing.RouteDescriptor) error {
	if target == standby {
		return errors.New("a group cannot re-home its own chain")
	}
	nrgTarget, err := r.lookupRouteGroup(target)
	if err != nil {
		return fmt.Errorf("target group: %w", err)
	}
	nrgStandby, err := r.lookupRouteGroup(standby)
	if err != nil {
		return fmt.Errorf("standby group: %w", err)
	}
	return rehomeChain(nrgTarget.rg, nrgStandby.rg)
}

// rehomeChain is the initiator sequence on two live groups, with the router's
// descriptor lookup already done. Split out so the emulated testbed can drive
// the real sequence against two route groups without a router.
func rehomeChain(g, s *RouteGroup) error {
	if g.desc.DstPK() != s.desc.DstPK() {
		return fmt.Errorf("the two groups do not share a peer (%s vs %s)", g.desc.DstPK(), s.desc.DstPK())
	}
	if g.mux == nil || s.mux == nil || !g.mux.legRehomeEnabled || !s.mux.legRehomeEnabled {
		return g.poolSourcedLegFallback(s)
	}

	// A standby tunnel is one chain. Moving a multi-leg group leg by leg is a
	// different operation (and would leave the app's session alive on the rest),
	// so it is refused rather than half-done.
	s.mu.Lock()
	legs := len(s.tps)
	var fwd routing.Rule
	var tp *transport.ManagedTransport
	if legs > 0 {
		fwd, tp = s.fwd[0], s.tps[0]
	}
	s.mu.Unlock()
	if legs != 1 || fwd == nil || tp == nil {
		return fmt.Errorf("the standby group has %d legs; re-home moves a single-chain standby tunnel", legs)
	}
	if err := g.rehomeTransportFree(tp); err != nil {
		return err
	}

	nonce, err := newRehomeNonce()
	if err != nil {
		return err
	}
	ackCh := make(chan byte, 1)
	rehomeWaiters.Store(nonce, ackCh)
	defer rehomeWaiters.Delete(nonce)

	req := routing.MakeLegRehomePacket(fwd.NextRouteID(), nonce, g.desc.SrcPort(), g.desc.DstPort(), 0)
	if err := s.writePacket(context.Background(), tp, req, fwd.KeyRouteID()); err != nil {
		return fmt.Errorf("send re-home request: %w", err)
	}
	globalMuxCounters.legRehomesSent.Add(1)

	var flags byte
	select {
	case flags = <-ackCh:
	case <-time.After(legRehomeAckTimeout()):
		globalMuxCounters.legRehomesFailed.Add(1)
		return fmt.Errorf("%w: no ack from %s within %s; both groups left intact", ErrRehomeNoAck, s.desc.DstPK(), legRehomeAckTimeout())
	}
	if flags&routing.LegRehomeRefused != 0 || flags&routing.LegRehomeAck == 0 {
		globalMuxCounters.legRehomesFailed.Add(1)
		return fmt.Errorf("%w: peer %s refused; both groups left intact", ErrRehomeNoAck, s.desc.DstPK())
	}
	globalMuxCounters.legRehomesAcked.Add(1)

	reason := fmt.Sprintf("rehome from :%d", s.desc.DstPort())
	leg, err := s.detachLegForRehome(0, reason)
	if err != nil {
		return err
	}
	idx, err := g.adoptRehomedLeg(leg, reason, false)
	if err != nil {
		return err
	}

	// Commit: sent through the TARGET group's rules, so it lands on the exit's
	// rewritten consume rule and promotes the leg it parked in standby.
	commit := routing.MakeLegRehomePacket(leg.fwd.NextRouteID(), nonce,
		g.desc.SrcPort(), g.desc.DstPort(), routing.LegRehomeCommit)
	if err := g.writePacket(context.Background(), leg.tp, commit, leg.fwd.KeyRouteID()); err != nil {
		g.logger.WithError(err).Warnf("Re-home: failed to send the commit for leg %d", idx)
	}
	s.noteTunnelConsumed(g.desc.DstPort())
	go func() {
		if err := s.Close(); err != nil {
			s.logger.WithError(err).Debug("Re-home: closing the consumed standby group")
		}
	}()
	g.logger.Infof("Re-homed the chain of standby group :%d into :%d as leg %d",
		s.desc.DstPort(), g.desc.DstPort(), idx)
	return nil
}

// poolSourcedLegFallback is the call site for the DIAL-based way of spending a
// pooled tunnel when its chain cannot be moved in place — an exit that never
// negotiated CapLegRehome. The replacement is a leg dialed through the setup
// node reusing the pool's route plan and its already-warm transport, which is
// built separately; until it lands here, this reports that in-place re-home is
// unavailable so a caller can branch on it rather than discovering a half-moved
// chain.
func (rg *RouteGroup) poolSourcedLegFallback(standby *RouteGroup) error {
	rg.logger.Debugf("Re-home unavailable for the chain of :%d; it has to be spent by dialing a pool-sourced leg",
		standby.desc.DstPort())
	return ErrRehomeUnsupported
}

// handleLegRehomePacket services every phase of a re-home on the group the
// packet's chain currently belongs to. Never errors on a malformed or
// unexpected packet: a re-home that cannot be carried out leaves both groups
// untouched, which is the same outcome as dropping it.
func (rg *RouteGroup) handleLegRehomePacket(packet routing.Packet) error {
	nonce, srcPort, dstPort, flags, ok := packet.LegRehomeFields()
	if !ok {
		return nil
	}
	switch {
	case flags&(routing.LegRehomeAck|routing.LegRehomeRefused) != 0:
		if ch, loaded := rehomeWaiters.Load(nonce); loaded {
			select {
			case ch.(chan byte) <- flags:
			default:
			}
		}
		return nil
	case flags&routing.LegRehomeCommit != 0:
		rg.commitRehomedLeg(packet.RouteID())
		return nil
	case flags&routing.LegRehomeSplit != 0:
		rg.acceptSplit(packet.RouteID(), nonce, srcPort, dstPort)
		return nil
	default:
		rg.acceptRehome(packet.RouteID(), nonce, srcPort, dstPort)
		return nil
	}
}

// acceptRehome is the exit half: the request arrived on THIS group's chain and
// names another group of the same peer as the chain's new owner.
func (rg *RouteGroup) acceptRehome(routeID routing.RouteID, nonce uint64, srcPort, dstPort routing.Port) {
	globalMuxCounters.legRehomesReceived.Add(1)
	idx := rg.legIndexByConsumeRule(routeID)
	target, err := rg.rehomeTarget(srcPort, dstPort)
	if err == nil && idx < 0 {
		err = fmt.Errorf("no leg holds consume rule %d", routeID)
	}
	if err == nil {
		err = target.rehomeTransportFree(rg.legTransportAt(idx))
	}
	var leg *rehomeLeg
	reason := fmt.Sprintf("rehome from :%d", rg.desc.DstPort())
	if err == nil {
		leg, err = rg.detachLegForRehome(idx, reason)
	}
	if err == nil {
		// Adopted in STANDBY: this side is the bulk sender on a download, and it
		// must not stripe onto the chain before the initiator has rewritten its
		// own consume rule. The commit promotes it.
		_, err = target.adoptRehomedLeg(leg, reason, true)
	}
	if err != nil {
		globalMuxCounters.legRehomesFailed.Add(1)
		rg.logger.WithError(err).Debug("Re-home refused")
		rg.replyRehome(idx, nonce, srcPort, dstPort, routing.LegRehomeRefused)
		return
	}
	target.replyRehomeOn(leg, nonce, srcPort, dstPort, routing.LegRehomeAck)
	rg.noteTunnelConsumed(dstPort)
	go func() {
		if err := rg.Close(); err != nil {
			rg.logger.WithError(err).Debug("Re-home: closing the consumed standby group")
		}
	}()
}

// commitRehomedLeg promotes a leg this group adopted in standby: the initiator
// has rewritten its own consume rule, so the chain now carries this group's
// sequence space in both directions.
func (rg *RouteGroup) commitRehomedLeg(routeID routing.RouteID) {
	idx := rg.legIndexByConsumeRule(routeID)
	if idx < 0 || rg.mux == nil {
		return
	}
	rg.mux.setLegStandby(idx, false)
	rg.mux.markLegReady(idx)
	rg.mux.signalWindow()
	tp := rg.legTransportAt(idx)
	rg.noteLegEvent(MuxEventLegPromoted, "rehome commit: the adopted chain now carries this group",
		MuxByPeer, idx, rg.legCount(), tp, rg.legHopsFor(tpEntryID(tp)))
	rg.signalServiceWake()
}

// rehomeTarget resolves the group the initiator named. The ports are the
// SENDER's, so they are mirrored onto this chain's own PK pair — which is what
// confines a re-home to two groups already joining the same two visors.
func (rg *RouteGroup) rehomeTarget(srcPort, dstPort routing.Port) (*RouteGroup, error) {
	if rg.mux == nil || !rg.mux.legRehomeEnabled {
		return nil, ErrRehomeUnsupported
	}
	if rg.rehomeHost == nil {
		return nil, errors.New("this route group is not registered with a router")
	}
	desc := routing.NewRouteDescriptor(rg.desc.SrcPK(), rg.desc.DstPK(), dstPort, srcPort)
	target := rg.rehomeHost.rehomeGroupFor(desc)
	if target == nil {
		return nil, fmt.Errorf("no route group for %s", desc.String())
	}
	if target == rg {
		return nil, errors.New("a group cannot re-home its own chain")
	}
	if target.mux == nil || !target.mux.legRehomeEnabled {
		return nil, ErrRehomeUnsupported
	}
	if target.desc.DstPK() != rg.desc.DstPK() {
		return nil, errors.New("the target group belongs to a different peer")
	}
	return target, nil
}

// replyRehome answers on the leg that is still ours (the refusal path).
func (rg *RouteGroup) replyRehome(idx int, nonce uint64, srcPort, dstPort routing.Port, flags byte) {
	rg.mu.Lock()
	var fwd routing.Rule
	var tp *transport.ManagedTransport
	if idx >= 0 && idx < len(rg.fwd) && idx < len(rg.tps) {
		fwd, tp = rg.fwd[idx], rg.tps[idx]
	}
	rg.mu.Unlock()
	if fwd == nil || tp == nil {
		return
	}
	rg.replyRehomeOn(&rehomeLeg{fwd: fwd, tp: tp}, nonce, srcPort, dstPort, flags)
}

// replyRehomeOn answers over a detached (or still-held) leg. The chain's
// next-hop route ID toward the initiator is unchanged by the rewrite, so the
// reply rides the same rule either way.
func (rg *RouteGroup) replyRehomeOn(leg *rehomeLeg, nonce uint64, srcPort, dstPort routing.Port, flags byte) {
	if leg == nil || leg.fwd == nil || leg.tp == nil {
		return
	}
	pkt := routing.MakeLegRehomePacket(leg.fwd.NextRouteID(), nonce, srcPort, dstPort, flags)
	if err := rg.writePacket(context.Background(), leg.tp, pkt, leg.fwd.KeyRouteID()); err != nil {
		rg.logger.WithError(err).Debug("Re-home: failed to answer the request")
	}
}

// legIndexByConsumeRule finds the leg a packet arrived on, the way every
// on-leg control packet identifies itself (see handleLegStatePacket).
func (rg *RouteGroup) legIndexByConsumeRule(routeID routing.RouteID) int {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for i, rv := range rg.rvs {
		if rv != nil && rv.KeyRouteID() == routeID {
			return i
		}
	}
	return -1
}

// rehomeTransportFree enforces the mux invariant a dialed aux leg is held to
// (appendRouteToGroup): no two legs of one group may share a transport.
func (rg *RouteGroup) rehomeTransportFree(tp *transport.ManagedTransport) error {
	if tp == nil {
		return errors.New("the chain has no transport")
	}
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for _, held := range rg.tps {
		if held != nil && held.Entry.ID == tp.Entry.ID {
			return fmt.Errorf("refusing to re-home: transport %s is already a leg of this group", tp.Entry.ID)
		}
	}
	return nil
}

// detachLegForRehome takes a leg out of this group WITHOUT deleting its rules —
// they are about to be rewritten and handed to another group. The counterpart
// of pruneLegByConsumeRule, which deletes them.
func (rg *RouteGroup) detachLegForRehome(idx int, reason string) (*rehomeLeg, error) {
	rg.mu.Lock()
	if idx < 0 || idx >= len(rg.tps) || idx >= len(rg.fwd) || idx >= len(rg.rvs) {
		rg.mu.Unlock()
		return nil, fmt.Errorf("no leg %d to re-home", idx)
	}
	leg := &rehomeLeg{fwd: rg.fwd[idx], rvs: rg.rvs[idx], tp: rg.tps[idx]}
	leg.hops = rg.legHopsLocked(tpEntryID(leg.tp))
	rg.tps = append(rg.tps[:idx], rg.tps[idx+1:]...)
	rg.fwd = append(rg.fwd[:idx], rg.fwd[idx+1:]...)
	rg.rvs = append(rg.rvs[:idx], rg.rvs[idx+1:]...)
	if rg.mux != nil {
		rg.mux.removeLegs(idx)
		rg.mux.rebuildWeights(rg.tps)
	}
	rg.noteLegEvent(MuxEventLegRemoved, reason+": the chain left this group", MuxByLocal,
		idx, len(rg.tps), leg.tp, leg.hops)
	rg.mu.Unlock()
	rg.fireLegChange("rehomed-away", idx)
	return leg, nil
}

// adoptRehomedLeg rewrites a detached chain's two edge rules to this group's
// descriptor, saves them (SaveRule replaces by key route ID, so the chain keeps
// its reserved IDs at every hop), and appends the leg.
func (rg *RouteGroup) adoptRehomedLeg(leg *rehomeLeg, reason string, standby bool) (int, error) {
	if leg == nil {
		return -1, errors.New("no leg to adopt")
	}
	fwd, err := rehomeForwardRule(leg.fwd, rg.desc)
	if err != nil {
		return -1, err
	}
	rvs, err := rehomeConsumeRule(leg.rvs, rg.desc)
	if err != nil {
		return -1, err
	}
	if err := rg.rt.SaveRule(rvs); err != nil {
		return -1, fmt.Errorf("save the re-homed consume rule: %w", err)
	}
	if err := rg.rt.SaveRule(fwd); err != nil {
		return -1, fmt.Errorf("save the re-homed forward rule: %w", err)
	}

	rg.mu.Lock()
	rg.fwd = append(rg.fwd, fwd)
	rg.rvs = append(rg.rvs, rvs)
	rg.tps = append(rg.tps, leg.tp)
	idx := len(rg.tps) - 1
	if rg.mux != nil {
		rg.mux.growLegs(len(rg.tps))
		rg.mux.rebuildWeights(rg.tps)
	}
	rg.noteLegEvent(MuxEventLegAdded, reason, MuxByOperator, idx, len(rg.tps), leg.tp, leg.hops)
	rg.mu.Unlock()

	if rg.mux != nil {
		// The chain is already established — it carried another group a moment
		// ago — so it needs no aux-leg handshake to become selectable.
		rg.mux.markLegReady(idx)
		rg.mux.setLegStandby(idx, standby)
	}
	if len(leg.hops) > 0 {
		rg.recordLegRoute(leg.hops, nil)
	}
	if !standby {
		rg.noteLegEvent(MuxEventLegPromoted, reason, MuxByOperator, idx, rg.legCount(),
			leg.tp, leg.hops)
	}
	rg.signalServiceWake()
	rg.fireLegChange("rehomed-in", idx)
	return idx, nil
}

// noteTunnelConsumed records that this group was SPENT, not lost: its chain
// became a leg of the group on port `into`. The app that owns the tunnel reads
// this to drop it from its pool without treating it as a death (which re-arms
// the redial backoff and the pool fill — a storm for a tunnel nobody lost).
func (rg *RouteGroup) noteTunnelConsumed(into routing.Port) {
	rg.noteMuxEvent(MuxEvent{
		Event: MuxEventTunnelConsumed, By: MuxByOperator, LegIndex: -1, Legs: 0,
		Reason: fmt.Sprintf("tunnel consumed: its chain is now a mux leg of :%d", into),
	})
}

// rehomeConsumeRule returns the chain's consume rule re-pointed at desc: the
// SAME key route ID (the chain's reserved inbound ID is what must not change)
// with a new descriptor, so the edge dispatch hands the chain's packets to the
// adopting group.
func rehomeConsumeRule(old routing.Rule, desc routing.RouteDescriptor) (routing.Rule, error) {
	if old == nil || old.Type() != routing.RuleReverse {
		return nil, errors.New("re-home expects a consume (reverse) rule")
	}
	return routing.ConsumeRule(old.KeepAlive(), old.KeyRouteID(),
		desc.SrcPK(), desc.DstPK(), desc.SrcPort(), desc.DstPort()), nil
}

// rehomeForwardRule returns the chain's forward rule re-pointed at desc. Its
// next-hop route ID and transport are the chain itself and are preserved
// exactly; only the descriptor it reports changes, so `visor state` and the
// rules table stop attributing the chain to the group that gave it up.
func rehomeForwardRule(old routing.Rule, desc routing.RouteDescriptor) (routing.Rule, error) {
	if old == nil || old.Type() != routing.RuleForward {
		return nil, errors.New("re-home expects a forward rule")
	}
	return routing.ForwardRule(old.KeepAlive(), old.KeyRouteID(), old.NextRouteID(), old.NextTransportID(),
		desc.SrcPK(), desc.DstPK(), desc.SrcPort(), desc.DstPort()), nil
}

// newRehomeNonce mints the 64-bit value that ties a request, its ack and its
// commit together.
func newRehomeNonce() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("re-home nonce: %w", err)
	}
	return binary.BigEndian.Uint64(b[:]), nil
}
