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
	log           *logging.Logger
	setupNodes    []cipher.PubKey
	conn          net.Conn
	rpc           *rpc.Client
	connectedNode cipher.PubKey // The setup node that successfully connected
}

// NewSetupClient creates a new SetupClient.
func NewSetupClient(ctx context.Context, log *logging.Logger, dmsgC *dmsg.Client, setupNodes []cipher.PubKey) (*SetupClient, error) {
	client := &SetupClient{
		log:        log,
		setupNodes: setupNodes,
	}

	conn, connectedPK, err := client.dial(ctx, dmsgC)
	if err != nil {
		return nil, err
	}

	client.conn = conn
	client.connectedNode = connectedPK

	client.rpc = rpc.NewClient(conn)

	return client, nil
}

// ConnectedNode returns the public key of the setup node that was successfully connected.
func (c *SetupClient) ConnectedNode() cipher.PubKey {
	return c.connectedNode
}

// perNodeDialTimeout is the maximum time to wait for each individual setup node
const perNodeDialTimeout = 10 * time.Second

func (c *SetupClient) dial(ctx context.Context, dmsgC *dmsg.Client) (net.Conn, cipher.PubKey, error) {
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
				return nil, cipher.PubKey{}, ctx.Err()
			}
			continue
		}

		c.log.Infof("connected to setup node: %s", sPK)
		return conn, sPK, nil
	}

	return nil, cipher.PubKey{}, ErrSetupNode
}

// ReorderSetupNodes moves the given public key to the front of the list.
// This should be called after a successful connection to prioritize working nodes.
func ReorderSetupNodes(nodes []cipher.PubKey, successPK cipher.PubKey) []cipher.PubKey {
	if len(nodes) <= 1 {
		return nodes
	}

	// Find the index of the successful node
	idx := -1
	for i, pk := range nodes {
		if pk == successPK {
			idx = i
			break
		}
	}

	// If not found or already first, no change needed
	if idx <= 0 {
		return nodes
	}

	// Move to front: [a, b, SUCCESS, c] -> [SUCCESS, a, b, c]
	result := make([]cipher.PubKey, len(nodes))
	result[0] = successPK
	copy(result[1:idx+1], nodes[:idx])
	copy(result[idx+1:], nodes[idx+1:])
	return result
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
