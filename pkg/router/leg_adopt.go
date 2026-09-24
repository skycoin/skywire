//go:build !tinygo || (js && wasm)

// Package router pkg/router/leg_adopt.go c2-net-routing
//
// Leg ADOPTION — the last move of the shape axis: a LEG RESERVE becomes a
// stream-level tunnel again.
//
// A split (leg_split.go) hands a packet-level leg back to the pool as a
// standalone group, and re-home (leg_rehome.go) can take it as a leg again. But
// the reserve has no app session and no end-to-end handshake, so it could never
// carry a stream: a chain that was ever split out stayed packet-level for good.
// Adoption closes that loop. The dialing app asks for a new tunnel naming the
// reserve (DialOptions.AdoptReservePort); both edges re-point the reserve's one
// consume rule at the new tunnel's descriptor — the same one-rule rewrite as
// re-home and split, on the same capability and the same leg control dialect,
// with one more flag (routing.LegRehomeAdopt) — and the chain is then set up
// EXACTLY like a freshly dialed route group from that point on:
// saveRouteGroupRules runs the handshake (initiator here, responder at the
// exit), the exit's copy goes through AcceptRoutes to whichever app listens on
// the service port, and the dialing app gets its conn from DialRoutes.
//
// The descriptor is the dial's own, so the local port is the fresh one the
// app's dial reserved — the reserve's local port is shared with the tunnel it
// was split out of and could not be reused.
//
// An exit that never learned the flag reads the request as a re-home naming a
// group it does not hold and refuses it; one that cannot be reached never
// answers. Either way the reserve is left exactly as it was and the app dials
// the way it always did.
package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// ErrAdoptUnsupported is returned when a leg reserve cannot be adopted in
// place: there is no such reserve, it has no mux that negotiated
// CapLegRehome, or it holds other than exactly one chain.
var ErrAdoptUnsupported = fmt.Errorf("%w: leg reserve adoption unavailable", ErrRehomeUnsupported)

// legAdoptHost is the half of the router the EXIT needs to finish an adoption:
// drop the reserve from the descriptor map and hand the re-pointed chain to the
// accept path, as the setup node's rules would be.
type legAdoptHost interface {
	acceptAdoptedChain(reserve *RouteGroup, rules routing.EdgeRules)
}

// acceptAdoptedChain implements legAdoptHost. The rules are already saved; the
// send is the same one IntroduceRules makes, run off the caller's goroutine
// because the caller is a packet-intake worker and the accept buffer can fill.
func (r *router) acceptAdoptedChain(reserve *RouteGroup, rules routing.EdgeRules) {
	r.dropGroupIfHeld(reserve)
	go func() {
		select {
		case r.accept <- rules:
		case <-r.done:
		}
	}()
}

// dropGroupIfHeld removes rg from the descriptor map, but only if the entry is
// still rg: a group that took its descriptor meanwhile is left alone.
func (r *router) dropGroupIfHeld(rg *RouteGroup) {
	r.mx.Lock()
	defer r.mx.Unlock()
	if nrg, ok := r.rgsNs[rg.desc]; ok && nrg != nil && nrg.rg == rg {
		delete(r.rgsNs, rg.desc)
	}
}

// legReserveFor finds the live leg reserve toward peer whose far-end port is
// port — the name the proxy status snapshot gives it (Tunnel.RemotePort). A
// named app only adopts its own reserves.
func (r *router) legReserveFor(peer cipher.PubKey, port routing.Port, app string) *RouteGroup {
	r.mx.Lock()
	defer r.mx.Unlock()
	for _, nrg := range r.rgsNs {
		if nrg == nil || nrg.rg == nil || !nrg.rg.legReserve || nrg.rg.isClosed() {
			continue
		}
		if nrg.rg.farEndPK() == peer && nrg.rg.farEndPort() == port &&
			(app == "" || nrg.rg.AppName() == app) {
			return nrg.rg
		}
	}
	return nil
}

// farEndPort is the port on the far end of this group's descriptor.
func (rg *RouteGroup) farEndPort() routing.Port {
	if rg.desc.SrcPK() == rg.farEndPK() {
		return rg.desc.SrcPort()
	}
	return rg.desc.DstPort()
}

// adoptDesc is the tunnel descriptor this reserve's chain becomes on THIS
// side: the same two visors in the same orientation, with the far end on
// farPort (the service) and this visor on localPort.
func (rg *RouteGroup) adoptDesc(localPort, farPort routing.Port) routing.RouteDescriptor {
	if rg.desc.SrcPK() == rg.farEndPK() {
		return routing.NewRouteDescriptor(rg.desc.SrcPK(), rg.desc.DstPK(), farPort, localPort)
	}
	return routing.NewRouteDescriptor(rg.desc.SrcPK(), rg.desc.DstPK(), localPort, farPort)
}

// adoptLegReserve is DialRoutes for opts.AdoptReservePort: the initiator half.
// On success the reserve is gone and the returned conn is a route group on the
// dial's own descriptor, handshaked and wired exactly like a dialed one.
func (r *router) adoptLegReserve(ctx context.Context, log *logging.Logger, rPK cipher.PubKey,
	lPort, rPort routing.Port, opts *DialOptions) (net.Conn, error) {
	reserve := r.legReserveFor(rPK, opts.AdoptReservePort, opts.AppName)
	if reserve == nil {
		return nil, fmt.Errorf("%w: no leg reserve to %s on port %d", ErrAdoptUnsupported, rPK, opts.AdoptReservePort)
	}
	desc := reserve.adoptDesc(lPort, rPort)
	if other := r.rehomeGroupFor(desc); other != nil {
		return nil, fmt.Errorf("a route group already holds %s", desc.String())
	}
	leg, err := reserve.requestAdopt(desc)
	if err != nil {
		return nil, err
	}
	// The chain is no longer the reserve's at either end; the legless reserve
	// goes, as a consumed standby does after a re-home.
	r.dropGroupIfHeld(reserve)
	reserve.noteTunnelAdopted(lPort)
	go func() {
		if err := reserve.Close(); err != nil {
			reserve.logger.WithError(err).Debug("Adopt: closing the emptied leg reserve")
		}
	}()

	rules, err := adoptedEdgeRules(r.rt, leg, desc)
	if err != nil {
		return nil, err
	}
	nsConf := noise.Config{
		LocalPK:   r.conf.PubKey,
		LocalSK:   r.conf.SecKey,
		RemotePK:  rPK,
		Initiator: true,
	}
	nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf, opts.AppName, opts.TunnelRole, false)
	if err != nil {
		r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
		return nil, fmt.Errorf("adopted leg reserve: %w", err)
	}
	opts.note("adopted leg reserve :%d (first hop %s) as a tunnel; no route setup", opts.AdoptReservePort, leg.tp.Remote())
	log.WithField("first_hop", leg.tp.Remote().String()).
		Infof("Adopted leg reserve :%d as tunnel :%d -> %s:%d", opts.AdoptReservePort, lPort, rPK, rPort)
	forwardDesc := routing.NewRouteDescriptor(r.conf.PubKey, rPK, lPort, rPort)
	return r.finishDial(log, nrg, rules, leg.hops, nil, forwardDesc, opts, rPK, lPort, rPort), nil
}

// adoptedEdgeRules re-points a detached chain's two edge rules at desc and
// saves them. SaveRule replaces by key, so the chain keeps its route IDs.
func adoptedEdgeRules(rt routing.Table, leg *rehomeLeg, desc routing.RouteDescriptor) (routing.EdgeRules, error) {
	fwd, err := rehomeForwardRule(leg.fwd, desc)
	if err != nil {
		return routing.EdgeRules{}, err
	}
	rvs, err := rehomeConsumeRule(leg.rvs, desc)
	if err != nil {
		return routing.EdgeRules{}, err
	}
	if err := rt.SaveRule(rvs); err != nil {
		return routing.EdgeRules{}, fmt.Errorf("save the adopted consume rule: %w", err)
	}
	if err := rt.SaveRule(fwd); err != nil {
		return routing.EdgeRules{}, fmt.Errorf("save the adopted forward rule: %w", err)
	}
	return routing.EdgeRules{Desc: desc, Forward: fwd, Reverse: rvs}, nil
}

// requestAdopt asks the exit to take this reserve's chain as the tunnel desc
// and, once it has, detaches the chain. On any failure the reserve is left
// exactly as it was.
func (rg *RouteGroup) requestAdopt(desc routing.RouteDescriptor) (*rehomeLeg, error) {
	fwd, tp, err := rg.adoptableChain()
	if err != nil {
		return nil, err
	}
	nonce, err := newRehomeNonce()
	if err != nil {
		return nil, err
	}
	ackCh := make(chan byte, 1)
	rehomeWaiters.Store(nonce, ackCh)
	defer rehomeWaiters.Delete(nonce)

	chain := &rehomeLeg{fwd: fwd, tp: tp}
	if err := rg.writeLegControl(chain, nonce, desc.SrcPort(), desc.DstPort(), routing.LegRehomeAdopt, legControlInBand); err != nil {
		return nil, fmt.Errorf("send adopt request: %w", err)
	}
	var flags byte
	select {
	case flags = <-ackCh:
	case <-time.After(legRehomeAckTimeout()):
		rg.logger.WithField("tp_id", tp.Entry.ID).
			WithField("far_end", rg.farEndPK().String()).
			WithField("first_hop", tp.Remote().String()).
			Infof("Adopt: no ack within %s; the leg reserve is left as it was", legRehomeAckTimeout())
		return nil, fmt.Errorf("%w: no adopt ack from %s within %s; the leg reserve is left as it was",
			ErrRehomeNoAck, rg.farEndPK(), legRehomeAckTimeout())
	}
	if flags&routing.LegRehomeRefused != 0 || flags&routing.LegRehomeAck == 0 {
		return nil, fmt.Errorf("%w: peer %s refused to adopt the leg reserve", ErrRehomeNoAck, rg.farEndPK())
	}
	idx := rg.legIndexByTransport(tp)
	if idx < 0 {
		return nil, fmt.Errorf("the leg reserve's chain on %s is gone", tp.Entry.ID)
	}
	return rg.detachLegForRehome(idx, fmt.Sprintf("adopted as tunnel :%d", desc.DstPort()))
}

// adoptableChain is the reserve's one chain, or why it has none to give.
func (rg *RouteGroup) adoptableChain() (routing.Rule, *transport.ManagedTransport, error) {
	if !rg.legReserve {
		return nil, nil, errors.New("only a leg reserve can be adopted as a tunnel")
	}
	if rg.mux == nil || !rg.mux.legRehomeEnabled {
		return nil, nil, ErrAdoptUnsupported
	}
	rg.mu.Lock()
	defer rg.mu.Unlock()
	if len(rg.tps) != 1 || len(rg.fwd) != 1 || rg.fwd[0] == nil || rg.tps[0] == nil {
		return nil, nil, fmt.Errorf("%w: the leg reserve holds %d chains", ErrAdoptUnsupported, len(rg.tps))
	}
	return rg.fwd[0], rg.tps[0], nil
}

// legIndexByTransport finds the leg riding tp.
func (rg *RouteGroup) legIndexByTransport(tp *transport.ManagedTransport) int {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	for i, held := range rg.tps {
		if held != nil && held.Entry.ID == tp.Entry.ID {
			return i
		}
	}
	return -1
}

// acceptAdopt is the exit half: the request arrived on this reserve's chain and
// names the initiator's tunnel descriptor. Like acceptSplit it never errors — an
// adoption that cannot be carried out is refused and the reserve stays.
func (rg *RouteGroup) acceptAdopt(routeID routing.RouteID, nonce uint64, srcPort, dstPort routing.Port,
	dialect legControlDialect) {
	idx := rg.legIndexByConsumeRule(routeID)
	// The ports are the SENDER's, mirrored onto this side's PK pair.
	desc := routing.NewRouteDescriptor(rg.desc.SrcPK(), rg.desc.DstPK(), dstPort, srcPort)
	host, err := rg.adoptAllowed(idx, desc)
	var leg *rehomeLeg
	var rules routing.EdgeRules
	reason := fmt.Sprintf("adopted as tunnel :%d", desc.DstPort())
	if err == nil {
		leg, err = rg.detachLegForRehome(idx, reason)
	}
	if err == nil {
		if rules, err = adoptedEdgeRules(rg.rt, leg, desc); err != nil {
			if _, rerr := rg.adoptRehomedLeg(leg, reason+" (rolled back)", false); rerr != nil {
				rg.logger.WithError(rerr).Warn("Adopt: the chain could not be rolled back into its reserve")
			}
		}
	}
	if err != nil {
		rg.logger.WithError(err).Debug("Adopt refused")
		rg.replyRehome(idx, nonce, srcPort, dstPort, routing.LegRehomeRefused|routing.LegRehomeAdopt, dialect)
		return
	}
	// Sealed by the reserve: at the initiator the chain is still the
	// reserve's when the ack lands. The rewrite left the next hop alone.
	rg.replyRehomeOn(leg, nonce, srcPort, dstPort, routing.LegRehomeAck|routing.LegRehomeAdopt, dialect)
	host.acceptAdoptedChain(rg, rules)
	rg.noteTunnelAdopted(desc.DstPort())
	rg.logger.Infof("Adopted leg reserve :%d as tunnel %s; handing it to the accept path", rg.desc.DstPort(), desc.String())
	go func() {
		if err := rg.Close(); err != nil {
			rg.logger.WithError(err).Debug("Adopt: closing the emptied leg reserve")
		}
	}()
}

// adoptAllowed reports whether this group may hand its chain over as the
// tunnel desc: it must be a one-chain leg reserve with the capability, on a
// host that can accept it, and nothing may already hold desc.
func (rg *RouteGroup) adoptAllowed(idx int, desc routing.RouteDescriptor) (legAdoptHost, error) {
	if _, _, err := rg.adoptableChain(); err != nil {
		return nil, err
	}
	if idx < 0 {
		return nil, errors.New("no leg holds the request's consume rule")
	}
	host, ok := rg.rehomeHost.(legAdoptHost)
	if !ok || host == nil {
		return nil, ErrAdoptUnsupported
	}
	if other := rg.rehomeHost.rehomeGroupFor(desc); other != nil {
		return nil, fmt.Errorf("a route group already holds %s", desc.String())
	}
	return host, nil
}

// noteTunnelAdopted names why the emptied reserve closes. Not a
// tunnel_consumed event: the reserve's local port is shared with the tunnel it
// was split out of, and the app would read that as its own tunnel leaving.
func (rg *RouteGroup) noteTunnelAdopted(port routing.Port) {
	rg.setCloseReason(fmt.Sprintf("leg reserve adopted: its chain is now tunnel :%d", port))
}
