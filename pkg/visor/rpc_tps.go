// Package visor pkg/visor/rpc_tps.go c3-vis-core
package visor

import (
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// TPSStatus returns the status of the embedded TPS.
func (r *RPC) TPSStatus(_ *struct{}, out *visorapi.TPSStatus) (err error) {
	defer rpcutil.LogCall(r.log, "TPSStatus", nil)(out, &err)

	status, err := r.visor.TPSStatus()
	if err != nil {
		return err
	}
	*out = *status
	return nil
}

// TPSAddTransport adds a transport on a target visor using the embedded TPS.
func (r *RPC) TPSAddTransport(in *visorapi.TPSAddTransportIn, out *visorapi.TPSTransportResponse) (err error) {
	defer rpcutil.LogCall(r.log, "TPSAddTransport", in)(out, &err)

	resp, err := r.visor.TPSAddTransport(in.TargetPK, in.RemotePK, in.TpType)
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// TPSRemoveTransport removes a transport on a target visor using the embedded TPS.
func (r *RPC) TPSRemoveTransport(in *visorapi.TPSRemoveTransportIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "TPSRemoveTransport", in)(nil, &err)
	return r.visor.TPSRemoveTransport(in.TargetPK, in.TpID)
}

// TPSGetTransports gets transports from a target visor using the embedded TPS.
func (r *RPC) TPSGetTransports(targetPK *cipher.PubKey, out *[]visorapi.TPSTransportResponse) (err error) {
	defer rpcutil.LogCall(r.log, "TPSGetTransports", targetPK)(out, &err)

	resp, err := r.visor.TPSGetTransports(*targetPK)
	if err != nil {
		return err
	}
	*out = resp
	return nil
}

// TPSExternalHealthCheck dials an external TPS over dmsg and performs a health check.
func (r *RPC) TPSExternalHealthCheck(tpsPK *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "TPSExternalHealthCheck", tpsPK)(nil, &err)
	return r.visor.TPSExternalHealthCheck(*tpsPK)
}

// TPSExternalAddTransport requests transport setup via an external TPS.
func (r *RPC) TPSExternalAddTransport(in *visorapi.TPSExternalAddTransportIn, out *visorapi.TPSTransportResponse) (err error) {
	defer rpcutil.LogCall(r.log, "TPSExternalAddTransport", in)(out, &err)

	resp, err := r.visor.TPSExternalAddTransport(in.TPSPK, in.TargetPK, in.RemotePK, in.TpType)
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// TPSExternalGetTransports gets transports from a target visor via an external TPS.
func (r *RPC) TPSExternalGetTransports(in *visorapi.TPSExternalGetTransportsIn, out *[]visorapi.TPSTransportResponse) (err error) {
	defer rpcutil.LogCall(r.log, "TPSExternalGetTransports", in)(out, &err)

	resp, err := r.visor.TPSExternalGetTransports(in.TPSPK, in.TargetPK)
	if err != nil {
		return err
	}
	*out = resp
	return nil
}
