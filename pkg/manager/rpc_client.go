// Package manager pkg/manager/rpc_client.go
package manager

import (
	"context"
	"io"
	"net/rpc"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/setup"
)

// RPCClient API provides methods to call an RPC Server.
// It implements API
type RPCClient struct {
	log     *logging.Logger
	timeout time.Duration
	conn    io.ReadWriteCloser
	client  *rpc.Client
	prefix  string
	FixGob  bool
}

// newRPCClient creates a new API.
func newRPCClient(log *logging.Logger, conn io.ReadWriteCloser, prefix string, timeout time.Duration) *RPCClient {
	if log == nil {
		log = logging.MustGetLogger("manager_rpc_client")
	}
	return &RPCClient{
		log:     log,
		timeout: timeout,
		conn:    conn,
		client:  rpc.NewClient(conn),
		prefix:  prefix,
	}
}

// Call calls the internal rpc.Client with the serviceMethod arg prefixed.
func (rc *RPCClient) Call(method string, args, reply interface{}) error {
	ctx := context.Background()
	timeout := rc.timeout

	if timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, time.Now().Add(timeout))
		defer cancel()
	}

	select {
	case call := <-rc.client.Go(rc.prefix+"."+method, args, reply, nil).Done:
		return call.Error
	case <-ctx.Done():
		if err := rc.conn.Close(); err != nil {
			rc.log.WithError(err).Warn("Failed to close rpc client after timeout error.")
		}
		return ctx.Err()
	}
}

// Challenge calls Challenge.
func (rc *RPCClient) Challenge() ([]byte, error) {
	var out []byte
	err := rc.Call("Challenge", &struct{}{}, &out)
	return out, err
}

// Response calls Response.
func (rc *RPCClient) Response(resp []byte) (bool, error) {
	var out bool
	err := rc.Call("Response", &resp, &out)
	return out, err
}

// AddTransport calls AddTransport.
func (rc *RPCClient) AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration) (*setup.TransportSummary, error) {
	var summary setup.TransportSummary
	err := rc.Call("AddTransport", &AddTransportIn{
		RemotePK: remote,
		TpType:   tpType,
		Timeout:  timeout,
	}, &summary)

	return &summary, err
}

// RemoveTransport calls RemoveTransport.
func (rc *RPCClient) RemoveTransport(tid uuid.UUID) error {
	return rc.Call("RemoveTransport", &tid, &struct{}{})
}

// GetTransports calls GetTransports.
func (rc *RPCClient) GetTransports() ([]*setup.TransportSummary, error) {
	summary := make([]*setup.TransportSummary, 0)
	err := rc.Call("GetTransports", &struct{}{}, &summary)
	return summary, err
}
