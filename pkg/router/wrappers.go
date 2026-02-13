// Package router pkg/router/wrappers.go
package router

import (
	"context"
	"fmt"

	"github.com/skycoin/dmsg/pkg/dmsg"

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

type setupNodeDialer struct{}

// NewSetupNodeDialer returns a wrapper for (*Client).DialRouteGroup.
func NewSetupNodeDialer() RouteGroupDialer {
	return new(setupNodeDialer)
}

// Dial dials RouteGroup and returns the connected setup node's public key.
func (d *setupNodeDialer) Dial(
	ctx context.Context,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	setupNodes []cipher.PubKey,
	req routing.BidirectionalRoute,
) (routing.EdgeRules, cipher.PubKey, error) {
	client, err := NewSetupClient(ctx, log, dmsgC, setupNodes)
	if err != nil {
		return routing.EdgeRules{}, cipher.PubKey{}, err
	}

	connectedNode := client.ConnectedNode()

	defer func() {
		if err := client.Close(); err != nil {
			log.Warn(err)
		}
	}()

	resp, err := client.DialRouteGroup(ctx, req)
	if err != nil {
		return routing.EdgeRules{}, cipher.PubKey{}, fmt.Errorf("route setup: %w", err)
	}

	return resp, connectedNode, nil
}
