// Package setupclient pkg/setup/setupclient/wrappers.go
package setupclient

import (
	"context"
	"fmt"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

//go:generate mockery -name RouteGroupDialer -case underscore -inpkg

// RouteGroupDialer is an interface for RouteGroup dialers
type RouteGroupDialer interface {
	Dial(
		ctx context.Context,
		log *logging.Logger,
		dmsgC *dmsg.Client,
		setupNodes []cipher.PubKey,
		req routing.BidirectionalRoute,
	) (routing.EdgeRules, error)
}

type setupNodeDialer struct{}

// NewSetupNodeDialer returns a wrapper for (*Client).DialRouteGroup.
func NewSetupNodeDialer() RouteGroupDialer {
	return new(setupNodeDialer)
}

// Dial dials RouteGroup.
func (d *setupNodeDialer) Dial(
	ctx context.Context,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	setupNodes []cipher.PubKey,
	req routing.BidirectionalRoute,
) (routing.EdgeRules, error) {
	client, err := NewClient(ctx, log, dmsgC, setupNodes)
	if err != nil {
		return routing.EdgeRules{}, err
	}

	defer func() {
		if err := client.Close(); err != nil {
			log.Warn(err)
		}
	}()

	resp, err := client.DialRouteGroup(ctx, req)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("route setup: %w", err)
	}

	return resp, nil
}
