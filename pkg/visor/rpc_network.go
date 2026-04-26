// Package visor pkg/visor/rpc.go
package visor

import (
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// RegisterTCPPort registers the local port to be accessed by remote visors
func (r *RPC) RegisterTCPPort(port *int, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RegisterTCPPort", port)(nil, &err)
	return r.visor.RegisterTCPPort(*port)
}

// DeregisterTCPPort deregisters the local port that can be accessed by remote visors
func (r *RPC) DeregisterTCPPort(port *int, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DeregisterTCPPort", port)(nil, &err)
	return r.visor.DeregisterTCPPort(*port)
}

// ListTCPPorts lists all the local por that can be accessed by remote visors
func (r *RPC) ListTCPPorts(_ *struct{}, out *[]int) (err error) {
	defer rpcutil.LogCall(r.log, "ListTCPPorts", nil)(out, &err)
	ports, err := r.visor.ListTCPPorts()
	*out = ports
	return err
}

// RegisterForwardedPort registers a port with full metadata.
func (r *RPC) RegisterForwardedPort(p *ForwardedPort, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RegisterForwardedPort", p)(nil, &err)
	return r.visor.RegisterForwardedPort(*p)
}

// UpdateForwardedPort updates metadata for an existing forwarded port.
func (r *RPC) UpdateForwardedPort(p *ForwardedPort, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "UpdateForwardedPort", p)(nil, &err)
	return r.visor.UpdateForwardedPort(*p)
}

// ListForwardedPorts returns all forwarded ports with metadata.
func (r *RPC) ListForwardedPorts(_ *struct{}, out *[]ForwardedPort) (err error) {
	defer rpcutil.LogCall(r.log, "ListForwardedPorts", nil)(out, &err)
	ports, err := r.visor.ListForwardedPorts()
	*out = ports
	return err
}
