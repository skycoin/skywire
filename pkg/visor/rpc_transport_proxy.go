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
	"context"
	"encoding/json"
	"fmt"
	"net/rpc"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
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
//
// Uses v.transportRPCMux — the SAME VStreamMux that's registered as
// the transport manager's VisorRPCPacket handler. Sharing this mux
// between accept and dial paths is required: the manager only routes
// inbound VisorRPCPacket frames to one handler, and dial-side
// response frames have to land in the same streams map that opened
// the stream. Creating a separate mux for the dial path (as the
// previous DialTransportRPC implementation did) left outbound
// streams unable to receive responses and every tp-rpc call timed
// out indefinitely.
func (v *Visor) TransportRPCCall(remotePK cipher.PubKey, method string, args json.RawMessage) (json.RawMessage, error) {
	if v.tpM == nil {
		return nil, fmt.Errorf("transport manager not available")
	}
	if v.transportRPCMux == nil {
		return nil, fmt.Errorf("transport RPC not initialized")
	}

	// On a fresh visor (or one that just restarted) the transport
	// to remotePK may not exist yet. Try to create one before the
	// dial — same stcpr→sudph fallback the autoUpgradeHypervisorTransport
	// goroutine uses on a steady cadence. If a non-dmsg transport
	// already exists, ensureFastTransport is a no-op.
	if err := v.ensureFastTransport(remotePK); err != nil {
		return nil, fmt.Errorf("transport rpc: ensure transport to %s: %w", remotePK.String(), err)
	}

	rpcC, err := DialTransportRPC(v.transportRPCMux, remotePK)
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

// ensureFastTransport guarantees a non-dmsg ManagedTransport to
// remotePK is in place before a transport-RPC dial. No-op when one
// already exists; otherwise tries stcpr first (direct TCP, no dmsg
// signaling), then sudph (UDP with NAT hole-punch). dmsg is not a
// candidate here — VStreamMux excludes dmsg transports because
// VisorRPCPacket on route ID 0 doesn't ride dmsg's own stream mux.
//
// Bounded dial timeout (8s per attempt) keeps a totally unreachable
// peer from stalling the caller for the full address-resolver
// retrier budget.
func (v *Visor) ensureFastTransport(remotePK cipher.PubKey) error {
	if hasFastTransportTo(v.tpM, remotePK) {
		return nil
	}

	localSTCPR := v.tpM.IsKnownNetwork(tptypes.STCPR)
	localSUDPH := v.tpM.IsKnownNetwork(tptypes.SUDPH)
	if !localSTCPR && !localSUDPH {
		return fmt.Errorf("local visor has neither stcpr nor sudph available")
	}

	var firstErr error
	for _, attempt := range []tptypes.Type{tptypes.STCPR, tptypes.SUDPH} {
		if attempt == tptypes.STCPR && !localSTCPR {
			continue
		}
		if attempt == tptypes.SUDPH && !localSUDPH {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		_, err := v.tpM.SaveTransport(ctx, remotePK, attempt, transport.LabelAutomatic)
		cancel()
		if err == nil {
			return nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	// hasFastTransportTo is the source of truth — SaveTransport can
	// return an error if the peer entry already exists with the same
	// type, even though the transport itself is fine. Re-check.
	if hasFastTransportTo(v.tpM, remotePK) {
		return nil
	}
	if firstErr != nil && strings.Contains(firstErr.Error(), "context") {
		return fmt.Errorf("transport create timed out: %w", firstErr)
	}
	return fmt.Errorf("transport create failed: %w", firstErr)
}

// DialTransportRPC opens a VStream to a remote visor on the visor's
// shared transport-RPC mux and returns an RPC client connected to
// it. The mux MUST be the one registered as the transport manager's
// VisorRPCPacket handler — see the comment on Visor.transportRPCMux
// for why. The caller is responsible for closing the returned
// client.
func DialTransportRPC(mux *transport.VStreamMux, remotePK cipher.PubKey) (*rpc.Client, error) {
	stream, err := mux.Dial(remotePK)
	if err != nil {
		return nil, fmt.Errorf("transport rpc: dial %s: %w", remotePK.String(), err)
	}
	return rpc.NewClient(stream), nil
}
