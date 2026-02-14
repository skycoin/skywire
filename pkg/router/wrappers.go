// Package router pkg/router/wrappers.go
package router

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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

	// Fall back to remote setup-nodes
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

	resp, err := client.DialRouteGroup(ctx, req)
	if err != nil {
		return routing.EdgeRules{}, cipher.PubKey{}, fmt.Errorf("route setup: %w", err)
	}

	return resp, connectedNode, nil
}

// DirectRouteSetup creates a route group directly using the provided dmsg client,
// bypassing the setup-node RPC call. This is useful for local setup-node integration.
func DirectRouteSetup(ctx context.Context, dmsgC *dmsg.Client, biRt routing.BidirectionalRoute) (routing.EdgeRules, error) {
	dialer := WrapDmsgClient(dmsgC)
	metrics := setupmetrics.NewEmpty()
	return CreateRouteGroup(ctx, dialer, biRt, metrics)
}
