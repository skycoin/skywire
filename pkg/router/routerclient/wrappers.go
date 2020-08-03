package routerclient

import (
	"context"
	"fmt"
	"net"

	"github.com/skycoin/skywire/pkg/snet"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/routing"
)

// TODO: remove this
// dmsgClientWrapper is a temporary workaround to make dmsg client implement `snet.Dialer`.
// The only reason to use this is because client's `Dial` returns `*dmsg.Stream` instead of `net.Conn`,
// so this stuff should be removed as soon as the func's signature changes
type dmsgClientWrapper struct {
	*dmsg.Client
}

func wrapDmsgC(dmsgC *dmsg.Client) *dmsgClientWrapper {
	return &dmsgClientWrapper{Client: dmsgC}
}

func (w *dmsgClientWrapper) Dial(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error) {
	addr := dmsg.Addr{
		PK:   remote,
		Port: port,
	}

	return w.Client.Dial(ctx, addr)
}

func (w *dmsgClientWrapper) Type() string {
	return snet.DmsgType
}

// AddEdgeRules is a wrapper for (*Client).AddEdgeRules.
func AddEdgeRules(
	ctx context.Context,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	pk cipher.PubKey,
	rules routing.EdgeRules,
) (bool, error) {
	client, err := NewClient(ctx, wrapDmsgC(dmsgC), pk)
	if err != nil {
		return false, fmt.Errorf("failed to dial remote: %v", err)
	}

	defer closeClient(log, client)

	ok, err := client.AddEdgeRules(ctx, rules)
	if err != nil {
		return false, fmt.Errorf("failed to add rules: %v", err)
	}

	return ok, nil
}

// AddIntermediaryRules is a wrapper for (*Client).AddIntermediaryRules.
func AddIntermediaryRules(
	ctx context.Context,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	pk cipher.PubKey,
	rules []routing.Rule,
) (bool, error) {
	client, err := NewClient(ctx, wrapDmsgC(dmsgC), pk)
	if err != nil {
		return false, fmt.Errorf("failed to dial remote: %v", err)
	}

	defer closeClient(log, client)

	routeIDs, err := client.AddIntermediaryRules(ctx, rules)
	if err != nil {
		return false, fmt.Errorf("failed to add rules: %v", err)
	}

	return routeIDs, nil
}

// ReserveIDs is a wrapper for (*Client).ReserveIDs.
func ReserveIDs(
	ctx context.Context,
	log *logging.Logger,
	dmsgC *dmsg.Client,
	pk cipher.PubKey,
	n uint8,
) ([]routing.RouteID, error) {
	client, err := NewClient(ctx, wrapDmsgC(dmsgC), pk)
	if err != nil {
		return nil, fmt.Errorf("failed to dial remote: %v", err)
	}

	defer closeClient(log, client)

	routeIDs, err := client.ReserveIDs(ctx, n)
	if err != nil {
		return nil, fmt.Errorf("failed to add rules: %v", err)
	}

	return routeIDs, nil
}

func closeClient(log *logging.Logger, client *Client) {
	if err := client.Close(); err != nil {
		log.Warn(err)
	}
}
