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
	rpc  *rpc.Client
	conn io.ReadWriteCloser // raw DMSG stream, kept for deadline management
	rPK  cipher.PubKey      // public key of remote router
	log  logrus.FieldLogger
}

// DialTimeout is the maximum time allowed for a single dial attempt to a remote visor.
// Without this, dials to unreachable visors block indefinitely in DialStream.readResponse,
// leaking goroutines. This caps the dial itself; the RPC deadline is set separately.
// Keep this short to free ephemeral ports quickly under load.
const DialTimeout = 10 * time.Second

// DialError identifies which remote PK failed to dial during route setup.
// The setup-node metrics layer inspects this to avoid tripping the circuit
// breaker on the destination when the failure was actually on the source
// side: id_reservation calls MakeMap which dials every hop (source, dest,
// intermediaries), and a flaky source visor would otherwise poison the
// destination's breaker, blocking all requests to a healthy destination.
type DialError struct {
	PK  cipher.PubKey
	Err error
}

// Error returns the formatted dial error, preserving the legacy message
// format ("dial <pk>@<port>: <inner>") so anything that still greps logs
// keeps working.
func (e *DialError) Error() string {
	return fmt.Sprintf("dial %v@%v: %v", e.PK, skyenv.DmsgAwaitSetupPort, e.Err)
}

// Unwrap exposes the inner error for errors.Is / errors.As.
func (e *DialError) Unwrap() error { return e.Err }

// DialFailedPK reports the remote PK that could not be dialed. The
// setupmetrics collector probes for this via an anonymous interface to
// stay free of a router import cycle.
func (e *DialError) DialFailedPK() cipher.PubKey { return e.PK }

// NewClient creates a new Client.
func NewClient(ctx context.Context, dialer network.Dialer, rPK cipher.PubKey) (*Client, error) {
	dialCtx, dialCancel := context.WithTimeout(ctx, DialTimeout)
	defer dialCancel()

	s, err := dialer.Dial(dialCtx, rPK, skyenv.DmsgAwaitSetupPort)
	if err != nil {
		return nil, &DialError{PK: rPK, Err: err}
	}

	return NewClientFromRaw(s, rPK), nil
}

// NewClientFromRaw creates a new client from a raw connection.
func NewClientFromRaw(conn io.ReadWriteCloser, rPK cipher.PubKey) *Client {
	return &Client{
		rpc:  rpc.NewClient(conn),
		conn: conn,
		rPK:  rPK,
		log:  logging.MustGetLogger(fmt.Sprintf("router_client:%s", rPK.String())),
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

// rpcDeadline is the per-call deadline set on the underlying connection.
// This prevents stuck RPC calls from holding ephemeral ports indefinitely.
// It is reset before each call (not at creation time) so pooled connections
// remain usable across their full idle TTL.
const rpcDeadline = 30 * time.Second

// streamIdleTimeoutForPool is the read deadline set on the underlying
// DMSG stream after an RPC call completes and the connection returns to
// the pool. This must match dmsg.StreamIdleTimeout (2 min) so that the
// rpc.Client.input() goroutine's Read eventually times out, triggering
// Stream.Read()'s auto-release of the ephemeral porter port.
//
// Without this, call() was clearing BOTH deadlines via SetDeadline(zero),
// leaving the stream with no read deadline — the input() goroutine would
// block in Read forever, and the ephemeral port was never freed. Over ~4
// hours of operation, this leaked all 16,384 ephemeral ports.
const streamIdleTimeoutForPool = 2 * time.Minute

func (c *Client) call(ctx context.Context, method string, args interface{}, reply interface{}) error {
	if c == nil || c.rpc == nil {
		return errors.New("router client not initialized")
	}

	// Set a fresh deadline for this RPC call on both read and write.
	deadline := time.Now().Add(rpcDeadline)
	if conn, ok := c.conn.(interface{ SetDeadline(time.Time) error }); ok {
		conn.SetDeadline(deadline) //nolint:errcheck,gosec
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
		// Clear the write deadline so the connection can idle in the pool.
		// IMPORTANT: Restore the read deadline (StreamIdleTimeout = 2min)
		// so the rpc.Client.input() goroutine's blocked Read will
		// eventually time out and trigger Stream.Read()'s auto-release
		// of the ephemeral porter port. Without this, the read blocks
		// forever and the port is never freed — the root cause of
		// ephemeral port exhaustion after ~4 hours of operation.
		if c.conn != nil {
			if conn, ok := c.conn.(interface {
				SetReadDeadline(time.Time) error
				SetWriteDeadline(time.Time) error
			}); ok {
				conn.SetWriteDeadline(time.Time{})                             //nolint:errcheck,gosec
				conn.SetReadDeadline(time.Now().Add(streamIdleTimeoutForPool)) //nolint:errcheck,gosec
			}
		}
		return call.Error
	}
}
