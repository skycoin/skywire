// Package router routerclient.go
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/rpc"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// RPCName is the RPC gateway object name.
const RPCName = "RPCGateway"

// Client is used to interact with the router's API remotely. The setup node uses this.
type Client struct {
	rpc *rpc.Client
	rPK cipher.PubKey // public key of remote router
	log logrus.FieldLogger
}

// DialTimeout is the maximum time allowed for a single dial attempt to a remote visor.
// Without this, dials to unreachable visors block indefinitely in DialStream.readResponse,
// leaking goroutines. This caps the dial itself; the RPC deadline is set separately.
// Keep this short to free ephemeral ports quickly under load.
const DialTimeout = 10 * time.Second

// NewClient creates a new Client.
func NewClient(ctx context.Context, dialer network.Dialer, rPK cipher.PubKey) (*Client, error) {
	dialCtx, dialCancel := context.WithTimeout(ctx, DialTimeout)
	defer dialCancel()

	s, err := dialer.Dial(dialCtx, rPK, skyenv.DmsgAwaitSetupPort)
	if err != nil {
		return nil, fmt.Errorf("dial %v@%v: %w", rPK, skyenv.DmsgAwaitSetupPort, err)
	}

	// Set a deadline on the underlying connection to prevent stale DMSG streams
	// from accumulating when the remote visor is dead. Without this, RPC calls
	// over dead streams block forever, leaking goroutines and ephemeral ports.
	// Keep this short — stuck RPC calls hold ephemeral ports until the deadline
	// fires. 30s is enough for any valid RPC exchange.
	if conn, ok := s.(interface{ SetDeadline(time.Time) error }); ok {
		conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	}

	return NewClientFromRaw(s, rPK), nil
}

// NewClientFromRaw creates a new client from a raw connection.
func NewClientFromRaw(conn io.ReadWriteCloser, rPK cipher.PubKey) *Client {
	return &Client{
		rpc: rpc.NewClient(conn),
		rPK: rPK,
		log: logging.MustGetLogger(fmt.Sprintf("router_client:%s", rPK.String())),
	}
}

// Close closes a Client. Safe to call multiple times.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	err := c.rpc.Close()
	// Ignore "connection is shut down" — the RPC client was already closed
	// (e.g., by context cancellation in call()).
	if err != nil && err.Error() == "connection is shut down" {
		return nil
	}
	return err
}

// AddEdgeRules adds forward and consume rules to router (forward and reverse).
func (c *Client) AddEdgeRules(ctx context.Context, rules routing.EdgeRules) (ok bool, err error) {
	const method = "AddEdgeRules"
	err = c.call(ctx, method, rules, &ok)
	return ok, err
}

// AddIntermediaryRules adds intermediary rules to router.
func (c *Client) AddIntermediaryRules(ctx context.Context, rules []routing.Rule) (ok bool, err error) {
	const method = "AddIntermediaryRules"
	err = c.call(ctx, method, rules, &ok)
	return ok, err
}

// ReserveIDs reserves n IDs and returns them.
func (c *Client) ReserveIDs(ctx context.Context, n uint8) (rtIDs []routing.RouteID, err error) {
	const method = "ReserveIDs"
	err = c.call(ctx, method, n, &rtIDs)
	return rtIDs, err
}

func (c *Client) call(ctx context.Context, method string, args interface{}, reply interface{}) error {
	if c == nil || c.rpc == nil {
		return errors.New("router client not initialized")
	}
	call := c.rpc.Go(RPCName+"."+method, args, reply, nil)
	select {
	case <-ctx.Done():
		// Close the RPC client to release the underlying DMSG stream.
		// Without this, the abandoned rpc.Go call keeps the stream alive
		// with its smux buffers (~40MB per stream), causing memory leaks.
		c.rpc.Close() //nolint:errcheck,gosec
		return ctx.Err()
	case <-call.Done:
		return call.Error
	}
}
