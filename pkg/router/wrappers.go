// Package router pkg/router/wrappers.go
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/rpc"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

//go:generate mockery --name RouteGroupDialer --case underscore --inpackage

// RouteGroupDialer is an interface for RouteGroup dialers
type RouteGroupDialer interface {
	Dial(
		ctx context.Context,
		log *logging.Logger,
		dmsgC *dmsg.Client,
		setupNodes []cipher.PubKey,
		req routing.BidirectionalRoute,
	) (routing.EdgeRules, cipher.PubKey, error) // Returns rules and the connected setup node PK
}

// EmbeddedSetupNode is an interface for embedded route setup-nodes that can
// create route groups locally without dialing a remote setup-node.
type EmbeddedSetupNode interface {
	// CreateRouteGroup creates a route group using the embedded setup-node's dmsg client.
	CreateRouteGroup(ctx context.Context, biRt routing.BidirectionalRoute) (routing.EdgeRules, error)
	// PK returns the public key of the embedded setup-node.
	PK() cipher.PubKey
	// DmsgClient returns the dmsg client used by the embedded setup-node.
	DmsgClient() *dmsg.Client
}

type setupNodeDialer struct {
	embeddedSetup EmbeddedSetupNode
	relayCache    *RSNRelayCache        // cached RSN relay peers (may be nil)
	tm            *transport.Manager    // for finding relay transports (may be nil)
	setupRPCMux   *transport.VStreamMux // virtual stream mux for RSN RPC (may be nil)

	// srcCascade is the SOURCE-side cascade builder used to inject RSN-signed
	// reserve/install cascades down this visor's own transports. nil when no
	// transport manager is available (e.g. NewSetupNodeDialer).
	srcCascade *CascadeBuilder
}

// CascadeAckRegistry exposes the source-side cascade builder's ack registry
// so the router's CascadeHandler can share it. ACKs for source-driven
// cascades arrive on the visor's single registered cascade handler and must
// be delivered to the source builder's in-flight sends. Returns nil when no
// source-side builder is configured.
//
// This is consumed by the router via the cascadeSourceProvider interface.
func (d *setupNodeDialer) CascadeAckRegistry() *ackRegistry {
	if d.srcCascade == nil {
		return nil
	}
	return d.srcCascade.AckRegistry()
}

// NewSetupNodeDialer returns a wrapper for (*Client).DialRouteGroup.
func NewSetupNodeDialer() RouteGroupDialer {
	return &setupNodeDialer{}
}

// NewSetupNodeDialerWithEmbedded returns a dialer that uses the embedded setup-node
// when available, falling back to remote setup-nodes.
func NewSetupNodeDialerWithEmbedded(embedded EmbeddedSetupNode) RouteGroupDialer {
	return &setupNodeDialer{embeddedSetup: embedded}
}

// NewSetupNodeDialerFull returns a dialer with all capabilities:
// embedded RSN, transport relay, DMSG fallback, and source-driven cascade.
func NewSetupNodeDialerFull(embedded EmbeddedSetupNode, relayCache *RSNRelayCache, tm *transport.Manager) RouteGroupDialer {
	var mux *transport.VStreamMux
	var srcCascade *CascadeBuilder
	if tm != nil {
		log := logging.MustGetLogger("setup_rpc_mux")
		mux = transport.NewVStreamMux(tm, routing.SetupRPCPacket, log)
		tm.SetSetupRPCHandler(mux.HandlePacket)
		// Source-side cascade builder: send-only (zero rsnSK). Its ack
		// registry is shared into the router's CascadeHandler at Serve time.
		srcCascade = NewSourceCascadeBuilder(logging.MustGetLogger("cascade_source"), tm, nil)
	}
	return &setupNodeDialer{
		embeddedSetup: embedded,
		relayCache:    relayCache,
		tm:            tm,
		setupRPCMux:   mux,
		srcCascade:    srcCascade,
	}
}

// Dial dials RouteGroup and returns the connected setup node's public key.
// If an embedded setup-node is configured, it is used first.
func (d *setupNodeDialer) Dial(
	ctx context.Context,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	setupNodes []cipher.PubKey,
	req routing.BidirectionalRoute,
) (routing.EdgeRules, cipher.PubKey, error) {
	// Try embedded setup-node first if available
	if d.embeddedSetup != nil {
		log.Debug("Using embedded route setup-node")
		rules, err := d.embeddedSetup.CreateRouteGroup(ctx, req)
		if err != nil {
			log.WithError(err).Warn("Embedded route setup-node failed, falling back to remote setup-nodes")
		} else {
			return rules, d.embeddedSetup.PK(), nil
		}
	}

	// Try transport-based relay to RSN (avoids DMSG dependency).
	// Check for a direct "setup" transport or a relay through a neighbor.
	if d.tm != nil && d.relayCache != nil {
		for _, sPK := range setupNodes {
			// Try direct "setup" transport first.
			if tp := FindDirectRSNTransport(sPK, d.tm); tp != nil {
				log.WithField("rsn", sPK.String()).Debug("Using direct setup transport to RSN")
				rules, relayErr := d.dialViaTransport(ctx, log, tp, req)
				if relayErr == nil {
					return rules, sPK, nil
				}
				log.WithError(relayErr).Debug("Direct setup transport to RSN failed")
			}

			// Try relay through a neighbor.
			tp, relayPK, relayErr := d.relayCache.FindRelayTransport(ctx, sPK, d.tm)
			if relayErr == nil {
				log.WithField("rsn", sPK.String()).
					WithField("relay", relayPK.String()).
					Debug("Trying relay to RSN via neighbor")
				rules, relayErr := d.dialViaTransport(ctx, log, tp, req)
				if relayErr == nil {
					return rules, sPK, nil
				}
				log.WithError(relayErr).Debug("Relay to RSN via neighbor failed")
			}
		}
	}

	// Fall back to remote setup-nodes via DMSG.
	client, err := NewSetupClient(ctx, log, dmsgC, setupNodes)
	if err != nil {
		return routing.EdgeRules{}, cipher.PubKey{}, err
	}

	connectedNode := client.ConnectedNode()

	defer func() {
		if err := client.Close(); err != nil {
			// Only log unexpected close errors (closed pipe is expected during cleanup)
			if !errors.Is(err, io.ErrClosedPipe) {
				log.WithError(err).Debug("Setup client close returned error")
			}
		}
	}()

	// While connected via DMSG, fetch and cache the RSN's relay peers
	// so future requests can use transport relay instead of DMSG.
	if d.relayCache != nil {
		peers, relayErr := client.FetchRelayPeers(ctx)
		if relayErr == nil && len(peers) > 0 {
			d.relayCache.Update(connectedNode, peers)
		}
	}

	// Prefer the source-driven cascade over the DMSG connection: the RSN
	// signs over DMSG, but the reserve/install cascades flow down OUR
	// transports (not the RSN's). This avoids the RSN having to dial each
	// hop over dmsg (the dmsg-202 failure mode for zombie sessions). Fall
	// back to the legacy DMSG DialRouteGroup if the RSN is un-upgraded.
	if d.srcCascade != nil {
		rules, cascErr := runSourceCascade(ctx, log, client.RPCClient(), d.srcCascade, req)
		if cascErr == nil {
			return rules, connectedNode, nil
		}
		if !errors.Is(cascErr, errCascadeSignUnimplemented) {
			log.WithError(cascErr).Warn("Source-driven cascade failed, falling back to DMSG DialRouteGroup")
		} else {
			log.Debug("RSN lacks source-driven cascade RPCs (dmsg), falling back to DialRouteGroup")
		}
	}

	resp, err := client.DialRouteGroup(ctx, req)
	if err != nil {
		return routing.EdgeRules{}, cipher.PubKey{}, fmt.Errorf("route setup: %w", err)
	}

	return resp, connectedNode, nil
}

// dialViaTransport sends a route setup RPC over a virtual stream on a transport.
// Uses SetupRPCPacket (route ID 0) to carry the RPC bidirectionally.
func (d *setupNodeDialer) dialViaTransport(
	ctx context.Context,
	log *logging.Logger,
	tp *transport.ManagedTransport,
	req routing.BidirectionalRoute,
) (routing.EdgeRules, error) {
	if d.setupRPCMux == nil {
		return routing.EdgeRules{}, fmt.Errorf("setup RPC mux not initialized")
	}

	log.WithField("remote", tp.Remote().String()).Debug("Dialing RSN via transport virtual stream")

	stream, err := d.setupRPCMux.DialOnTransport(tp)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("vstream dial: %w", err)
	}
	defer stream.Close() //nolint:errcheck

	rpcC := rpc.NewClient(stream)
	defer rpcC.Close() //nolint:errcheck

	// Prefer the source-driven cascade: the RSN only signs, and we inject the
	// cascades down our own transports. Fall back to the legacy DialRouteGroup
	// RPC if the RSN doesn't implement the CascadeSign* methods.
	if d.srcCascade != nil {
		rules, cascErr := runSourceCascade(ctx, log, rpcC, d.srcCascade, req)
		if cascErr == nil {
			return rules, nil
		}
		if !errors.Is(cascErr, errCascadeSignUnimplemented) {
			return routing.EdgeRules{}, cascErr
		}
		log.Debug("RSN lacks source-driven cascade RPCs (vstream), falling back to DialRouteGroup")
	}

	var rules routing.EdgeRules
	call := rpcC.Go("SetupRPCGateway.DialRouteGroup", req, &rules, nil)

	select {
	case <-ctx.Done():
		rpcC.Close() //nolint:errcheck,gosec
		return routing.EdgeRules{}, ctx.Err()
	case <-call.Done:
		if call.Error != nil {
			return routing.EdgeRules{}, call.Error
		}
		return rules, nil
	}
}
