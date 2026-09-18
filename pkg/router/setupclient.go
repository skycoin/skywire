//go:build !tinygo || (js && wasm)

// Package router pkg/router/setupclient.go c2-net-routing
package router

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
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

	// caps is the setup node's advertised capability list, read once per
	// connection by Capabilities. capsRead distinguishes "not asked yet" from
	// "asked, and the node advertises nothing" (an un-upgraded node).
	capsMu   sync.Mutex
	caps     []string
	capsRead bool
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

// RPCClient returns the underlying net/rpc client so the source-driven
// cascade orchestrator can issue the CascadeSign* RPCs over the same DMSG
// connection used for the legacy DialRouteGroup path.
func (c *SetupClient) RPCClient() *rpc.Client {
	return c.rpc
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

// FetchRelayPeers queries the RSN for its transport peers.
// The visor caches these to enable relay-based route setup without DMSG.
func (c *SetupClient) FetchRelayPeers(ctx context.Context) ([]cipher.PubKey, error) {
	var resp RelayPeersReply
	err := c.call(ctx, rpcName+".RelayPeers", &RelayPeersArgs{}, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Peers, nil
}

// SignTransportQuery asks the RSN to sign a transport-query capability
// targeting dst on behalf of src (see the RSN-oracle 2-hop route path). The
// signed query is carried by the source to dst, which verifies it against its
// trusted-RSN allowlist before returning its transport list.
func (c *SetupClient) SignTransportQuery(ctx context.Context, src, dst cipher.PubKey) (*TransportQuery, error) {
	var resp SignTransportQueryReply
	if err := c.call(ctx, rpcName+".SignTransportQuery",
		&SignTransportQueryArgs{RequesterPK: src, TargetPK: dst}, &resp); err != nil {
		return nil, err
	}
	return resp.Query, nil
}

// Capabilities asks the setup node which request shapes it understands, and
// caches the answer for the life of this client (one client is one connection,
// and a node's capabilities do not change under a live connection).
//
// A setup node that predates the Capabilities RPC answers with net/rpc's
// "can't find method"; that is not an error here, it is the answer — an empty
// capability list, meaning only the original DialRouteGroup. This is what lets
// the batched form ship without a flag on either side.
func (c *SetupClient) Capabilities(ctx context.Context) ([]string, error) {
	c.capsMu.Lock()
	defer c.capsMu.Unlock()
	if c.capsRead {
		return c.caps, nil
	}
	var resp CapabilitiesReply
	err := c.call(ctx, rpcName+".Capabilities", &CapabilitiesArgs{}, &resp)
	switch {
	case err == nil:
		c.caps = resp.Caps
	case isUnimplementedRPC(err):
		c.caps = nil
	default:
		return nil, err
	}
	c.capsRead = true
	return c.caps, nil
}

// DialRouteGroupBatch sets up every route in the batch in ONE request. Only
// call it when Capabilities advertised CapBatchRouteSetup.
//
// A nil error does NOT mean every route installed: the reply carries a result
// per route and partial success is the normal outcome of a batch whose members
// leave over different intermediates.
func (c *SetupClient) DialRouteGroupBatch(ctx context.Context, batch routing.BidirectionalRouteBatch) (routing.BidirectionalRouteBatchReply, error) {
	var resp routing.BidirectionalRouteBatchReply
	err := c.call(ctx, rpcName+".DialRouteGroupBatch", &batch, &resp)
	return resp, err
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
