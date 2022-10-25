// Package appevent pkg/app/appevent/rpc.go
package appevent

import (
	"context"
	"fmt"
	"io"
	"net/rpc"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/app/appcommon"
)

// RPCGateway represents the RPC gateway that opens up an app for incoming events from visor.
type RPCGateway struct {
	log  logrus.FieldLogger
	subs *Subscriber
}

// NewRPCGateway returns a new RPCGateway.
func NewRPCGateway(log logrus.FieldLogger, subs *Subscriber) *RPCGateway {
	if log == nil {
		log = logging.MustGetLogger("app_rpc_egress_gateway")
	}
	if subs == nil {
		panic("'subs' input cannot be nil")
	}
	return &RPCGateway{log: log, subs: subs}
}

// Notify notifies the app about events.
func (g *RPCGateway) Notify(e *Event, _ *struct{}) (err error) {
	return PushEvent(g.subs, e)
}

//go:generate mockery -name RPCClient -case underscore -inpkg

// RPCClient describes the RPC client interface that communicates the NewRPCGateway.
type RPCClient interface {
	io.Closer
	Notify(ctx context.Context, e *Event) error
	Hello() *appcommon.Hello
}

// NewRPCClient constructs a new 'rpcClient'.
func NewRPCClient(hello *appcommon.Hello) (RPCClient, error) {
	if hello.EgressNet == "" || hello.EgressAddr == "" {
		return &rpcClient{rpcC: nil, hello: hello}, nil
	}

	rpcC, err := rpc.Dial(hello.EgressNet, hello.EgressAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial RPC: %w", err)
	}
	return &rpcClient{rpcC: rpcC, hello: hello}, nil
}

type rpcClient struct {
	rpcC  *rpc.Client
	hello *appcommon.Hello
}

// Notify sends a notify to the rpc gateway.
func (c *rpcClient) Notify(ctx context.Context, e *Event) error {
	if c.rpcC == nil {
		return nil
	}

	call := c.rpcC.Go(c.formatMethod("Notify"), e, nil, nil)
	select {
	case <-call.Done:
		return call.Error
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Hello returns the internal hello object.
func (c *rpcClient) Hello() *appcommon.Hello {
	return c.hello
}

// Close closes the underlying rpc client (if any).
func (c *rpcClient) Close() error {
	if c.rpcC == nil {
		return nil
	}
	return c.rpcC.Close()
}

// formatMethod formats complete RPC method signature.
func (c *rpcClient) formatMethod(method string) string {
	const methodFmt = "%s.%s"
	return fmt.Sprintf(methodFmt, c.hello.ProcKey.String(), method)
}
