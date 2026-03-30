// router_serve.go contains functions for serving and accepting routes.
package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/noise"

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
			Err:    errors.New("use of closed network connection"),
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

	nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf)
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

	go r.serveTransportManager(ctx)

	go r.serveSetup()

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
			r.logger.Warnf("closing conn from untrusted setup node: %v", conn.Close())
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
func (r *router) saveRouteGroupRules(ctx context.Context, rules routing.EdgeRules, nsConf noise.Config) (*NoiseRouteGroup, error) {
	// Check context before starting
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	r.logger.Debugf("Saving route group rules with desc: %s", &rules.Desc)

	// When route group is wrapped with noise, it's put into `nrgs`. but before that,
	// in the process of wrapping we still need to use this route group to handle
	// handshake packets. so we keep these not-yet wrapped rgs in the `rgsRaw`
	// until they get wrapped with noise

	r.mx.Lock()

	// first ensure that this rg is not being wrapped with noise right now
	if _, ok := r.rgsRaw[rules.Desc]; ok {
		r.mx.Unlock()
		r.logger.Warnf("Desc %s already reserved, skipping...", rules.Desc)
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
	rg.appendRules(rules.Forward, rules.Reverse, tp)
	// we put raw rg so it can be accessible to the router when handshake packets come in
	r.rgsRaw[rules.Desc] = rg
	r.mx.Unlock()

	if nsConf.Initiator {
		if err := rg.sendHandshake(true); err != nil {
			r.logger.WithError(err).Errorf("Failed to send handshake from route group (%s): %v, closing...",
				&rules.Desc, err)
			if err := rg.Close(); err != nil {
				r.logger.WithError(err).Errorf("Failed to close route group (%s): %v", &rules.Desc, err)
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

	select {
	case <-rg.handshakeProcessed:
	case <-hsCtx.Done():
		// Check if parent context was canceled (e.g., setup timeout)
		if ctx.Err() != nil {
			r.logger.Debugf("Route group setup canceled for %s: %v", &rules.Desc, ctx.Err())
			// Clean up
			if err := rg.Close(); err != nil {
				r.logger.WithError(err).Warnf("Failed to close route group on context cancellation")
			}
			r.mx.Lock()
			delete(r.rgsRaw, rules.Desc)
			r.mx.Unlock()
			return nil, ctx.Err()
		}
		// remote should send handshake packet during initialization,
		// if no packet received during timeout interval, we're dealing
		// with the old visor
		rg.handshakeProcessedOnce.Do(func() {
			rg.encrypt = false
			close(rg.handshakeProcessed)
		})
	}

	if !nsConf.Initiator {
		if err := rg.sendHandshake(true); err != nil {
			r.logger.WithError(err).Errorf("Failed to send handshake from route group (%s): %v, closing...",
				&rules.Desc, err)
			if err := rg.Close(); err != nil {
				r.logger.WithError(err).Errorf("Failed to close route group (%s): %v", &rules.Desc, err)
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
		r.logger.Debugf("Noise route group with desc %s already exists, closing the old one and replacing...", &rules.Desc)

		if err := nrg.Close(); err != nil {
			r.logger.Errorf("Error closing already existing noise route group: %v", err)
		}

		r.logger.Debugf("Successfully closed old noise route group")
	}

	if rg.encrypt {
		// wrapping rg with noise
		wrappedRG, err := network.EncryptConn(nsConf, rg)
		if err != nil {
			r.logger.WithError(err).Errorf("Failed to wrap route group (%s): %v, closing...", &rules.Desc, err)
			if err := rg.Close(); err != nil {
				r.logger.WithError(err).Errorf("Failed to close route group (%s): %v", &rules.Desc, err)
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

	return nrg, nil
}
