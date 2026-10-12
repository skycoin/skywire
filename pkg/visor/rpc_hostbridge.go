// Package visor pkg/visor/rpc_hostbridge.go c3-vis-wasm
package visor

import (
	"github.com/skycoin/skywire/pkg/util/rpcutil"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// HostBridgeConnect connects the visor to a host bridge.
func (r *RPC) HostBridgeConnect(url *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HostBridgeConnect", url)(nil, &err)
	return r.visor.HostBridgeConnect(*url)
}

// HostBridgeDisconnect ends the session to the host bridge.
func (r *RPC) HostBridgeDisconnect(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HostBridgeDisconnect", nil)(nil, &err)
	return r.visor.HostBridgeDisconnect()
}

// HostBridgeStatus reports the session to the host bridge.
func (r *RPC) HostBridgeStatus(_ *struct{}, out *visorapi.HostBridgeStatus) (err error) {
	defer rpcutil.LogCall(r.log, "HostBridgeStatus", nil)(out, &err)
	*out, err = r.visor.HostBridgeStatus()
	return err
}

// HostBridgeForward forwards a port of the bridge's host to the visor.
func (r *RPC) HostBridgeForward(in *visorapi.HostBridgeForwardArgs, out *visorapi.HostBridgeForward) (err error) {
	defer rpcutil.LogCall(r.log, "HostBridgeForward", in)(out, &err)
	*out, err = r.visor.HostBridgeForward(in.Host, in.Local)
	return err
}

// HostBridgeUnforward stops a forward.
func (r *RPC) HostBridgeUnforward(host *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HostBridgeUnforward", host)(nil, &err)
	return r.visor.HostBridgeUnforward(*host)
}
