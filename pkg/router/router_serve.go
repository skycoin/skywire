// Package router pkg/router/router_serve.go c2-net-routing
// router_serve.go contains functions for serving and accepting routes.
package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// AcceptRoutes should block until we receive an AddRules packet from SetupNode
// that contains ConsumeRule(s) or ForwardRule(s).
// Then the following should happen:
// - Save to routing.Table and internal RouteGroup map.
// - Return the RoutingGroup.
func (r *router) AcceptRoutes(ctx context.Context) (net.Conn, error) {
	var (
		rules routing.EdgeRules
		ok    bool
	)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case rules, ok = <-r.accept:
	}

	if !ok {
		err := &net.OpError{
			Op:     "accept",
			Net:    "skynet",
			Source: nil,
			Err:    net.ErrClosed,
		}

		return nil, err
	}

	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		return nil, fmt.Errorf("SaveRoutingRules: %w", err)
	}

	nsConf := noise.Config{
		LocalPK:   r.conf.PubKey,
		LocalSK:   r.conf.SecKey,
		RemotePK:  rules.Desc.SrcPK(),
		Initiator: false,
	}

	// IntroduceRules / accepted-route path: no app context to thread.
	// Accept-side datagram intent comes from the local serving port
	// being registered as faithful-UDP-capable (RegisterDatagramPort).
	datagram := r.isDatagramPort(rules.Desc.DstPort())
	nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf, "", datagram)
	if err != nil {
		// Clean up saved rules if route group setup fails
		r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
		return nil, fmt.Errorf("saveRouteGroupRules: %w", err)
	}

	nrg.rg.startOffServiceLoops()

	return nrg, nil
}

// Serve starts transport listening loop.
func (r *router) Serve(ctx context.Context) error {
	r.logger.Debug("Starting router")

	// Initialize cascade handler so visors can process cascade route
	// setup messages arriving on route ID 0 of any transport.
	trustedPKs := make([]cipher.PubKey, 0, len(r.trustedVisors))
	for pk := range r.trustedVisors {
		trustedPKs = append(trustedPKs, pk)
	}
	ch := NewCascadeHandler(r.logger, r.conf.PubKey, trustedPKs, r.rt, r.tm, r.IntroduceRules)

	// Source-driven cascade: the visor's route-group dialer owns a send-only
	// CascadeBuilder that injects RSN-signed cascades down this visor's own
	// transports. ACKs for those sends arrive on this single registered
	// cascade handler, so share the dialer's ack registry into it and into
	// the builder so the handler can deliver ACKs to in-flight sends.
	if r.conf != nil {
		if sp, ok := r.conf.RouteGroupDialer.(cascadeSourceProvider); ok {
			if reg := sp.CascadeAckRegistry(); reg != nil {
				ch.acks = reg
				r.logger.Debug("Cascade handler sharing source-driven ack registry")
			}
		}
		// Wire the handler in as the dialer's source-origin processor so the
		// source consumes the outermost (source-addressed) cascade layer
		// itself — reserving its own route IDs and relaying inward — instead
		// of shipping it to the first hop (which would reject the signature).
		if os, ok := r.conf.RouteGroupDialer.(cascadeOriginSetter); ok {
			os.SetCascadeOrigin(ch)
			r.logger.Debug("Cascade handler wired as source-origin processor")
		}
	}

	r.tm.SetCascadeHandler(ch.HandlePacket)
	r.logger.WithField("trusted_rsns", len(trustedPKs)).Debug("Cascade handler registered")

	go r.serveTransportManager(ctx)

	go r.serveSetup()

	// Reclaim frames parked during the route-group registration window whose
	// route group never registers (see router_pending.go).
	go r.pending.runSweep(ctx, r.logger)

	return nil
}

func (r *router) serveTransportManager(ctx context.Context) {
	for {
		// Check context before blocking on ReadPacket
		select {
		case <-ctx.Done():
			r.logger.Debug("Context canceled, stopping transport manager serve loop")
			return
		default:
		}

		packet, err := r.tm.ReadPacket()
		if err != nil {
			if err == transport.ErrNotServing {
				r.logger.WithError(err).Debug("Stopped reading packets")
				return
			}

			r.logger.WithError(err).Error("Stopped reading packets due to unexpected error.")
			return
		}

		if err := r.handleTransportPacket(ctx, packet); err != nil {
			if err == transport.ErrNotServing {
				r.logger.WithError(err).Warnf("Stopped serving Transport.")
				return
			}

			r.logger.Warnf("Failed to handle transport frame: %v", err)
		}
	}
}

func (r *router) serveSetup() {
	for {
		// Check shutdown before blocking on AcceptStream
		select {
		case <-r.done:
			r.logger.Debug("Router closed, stopping setup serve loop")
			return
		default:
		}

		conn, err := r.sl.AcceptStream()
		if err != nil {
			log := r.logger.WithError(err)
			if errors.Is(err, dmsg.ErrEntityClosed) {
				log.Debug("Setup client stopped serving.")
				return
			}
			log.Error("Setup client stopped serving due to unexpected error.")
			return
		}

		remotePK := conn.RawRemoteAddr().PK
		if !r.SetupIsTrusted(remotePK) {
			closeErr := conn.Close()
			r.logger.WithField("remote_pk", remotePK).
				WithField("close_err", closeErr).
				Warn("Closing conn from untrusted setup node")
			continue
		}

		r.logger.Debugf("handling setup request: setupPK(%s)", remotePK)

		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					r.logger.Errorf("Panic in router RPC handler: %v", rec)
				}
				conn.Close() //nolint:errcheck,gosec
			}()
			conn.SetDeadline(time.Now().Add(2 * time.Minute)) //nolint:errcheck,gosec
			r.rpcSrv.ServeConn(conn)
		}()
	}
}

// nolint: gocyclo
//
//gocyclo:ignore
// awaitHandshakeAck blocks until the peer's reciprocal setup handshake closes
// processed, or ctx expires (returning ctx.Err()). When retransmit is non-nil
// and interval > 0 (the initiator), it re-invokes retransmit every interval so
// a single lost setup-handshake packet on the multi-hop route recovers within
// the interval instead of eating the whole handshake-await timeout and dropping
// a live dial. The responder passes retransmit == nil: it has nothing to
// re-send until the forward handshake arrives (whereupon processed closes).
func awaitHandshakeAck(ctx context.Context, processed <-chan struct{}, retransmit func(), interval time.Duration) error {
	var tickC <-chan time.Time
	if retransmit != nil && interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		tickC = ticker.C
	}
	for {
		select {
		case <-processed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-tickC:
			retransmit()
		}
	}
}

func (r *router) saveRouteGroupRules(ctx context.Context, rules routing.EdgeRules, nsConf noise.Config, appName string, datagram bool) (*NoiseRouteGroup, error) {
	// Check context before starting
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var log *logging.Logger
	if appName != "" {
		log = r.logger.WithAppName(appName)
	} else {
		log = r.scopedLog(rules.Desc.SrcPort())
	}
	log.Debugf("Saving route group rules with desc: %s", &rules.Desc)

	// When route group is wrapped with noise, it's put into `nrgs`. but before that,
	// in the process of wrapping we still need to use this route group to handle
	// handshake packets. so we keep these not-yet wrapped rgs in the `rgsRaw`
	// until they get wrapped with noise

	r.mx.Lock()

	// first ensure that this rg is not being wrapped with noise right now
	if _, ok := r.rgsRaw[rules.Desc]; ok {
		r.mx.Unlock()
		log.Warnf("Desc %s already reserved, skipping...", &rules.Desc)
		return nil, fmt.Errorf("noise route group with desc %s already being initialized", &rules.Desc)
	}

	// we need to close currently existing wrapped rg if there's one
	nrg, ok := r.rgsNs[rules.Desc]

	// Look up the transport for the first hop - fail early if not available
	nextTpID := rules.Forward.NextTransportID()
	tp := r.tm.Transport(nextTpID)
	if tp == nil {
		r.mx.Unlock()
		return nil, fmt.Errorf("transport %s not found locally", nextTpID)
	}

	rg := NewRouteGroup(DefaultRouteGroupConfig(), r.rt, rules.Desc, r.mLogger)
	rg.SetAppName(appName)
	rg.initiator = nsConf.Initiator
	rg.appendRules(rules.Forward, rules.Reverse, tp)
	// we put raw rg so it can be accessible to the router when handshake packets come in
	r.rgsRaw[rules.Desc] = rg
	r.mx.Unlock()

	// Re-dispatch any frames that arrived for this descriptor during the
	// rule-save -> registration window (the receive-side setup race). They were
	// parked instead of dropped; deliver them now that the route group exists so
	// a handshake frame that raced registration still completes the handshake.
	for _, pp := range r.pending.take(rules.Desc) {
		if err := rg.handlePacket(pp.pkt); err != nil {
			log.WithError(err).Debug("Failed to re-dispatch parked packet after route group registration")
		}
	}

	if nsConf.Initiator {
		if err := rg.sendHandshake(true); err != nil {
			log.WithError(err).Errorf("Failed to send handshake from route group (%s): %v, closing...",
				&rules.Desc, err)
			if err := rg.Close(); err != nil {
				log.WithError(err).Errorf("Failed to close route group (%s): %v", &rules.Desc, err)
			}
			// Clean up rgsRaw on failure to prevent blocking future connections
			r.mx.Lock()
			delete(r.rgsRaw, rules.Desc)
			r.mx.Unlock()

			return nil, fmt.Errorf("sendHandshake (%s): %w", &rules.Desc, err)
		}
	}

	// Use parent context for overall timeout, with handshake-specific timeout as fallback
	hsCtx, hsCancel := context.WithTimeout(ctx, handshakeAwaitTimeout)
	defer hsCancel()

	// The initiator re-sends its handshake on handshakeRetransmitInterval while
	// waiting for the reciprocal handshake: the setup handshake is a single
	// unreliable packet over the multi-hop route with no ARQ (the mux reorder/SACK
	// layer does not exist yet), so a single lost handshake packet would otherwise
	// eat the whole handshakeAwaitTimeout and drop a live dial. The responder
	// passes no retransmit — it has nothing to re-send until it has received the
	// forward handshake (after which it re-acks from handlePacket on any duplicate).
	var retransmit func()
	if nsConf.Initiator {
		retransmit = func() {
			if err := rg.sendHandshake(true); err != nil {
				log.WithError(err).Debugf("Handshake retransmit failed for %s", &rules.Desc)
			}
		}
	}

	if err := awaitHandshakeAck(hsCtx, rg.handshakeProcessed, retransmit, handshakeRetransmitInterval); err != nil {
		// Check if parent context was canceled (e.g., setup timeout)
		if ctx.Err() != nil {
			log.Debugf("Route group setup canceled for %s: %v", &rules.Desc, ctx.Err())
			// Clean up
			if err := rg.Close(); err != nil {
				log.WithError(err).Warnf("Failed to close route group on context cancellation")
			}
			r.mx.Lock()
			delete(r.rgsRaw, rules.Desc)
			r.mx.Unlock()
			return nil, ctx.Err()
		}
		// Remote never sent its reciprocal handshake. Historically this
		// branch silently downgraded encrypt=false on the assumption that
		// the remote was an old visor without handshake support — but
		// every supported visor does the handshake now, so what this
		// silence actually masks is "remote app isn't there / transport
		// is one-way / whitelist denied". The route group then runs in a
		// half-open state (rules saved on both sides per the setup-node
		// but no real peer), and the operator sees a 'running' app that
		// silently black-holes for ~90s before the keep-alive gives up.
		//
		// Hard-fail loudly so the dial returns a real error to the caller
		// and the operator can see "remote not responding" instead of a
		// phantom success.
		// Enrich the failure with this side's full rule linkage so an initiator
		// timeout and the peer's wrap-failure log can be compared to detect a
		// reverse-leg route-ID mismatch. Both rules are rendered via the
		// panic-safe Rule.String() (keyRtID/nxtRtID/nxtTpID per rule type);
		// calling NextRouteID()/NextTransportID() directly panics on a Consume
		// (reverse) rule.
		log.WithField("timeout", handshakeAwaitTimeout).
			WithField("fwd_rule", rules.Forward.String()).
			WithField("rev_rule", rules.Reverse.String()).
			Warn("Remote handshake not received within timeout — failing dial")
		// Clean up rgsRaw + delete the rules we installed locally so the
		// keyRouteIDs are free for the next attempt.
		if err := rg.Close(); err != nil {
			log.WithError(err).Warnf("Failed to close route group on handshake timeout")
		}
		r.mx.Lock()
		delete(r.rgsRaw, rules.Desc)
		r.mx.Unlock()
		r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
		return nil, fmt.Errorf("remote handshake not received within %s (peer %s likely unreachable, app down, or whitelist-rejected)",
			handshakeAwaitTimeout, rules.Desc.DstPK())
	}

	if !nsConf.Initiator {
		if err := rg.sendHandshake(true); err != nil {
			log.WithError(err).Errorf("Failed to send handshake from route group (%s): %v, closing...",
				&rules.Desc, err)
			if err := rg.Close(); err != nil {
				log.WithError(err).Errorf("Failed to close route group (%s): %v", &rules.Desc, err)
			}
			// Clean up rgsRaw on failure to prevent blocking future connections
			r.mx.Lock()
			delete(r.rgsRaw, rules.Desc)
			r.mx.Unlock()

			return nil, fmt.Errorf("sendHandshake (%s): %w", &rules.Desc, err)
		}
	}

	if ok && nrg != nil {
		// if already functioning wrapped rg exists, we safely close it here
		log.Debugf("Noise route group with desc %s already exists, closing the old one and replacing...", &rules.Desc)

		if err := nrg.Close(); err != nil {
			log.Errorf("Error closing already existing noise route group: %v", err)
		}

		log.Debugf("Successfully closed old noise route group")
	}

	if rg.encrypt {
		// wrapping rg with noise
		wrappedRG, err := network.EncryptConn(nsConf, rg)
		if err != nil {
			// Rule linkage to pair with the initiator's "Remote handshake not
			// received" log. Both rules are rendered via the panic-safe
			// Rule.String() (it prints keyRtID/nxtRtID/nxtTpID per rule type);
			// calling type-specific accessors like NextRouteID()/NextTransportID()
			// directly panics on a Consume (reverse) rule. A "no data captured"
			// wrap-failure usually means the initiator already timed out and never
			// sent the noise first message.
			log.WithError(err).
				WithField("fwd_rule", rules.Forward.String()).
				WithField("rev_rule", rules.Reverse.String()).
				Errorf("Failed to wrap route group (%s): %v, closing...", &rules.Desc, err)
			if err := rg.Close(); err != nil {
				log.WithError(err).Errorf("Failed to close route group (%s): %v", &rules.Desc, err)
			}
			// Clean up rgsRaw on failure to prevent blocking future connections
			r.mx.Lock()
			delete(r.rgsRaw, rules.Desc)
			r.mx.Unlock()

			return nil, fmt.Errorf("WrapConn (%s): %w", &rules.Desc, err)
		}

		nrg = &NoiseRouteGroup{
			rg:   rg,
			Conn: wrappedRG,
		}
	} else {
		nrg = &NoiseRouteGroup{
			rg:   rg,
			Conn: rg,
		}
	}

	r.mx.Lock()
	// put ready nrg and remove raw rg, we won't need it anymore
	r.rgsNs[rules.Desc] = nrg
	delete(r.rgsRaw, rules.Desc)
	r.mx.Unlock()

	// Faithful-UDP (#2607): when this end intends datagram mode for the
	// route, build a DatagramRouteGroup sibling over the same rules +
	// transport, keyed off the reliable session's Noise ChannelBinding.
	// Best-effort and additive — never affects the reliable route.
	if datagram {
		r.buildDatagramSibling(rules, nsConf, nrg.Conn, tp)
	}

	return nrg, nil
}
