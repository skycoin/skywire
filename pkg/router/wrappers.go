// Package router pkg/router/wrappers.go
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/rpc"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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
// embedded RSN, transport relay, and DMSG fallback.
func NewSetupNodeDialerFull(embedded EmbeddedSetupNode, relayCache *RSNRelayCache, tm *transport.Manager) RouteGroupDialer {
	var mux *transport.VStreamMux
	if tm != nil {
		log := logging.MustGetLogger("setup_rpc_mux")
		mux = transport.NewVStreamMux(tm, routing.SetupRPCPacket, log)
		tm.SetSetupRPCHandler(mux.HandlePacket)
	}
	return &setupNodeDialer{
		embeddedSetup: embedded,
		relayCache:    relayCache,
		tm:            tm,
		setupRPCMux:   mux,
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
				log.WithField("rsn", sPK.String()[:8]).Debug("Using direct setup transport to RSN")
				rules, relayErr := d.dialViaTransport(ctx, log, tp, req)
				if relayErr == nil {
					return rules, sPK, nil
				}
				log.WithError(relayErr).Debug("Direct setup transport to RSN failed")
			}

			// Try relay through a neighbor.
			tp, relayPK, relayErr := d.relayCache.FindRelayTransport(ctx, sPK, d.tm)
			if relayErr == nil {
				log.WithField("rsn", sPK.String()[:8]).
					WithField("relay", relayPK.String()[:8]).
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

	log.WithField("remote", tp.Remote().String()[:8]).Debug("Dialing RSN via transport virtual stream")

	stream, err := d.setupRPCMux.DialOnTransport(tp)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("vstream dial: %w", err)
	}
	defer stream.Close() //nolint:errcheck

	rpcC := rpc.NewClient(stream)
	defer rpcC.Close() //nolint:errcheck

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
