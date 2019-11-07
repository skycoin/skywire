package setupclient

import (
	"context"
	"fmt"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
)

// DialRouteGroup is a wrapper for (*Client).DialRouteGroup.
func DialRouteGroup(ctx context.Context, log *logging.Logger, n *snet.Network, setupNodes []cipher.PubKey,
	req routing.BidirectionalRoute) (routing.EdgeRules, error) {

	client, err := NewClient(ctx, log, n, setupNodes)
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
		return routing.EdgeRules{}, fmt.Errorf("route setup: %s", err)
	}

	return resp, nil
}
