// Package router pkg/router/setupclient.go
package router

import (
	"context"
	"errors"
	"net"
	"net/rpc"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const rpcName = "SetupRPCGateway"

// ErrSetupNode is used when the visor is unable to connect to a setup node
var ErrSetupNode = errors.New("failed to dial to a setup node")

// SetupClient is an RPC client for setup node.
type SetupClient struct {
	log        *logging.Logger
	setupNodes []cipher.PubKey
	conn       net.Conn
	rpc        *rpc.Client
}

// NewSetupClient creates a new SetupClient.
func NewSetupClient(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client, setupNodes []cipher.PubKey) (*SetupClient, error) {
	client := &SetupClient{
		log:        log,
		setupNodes: setupNodes,
	}

	conn, err := client.dial(ctx, dmsgC)
	if err != nil {
		return nil, err
	}

	client.conn = conn

	client.rpc = rpc.NewClient(conn)

	return client, nil
}

func (c *SetupClient) dial(ctx context.Context, dmsgC *dmsg.Client) (net.Conn, error) {
	for _, sPK := range c.setupNodes {
		addr := dmsg.Addr{PK: sPK, Port: skyenv.DmsgSetupPort}
		conn, err := dmsgC.Dial(ctx, addr)
		if err != nil {
			c.log.WithError(err).Warnf("failed to dial to setup node: setupPK(%s)", sPK)
			continue
		}

		return conn, nil
	}

	return nil, ErrSetupNode
}

// Close closes a Client.
func (c *SetupClient) Close() error {
	if c == nil {
		return nil
	}

	if err := c.rpc.Close(); err != nil {
		return err
	}

	return c.conn.Close()
}

// DialRouteGroup generates rules for routes from a visor and sends them to visors.
func (c *SetupClient) DialRouteGroup(ctx context.Context, req routing.BidirectionalRoute) (routing.EdgeRules, error) {
	var resp routing.EdgeRules
	err := c.call(ctx, rpcName+".DialRouteGroup", req, &resp)

	return resp, err
}

func (c *SetupClient) call(ctx context.Context, serviceMethod string, args interface{}, reply interface{}) error {
	call := c.rpc.Go(serviceMethod, args, reply, nil)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-call.Done:
		return call.Error
	}
}
