// Package visor pkg/visor/embedded_route_setup.go
package visor

import (
	"context"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// EmbeddedRouteSetup holds the state of an embedded Route Setup Node.
// It uses a separate dmsg client with its own PK/SK identity to dial
// remote visors and set up routes locally without needing to contact
// a remote setup-node over dmsg.
type EmbeddedRouteSetup struct {
	dmsgC *dmsg.Client
	pk    cipher.PubKey
	log   *logging.Logger
}

// CreateRouteGroup creates a route group by directly calling the setup-node logic.
// This bypasses the need to dial a remote setup-node over dmsg.
func (ers *EmbeddedRouteSetup) CreateRouteGroup(ctx context.Context, biRt routing.BidirectionalRoute) (routing.EdgeRules, error) {
	ers.log.WithField("src", biRt.Desc.SrcPK()).WithField("dst", biRt.Desc.DstPK()).
		Debug("Creating route group via embedded setup-node")

	dialer := router.WrapDmsgClient(ers.dmsgC)
	metrics := setupmetrics.NewEmpty()

	return router.CreateRouteGroup(ctx, dialer, biRt, metrics)
}

// PK returns the public key of the embedded route setup-node.
func (ers *EmbeddedRouteSetup) PK() cipher.PubKey {
	return ers.pk
}

// DmsgClient returns the dmsg client used by the embedded route setup-node.
func (ers *EmbeddedRouteSetup) DmsgClient() *dmsg.Client {
	return ers.dmsgC
}
