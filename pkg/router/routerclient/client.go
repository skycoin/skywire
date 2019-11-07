package routerclient

import (
	"context"
	"net/rpc"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
)

const rpcName = "RPCGateway"

// Client is an RPC client for router.
type Client struct {
	s   *dmsg.Stream
	rpc *rpc.Client
}

// NewClient creates a new Client.
func NewClient(ctx context.Context, dmsgC *dmsg.Client, pk cipher.PubKey) (*Client, error) {
	s, err := dmsgC.Dial(ctx, pk, snet.AwaitSetupPort)
	if err != nil {
		return nil, err
	}

	client := &Client{
		s:   s,
		rpc: rpc.NewClient(s.Conn),
	}
	return client, nil
}

// Close closes a Client.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	if err := c.s.Close(); err != nil {
		return err
	}

	if err := c.rpc.Close(); err != nil {
		return err
	}

	return nil
}

// AddEdgeRules adds forward and consume rules to router (forward and reverse).
// TODO(nkryuchkov): make sure that deadline functions are used, then get rid of context here and below
func (c *Client) AddEdgeRules(ctx context.Context, rules routing.EdgeRules) (bool, error) {
	var ok bool
	err := c.call(ctx, rpcName+".AddEdgeRules", rules, &ok)

	return ok, err
}

// AddIntermediaryRules adds intermediary rules to router.
func (c *Client) AddIntermediaryRules(ctx context.Context, rules []routing.Rule) (bool, error) {
	var ok bool
	err := c.call(ctx, rpcName+".AddIntermediaryRules", rules, &ok)

	return ok, err
}

// ReserveIDs reserves n IDs and returns them.
func (c *Client) ReserveIDs(ctx context.Context, n uint8) ([]routing.RouteID, error) {
	var routeIDs []routing.RouteID
	err := c.call(ctx, rpcName+".ReserveIDs", n, &routeIDs)

	return routeIDs, err
}

func (c *Client) call(ctx context.Context, serviceMethod string, args interface{}, reply interface{}) error {
	call := c.rpc.Go(serviceMethod, args, reply, nil)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-call.Done:
		return call.Error
	}
}
