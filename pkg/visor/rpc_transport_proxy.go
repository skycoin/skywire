// Package visor pkg/visor/rpc_transport_proxy.go
//
// TransportRPCProxy enables CLI access to a remote visor's RPC through
// the local visor's transport. The CLI connects to the local visor
// (localhost:3435 as usual), calls ProxyRPC with the remote PK, and the
// local visor opens a VStream to the remote visor over their shared
// transport. The remote visor serves RPC on that VStream (authenticated
// via the hypervisor/dmsgpty whitelist).
//
// Usage from CLI:
//
//	skywire cli --rpc localhost:3435 visor tp-rpc <remote-pk> <method> [args]
//
// The local visor must:
// 1. Have a transport to the remote visor
// 2. Be in the remote visor's hypervisor/dmsgpty whitelist
package visor

import (
	"encoding/json"
	"fmt"
	"net/rpc"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

// TransportRPCProxyRequest is the request for the TransportRPCProxy RPC method.
type TransportRPCProxyRequest struct {
	RemotePK cipher.PubKey `json:"remote_pk"`
	Method   string        `json:"method"`
	Args     []byte        `json:"args"` // JSON-encoded RPC args
}

// TransportRPCProxyReply is the response from the TransportRPCProxy RPC method.
type TransportRPCProxyReply struct {
	Result []byte `json:"result"` // JSON-encoded RPC result
	Error  string `json:"error,omitempty"`
}

// TransportRPCCall proxies an RPC call to a remote visor over a transport VStream.
// args is optional JSON-encoded RPC arguments; nil means no-arg call.
func (v *Visor) TransportRPCCall(remotePK cipher.PubKey, method string, args json.RawMessage) (json.RawMessage, error) {
	if v.tpM == nil {
		return nil, fmt.Errorf("transport manager not available")
	}
	log := logging.MustGetLogger("transport_rpc_proxy")
	rpcC, err := DialTransportRPC(remotePK, v.tpM, log)
	if err != nil {
		return nil, err
	}
	defer rpcC.Close() //nolint:errcheck,gosec

	var rpcArgs interface{} = &struct{}{}
	if len(args) > 0 {
		rpcArgs = &args
	}

	var result json.RawMessage
	if err := rpcC.Call(method, rpcArgs, &result); err != nil {
		return nil, fmt.Errorf("remote RPC %s: %w", method, err)
	}
	return result, nil
}

// DialTransportRPC opens a VStream to a remote visor over a transport
// and returns an RPC client connected to it. The caller is responsible
// for closing the returned client.
func DialTransportRPC(remotePK cipher.PubKey, tm *transport.Manager, log *logging.Logger) (*rpc.Client, error) {
	mux := transport.NewVStreamMux(tm, routing.VisorRPCPacket, log)

	stream, err := mux.Dial(remotePK)
	if err != nil {
		mux.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("transport rpc: dial %s: %w", remotePK.String()[:8], err)
	}

	return rpc.NewClient(stream), nil
}
