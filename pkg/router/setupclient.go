// Package router pkg/router/setupclient.go
package router

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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

// perNodeDialTimeout is the maximum time to wait for each individual setup node
const perNodeDialTimeout = 10 * time.Second

func (c *SetupClient) dial(ctx context.Context, dmsgC *dmsg.Client) (net.Conn, error) {
	for _, sPK := range c.setupNodes {
		addr := dmsg.Addr{PK: sPK, Port: skyenv.DmsgSetupPort}

		// Use per-node timeout to prevent one slow/dead node from blocking others
		dialCtx, cancel := context.WithTimeout(ctx, perNodeDialTimeout)
		conn, err := dmsgC.Dial(dialCtx, addr)
		cancel() // Always cancel to avoid context leak

		if err != nil {
			c.log.WithError(err).Warnf("failed to dial to setup node: setupPK(%s)", sPK)
			// Check if parent context was canceled
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
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

// HealthCheck checks setup node to verify it's responsive.
func (c *SetupClient) HealthCheck(ctx context.Context) (string, error) {
	var reply HealthCheckReply
	err := c.call(ctx, rpcName+".HealthCheck", &HealthCheckArgs{}, &reply)
	if err != nil {
		return "", err
	}
	return reply.Status, nil
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
