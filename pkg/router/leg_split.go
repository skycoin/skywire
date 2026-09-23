//go:build !tinygo || (js && wasm)

// Package router pkg/router/leg_split.go c2-net-routing
//
// Leg SPLIT — the reverse of leg re-home (leg_rehome.go).
//
// Re-home moves a whole standby CHAIN into an active group as one more
// packet-level mux leg. A split moves it back out: the leg leaves the group and
// becomes a standalone STANDBY route group again, with its own ports and its
// own consume rule at each edge, keeping the same transport and the same
// reserved route IDs at every hop. Nothing is dialed, nothing is closed.
//
// It is the same rewrite in the other direction — one consume rule per edge
// re-pointed at a different descriptor — so it rides the SAME packet type with
// one more flag, routing.LegRehomeSplit, and the same capability bit
// (routing.CapLegRehome). An exit that never negotiated it simply never acks,
// and the caller falls back to what the release path did before: closing the
// transport.
//
// Why it matters: a route has to be able to change from stream level to packet
// level and back trivially. Before this, the pool arbiter's release
// (pool_arbiter.go releaseLegByTransport) CLOSED a leg it had taken from the
// standby pool, so every load episode cost the app a fresh setup dial to refill
// the pool. A split returns the chain to the pool intact, ready to be taken
// again by re-home.
package router

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// ErrSplitUnsupported is returned when a leg cannot be split back out in
// place: the peer did not negotiate CapLegRehome, the group has no mux, or the
// group is not registered with a router that can hold the new standby group.
// The caller's fallback is to close the leg's transport, which is what the
// release path did before splits existed.
var ErrSplitUnsupported = fmt.Errorf("%w: leg split unavailable", ErrRehomeUnsupported)

// splitPortFirst is the bottom of the local port range a split-out standby
// group is given. It is not a tunable: the range only has to avoid the
// well-known app ports below it and be wide enough that a random pick collides
// rarely, and every collision is retried against the router's own descriptor
// map anyway (see splitGroupPort).
const splitPortFirst = 16384

// legSplitHost is the half of the router a split needs on top of
// legRehomeHost: a free local port for the group that is about to appear, and
// the registration that makes it visible to the edge dispatch and to the pool
// arbiter. A route group whose host does not implement it cannot split.
type legSplitHost interface {
	// splitGroupPort reserves a local port no live group of this router is
	// using with the same peer.
	splitGroupPort(base routing.RouteDescriptor) (routing.Port, error)
	// registerSplitGroup publishes a group built outside the dial and accept
	// paths, so packets arriving on its chain reach it.
	registerSplitGroup(rg *RouteGroup) error
}

// splitGroupPort implements legSplitHost: a random port in the ephemeral range
// that no route group of this router already holds against the same peer.
func (r *router) splitGroupPort(base routing.RouteDescriptor) (routing.Port, error) {
	for i := 0; i < 32; i++ {
		port, err := randomSplitPort()
		if err != nil {
			return 0, err
		}
		desc := routing.NewRouteDescriptor(base.SrcPK(), base.DstPK(), port, base.DstPort())
		r.mx.Lock()
		_, ns := r.rgsNs[desc]
		_, raw := r.rgsRaw[desc]
		r.mx.Unlock()
		if !ns && !raw {
			return port, nil
		}
	}
	return 0, errors.New("no free local port for the split-out standby group")
}

// registerSplitGroup implements legSplitHost. The group is published unwrapped:
// its chain is already established and it carries no application stream — it is
// a chain the pool arbiter may take again, not a tunnel an app can dial.
func (r *router) registerSplitGroup(rg *RouteGroup) error {
	if rg == nil {
		return errors.New("no group to register")
	}
	r.mx.Lock()
	defer r.mx.Unlock()
	if _, ok := r.rgsNs[rg.desc]; ok {
		return fmt.Errorf("a route group already holds %s", rg.desc.String())
	}
	if _, ok := r.rgsRaw[rg.desc]; ok {
		return fmt.Errorf("a route group is initializing on %s", rg.desc.String())
	}
	r.rgsNs[rg.desc] = &NoiseRouteGroup{rg: rg, Conn: rg}
	return nil
}

// randomSplitPort picks one port in [splitPortFirst, 65535].
func randomSplitPort() (routing.Port, error) {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("split port: %w", err)
	}
	return routing.Port(splitPortFirst + binary.BigEndian.Uint16(b[:])%(65535-splitPortFirst)), nil
}

// splitLeg is the initiator sequence: leg idx of this group leaves it and
// becomes a standalone standby group of the same app and the same peer.
//
// On any refusal — an unsupported peer, no ack, a host that cannot register the
// new group — the leg is left exactly where it was and an error is returned;
// the caller then closes the transport as the release always did.
func (rg *RouteGroup) splitLeg(idx int, reason string) (*RouteGroup, error) {
	if rg.mux == nil || !rg.mux.legRehomeEnabled {
		return nil, ErrSplitUnsupported
	}
	host, ok := rg.rehomeHost.(legSplitHost)
	if !ok || host == nil {
		return nil, fmt.Errorf("%w: this route group has no host to register the standby group with", ErrSplitUnsupported)
	}

	rg.mu.Lock()
	legs := len(rg.tps)
	var fwd routing.Rule
	var tp *transport.ManagedTransport
	if idx >= 0 && idx < legs && idx < len(rg.fwd) {
		fwd, tp = rg.fwd[idx], rg.tps[idx]
	}
	rg.mu.Unlock()
	// A group's LAST leg is the group: splitting it off would leave the app's
	// session with no route at all, which is a close, not a split.
	if legs < 2 {
		return nil, fmt.Errorf("a group holding %d leg(s) cannot split one off", legs)
	}
	if fwd == nil || tp == nil {
		return nil, fmt.Errorf("no leg %d to split", idx)
	}

	port, err := host.splitGroupPort(rg.desc)
	if err != nil {
		return nil, err
	}

	// Quiesce the leg before the exit is told: the scheduler must stop striping
	// onto a chain whose consume rule is about to name another group. The
	// release path this serves closed the transport outright, so anything still
	// in flight is no worse off than it was.
	rg.mux.setLegStandby(idx, true)

	nonce, err := newRehomeNonce()
	if err != nil {
		rg.mux.setLegStandby(idx, false)
		return nil, err
	}
	ackCh := make(chan byte, 1)
	rehomeWaiters.Store(nonce, ackCh)
	defer rehomeWaiters.Delete(nonce)

	req := routing.MakeLegRehomePacket(fwd.NextRouteID(), nonce, port, rg.desc.DstPort(), routing.LegRehomeSplit)
	if err := rg.writePacket(context.Background(), tp, req, fwd.KeyRouteID()); err != nil {
		rg.mux.setLegStandby(idx, false)
		return nil, fmt.Errorf("send split request: %w", err)
	}
	globalMuxCounters.legSplitsSent.Add(1)

	var flags byte
	select {
	case flags = <-ackCh:
	case <-time.After(legRehomeAckTimeout()):
		rg.mux.setLegStandby(idx, false)
		globalMuxCounters.legSplitsFailed.Add(1)
		return nil, fmt.Errorf("%w: no split ack from %s within %s; the leg is left where it was",
			ErrRehomeNoAck, rg.desc.DstPK(), legRehomeAckTimeout())
	}
	if flags&routing.LegRehomeRefused != 0 || flags&routing.LegRehomeAck == 0 {
		rg.mux.setLegStandby(idx, false)
		globalMuxCounters.legSplitsFailed.Add(1)
		return nil, fmt.Errorf("%w: peer %s refused the split; the leg is left where it was",
			ErrRehomeNoAck, rg.desc.DstPK())
	}
	globalMuxCounters.legSplitsAcked.Add(1)

	// The wait was long enough for a prune to have moved the legs under us;
	// the chain is named by its TRANSPORT, never by the index the request was
	// built from.
	if idx = rg.releasableLegIndex(tp.Entry.ID); idx < 0 {
		globalMuxCounters.legSplitsFailed.Add(1)
		return nil, fmt.Errorf("the leg on transport %s is gone; nothing to split", tp.Entry.ID)
	}
	ns, err := rg.adoptSplitLeg(idx, port, rg.desc.DstPort(), reason)
	if err != nil {
		globalMuxCounters.legSplitsFailed.Add(1)
		return nil, err
	}
	if err := host.registerSplitGroup(ns); err != nil {
		globalMuxCounters.legSplitsFailed.Add(1)
		return nil, fmt.Errorf("register the split-out standby group: %w", err)
	}
	return ns, nil
}

// adoptSplitLeg detaches leg idx and hands it to a new standby group on port,
// rolling the leg back into this group if the hand-off itself fails. Shared by
// both edges: the exit runs exactly this once it has agreed to the split.
func (rg *RouteGroup) adoptSplitLeg(idx int, srcPort, dstPort routing.Port, reason string) (*RouteGroup, error) {
	leg, err := rg.detachLegForRehome(idx, reason)
	if err != nil {
		return nil, err
	}
	ns := rg.newSplitGroup(srcPort, dstPort)
	if _, err := ns.adoptRehomedLeg(leg, reason, false); err != nil {
		// The chain is nobody's for an instant; give it back rather than
		// stranding a live route with no owner.
		if _, rerr := rg.adoptRehomedLeg(leg, reason+" (split rolled back)", false); rerr != nil {
			rg.logger.WithError(rerr).Warn("Split: the leg could not be rolled back into its group")
		}
		if cerr := ns.Close(); cerr != nil {
			rg.logger.WithError(cerr).Debug("Split: closing the group that never took the chain")
		}
		return nil, fmt.Errorf("hand the chain to the standby group: %w", err)
	}
	return ns, nil
}

// newSplitGroup builds the standalone standby group a split leg becomes: the
// same app, the same peer, a fresh local port.
//
// The group never handshakes — its chain is already live — so the negotiation
// that normally builds the mux (route_group.go) cannot run for it, and the
// shape is inherited from the group it came out of instead. Both edges inherit
// from their own copy of that same group, so the two ends still agree.
func (rg *RouteGroup) newSplitGroup(srcPort, dstPort routing.Port) *RouteGroup {
	desc := routing.NewRouteDescriptor(rg.desc.SrcPK(), rg.desc.DstPK(), srcPort, dstPort)
	ns := NewRouteGroup(rg.cfg, rg.rt, desc, nil)
	ns.initiator = rg.initiator
	ns.muxEvents = rg.muxEvents
	ns.rehomeHost = rg.rehomeHost
	ns.SetAppName(rg.AppName())
	// A chain handed back to the pool is a STANDBY tunnel whatever the group it
	// left was doing; on the accept side the role is empty and stays empty
	// (SetTunnelRole ignores the empty role).
	if rg.TunnelRole() == tunnelRoleActive {
		ns.SetTunnelRole(tunnelRoleStandby)
	} else {
		ns.SetTunnelRole(rg.TunnelRole())
	}
	ns.mux = newRouteMux(ns.logger, rg.mux.sackEnabled)
	ns.mux.knobHolder = ns.knobHolder
	ns.mux.legRehomeEnabled = rg.mux.legRehomeEnabled
	ns.mux.deliveryCRC = rg.mux.deliveryCRC
	ns.mux.groupPort = desc.SrcPort()
	ns.mux.SetLegLatencyFn(ns.legEndToEndLatencyMs)
	ns.mux.SetForwardRehomeFn(ns.noteForwardRehome)
	ns.noteMuxEvent(MuxEvent{Event: MuxEventGroupCreated, By: MuxByLocal, LegIndex: -1,
		Reason: fmt.Sprintf("standby group split out of :%d, holding that group's chain", rg.desc.DstPort())})
	ns.startOffServiceLoops()
	return ns
}

// acceptSplit is the exit half of a split: the request arrived on THIS group's
// chain and names the ports the initiator has picked for the chain's new,
// standalone group. Mirrors acceptRehome, and like it never errors — a split
// that cannot be carried out is refused, and both ends keep what they have.
func (rg *RouteGroup) acceptSplit(routeID routing.RouteID, nonce uint64, srcPort, dstPort routing.Port) {
	globalMuxCounters.legSplitsReceived.Add(1)
	idx := rg.legIndexByConsumeRule(routeID)
	err := rg.splitAllowed(srcPort, dstPort)
	if err == nil && idx < 0 {
		err = fmt.Errorf("no leg holds consume rule %d", routeID)
	}
	var ns *RouteGroup
	reason := fmt.Sprintf("split out of :%d", rg.desc.DstPort())
	if err == nil {
		rg.mux.setLegStandby(idx, true)
		// The initiator's ports are ITS ports; the new group on this side is
		// their mirror, exactly as rehomeTarget mirrors a re-home's.
		ns, err = rg.adoptSplitLeg(idx, dstPort, srcPort, reason)
	}
	if err == nil {
		err = rg.rehomeHost.(legSplitHost).registerSplitGroup(ns) //nolint:errcheck,forcetypeassert // splitAllowed proved the type
	}
	if err != nil {
		globalMuxCounters.legSplitsFailed.Add(1)
		if idx >= 0 && rg.mux != nil {
			rg.mux.setLegStandby(idx, false)
		}
		rg.logger.WithError(err).Debug("Split refused")
		rg.replyRehome(idx, nonce, srcPort, dstPort, routing.LegRehomeRefused|routing.LegRehomeSplit)
		return
	}
	ns.replyRehomeOnLeg(nonce, srcPort, dstPort, routing.LegRehomeAck|routing.LegRehomeSplit)
	rg.logger.Infof("Split leg %d out of :%d into standby group :%d (peer :%d)", idx, rg.desc.DstPort(), ns.desc.SrcPort(), ns.desc.DstPort())
}

// splitAllowed reports whether this group may give a chain up: the mux must
// have negotiated the capability, the host must be able to register the group
// the chain becomes, and no group may already hold the mirrored descriptor.
func (rg *RouteGroup) splitAllowed(srcPort, dstPort routing.Port) error {
	if rg.mux == nil || !rg.mux.legRehomeEnabled {
		return ErrSplitUnsupported
	}
	if rg.rehomeHost == nil {
		return errors.New("this route group is not registered with a router")
	}
	if _, ok := rg.rehomeHost.(legSplitHost); !ok {
		return ErrSplitUnsupported
	}
	desc := routing.NewRouteDescriptor(rg.desc.SrcPK(), rg.desc.DstPK(), dstPort, srcPort)
	if other := rg.rehomeHost.rehomeGroupFor(desc); other != nil {
		return fmt.Errorf("a route group already holds %s", desc.String())
	}
	return nil
}

// replyRehomeOnLeg answers over this group's own first leg — the chain it has
// just taken over, which still reaches the initiator on the same next-hop route
// ID the rewrite left alone.
func (rg *RouteGroup) replyRehomeOnLeg(nonce uint64, srcPort, dstPort routing.Port, flags byte) {
	rg.mu.Lock()
	var leg *rehomeLeg
	if len(rg.fwd) > 0 && len(rg.tps) > 0 {
		leg = &rehomeLeg{fwd: rg.fwd[0], tp: rg.tps[0]}
	}
	rg.mu.Unlock()
	rg.replyRehomeOn(leg, nonce, srcPort, dstPort, flags)
}

// splitOnRelease reports whether the pool arbiter's release should try to hand
// the chain back to the pool before closing it.
func (rg *RouteGroup) splitOnRelease() bool { return rg.knBool(routersettings.LegSplitOnRelease) }
