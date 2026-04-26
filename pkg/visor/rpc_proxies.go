// Package visor pkg/visor/rpc.go
package visor

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// Connect creates a connection with the remote visor to listen on the remote port and serve that on the local port
func (r *RPC) Connect(in *ConnectIn, out *uuid.UUID) (err error) {
	defer rpcutil.LogCall(r.log, "Connect", in)(out, &err)

	id, err := r.visor.Connect(in.RemotePK, in.RemotePort, in.LocalPort)
	*out = id
	return err
}

// Disconnect breaks the connection with the given id
func (r *RPC) Disconnect(id *uuid.UUID, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "Disconnect", id)(nil, &err)
	err = r.visor.Disconnect(*id)
	return err
}

// List returns all the ongoing skyforwarding connections
func (r *RPC) List(_ *struct{}, out *map[uuid.UUID]*appnet.ForwardConn) (err error) {
	defer rpcutil.LogCall(r.log, "List", nil)(out, &err)
	proxies, err := r.visor.List()
	*out = proxies
	return err
}

// ConnectRawTCP creates a raw TCP connection with the remote visor
func (r *RPC) ConnectRawTCP(in *ConnectIn, out *uuid.UUID) (err error) {
	defer rpcutil.LogCall(r.log, "ConnectRawTCP", in)(out, &err)

	id, err := r.visor.ConnectRawTCP(in.RemotePK, in.RemotePort, in.LocalPort)
	*out = id
	return err
}

// DisconnectRawTCP breaks the raw TCP connection with the given id
func (r *RPC) DisconnectRawTCP(id *uuid.UUID, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DisconnectRawTCP", id)(nil, &err)
	err = r.visor.DisconnectRawTCP(*id)
	return err
}

// ListRawTCP returns all the ongoing raw TCP skyforwarding connections
func (r *RPC) ListRawTCP(_ *struct{}, out *map[uuid.UUID]*appnet.RawTCPForwardConn) (err error) {
	defer rpcutil.LogCall(r.log, "ListRawTCP", nil)(out, &err)
	proxies, err := r.visor.ListRawTCP()
	*out = proxies
	return err
}

// EmbeddedProxies returns the runtime state of the in-process
// resolving proxies. See Visor.EmbeddedProxies for semantics.
func (r *RPC) EmbeddedProxies(_ *struct{}, out *EmbeddedProxiesStatus) (err error) {
	defer rpcutil.LogCall(r.log, "EmbeddedProxies", nil)(out, &err)

	status, err := r.visor.EmbeddedProxies()
	if err != nil {
		return err
	}
	*out = *status
	return nil
}

// SetEmbeddedProxyEnabled toggles a resolver (dmsg/skynet) on or off
// at runtime without editing the config file.
func (r *RPC) SetEmbeddedProxyEnabled(req *SetEmbeddedProxyEnabledRequest, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetEmbeddedProxyEnabled", req)(nil, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.SetEmbeddedProxyEnabled(req.Kind, req.Enable)
}

// SetEmbeddedProxyUpstream changes the upstream SOCKS5 address for a resolver.
func (r *RPC) SetEmbeddedProxyUpstream(req *SetEmbeddedProxyUpstreamRequest, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetEmbeddedProxyUpstream", req)(nil, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.SetEmbeddedProxyUpstream(req.Kind, req.Addr)
}
