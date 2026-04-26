// Package visor pkg/visor/rpc.go
package visor

import (
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// BandwidthTest performs a bandwidth test over skywire route.
func (r *RPC) BandwidthTest(conf BandwidthTestConfig, out *BandwidthResult) (err error) {
	defer rpcutil.LogCall(r.log, "BandwidthTest", conf)(out, &err)

	*out, err = r.visor.BandwidthTest(conf)
	return err
}

// DmsgBandwidthTest performs a bandwidth test over dmsg.
func (r *RPC) DmsgBandwidthTest(conf BandwidthTestConfig, out *BandwidthResult) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgBandwidthTest", conf)(out, &err)

	*out, err = r.visor.DmsgBandwidthTest(conf)
	return err
}

// DmsgProbe checks dmsg reachability of a remote PK on a given port.
func (r *RPC) DmsgProbe(req *DmsgProbeRequest, out *bool) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgProbe", req)(out, &err)

	reachable, err := r.visor.DmsgProbe(req.PK, req.Port)
	if err != nil {
		return err
	}
	*out = reachable
	return nil
}

func (r *RPC) DmsgHTTP(req *DmsgHTTPRequest, out *DmsgHTTPResponse) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgHTTP", req)(out, &err)

	resp, err := r.visor.DmsgHTTP(*req)
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// SkynetHTTP performs an HTTP request over skynet using the visor's router.
func (r *RPC) SkynetHTTP(req *SkynetHTTPRequest, out *SkynetHTTPResponse) (err error) {
	defer rpcutil.LogCall(r.log, "SkynetHTTP", req)(out, &err)

	resp, err := r.visor.SkynetHTTP(*req)
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// DmsgConnectAll reaches every dmsg server in discovery and ensures a session to each.
func (r *RPC) DmsgConnectAll(_ *struct{}, out *DmsgConnectAllResult) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgConnectAll", nil)(out, &err)
	resp, err := r.visor.DmsgConnectAll()
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// SetDmsgSessionsCount updates the persisted sessions_count in the visor
// config and triggers a one-shot connect-all so the running dmsg client
// reaches the new target immediately.
func (r *RPC) SetDmsgSessionsCount(count *int, out *DmsgConnectAllResult) (err error) {
	defer rpcutil.LogCall(r.log, "SetDmsgSessionsCount", count)(out, &err)
	resp, err := r.visor.SetDmsgSessionsCount(*count)
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// DmsgSessions returns the current dmsg session state of every dmsg client
// running inside the visor.
func (r *RPC) DmsgSessions(_ *struct{}, out *DmsgClientSessions) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgSessions", nil)(out, &err)
	resp, err := r.visor.DmsgSessions()
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// DmsgPorterStats returns ephemeral port reservation counts.
func (r *RPC) DmsgPorterStats(_ *struct{}, out *DmsgPorterStatus) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgPorterStats", nil)(out, &err)
	s, err := r.visor.DmsgPorterStats()
	if err != nil {
		return err
	}
	*out = *s
	return nil
}

// DmsgPorterReset frees all ephemeral port reservations.
func (r *RPC) DmsgPorterReset(_ *struct{}, out *DmsgPorterStatus) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgPorterReset", nil)(out, &err)
	s, err := r.visor.DmsgPorterReset()
	if err != nil {
		return err
	}
	*out = *s
	return nil
}

// DmsgSetMinSessions updates the minimum DMSG session count.
func (r *RPC) DmsgSetMinSessions(in *int, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgSetMinSessions", in)(nil, &err)
	return r.visor.DmsgSetMinSessions(*in)
}

// DmsgReconnect forces DMSG session reconnection.
func (r *RPC) DmsgReconnect(_ *struct{}, out *int) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgReconnect", nil)(out, &err)
	n, err := r.visor.DmsgReconnect()
	if err != nil {
		return err
	}
	*out = n
	return nil
}

// DmsgPorterDiag returns detailed diagnostics of the RSN's ephemeral port entries.
func (r *RPC) DmsgPorterDiag(_ *struct{}, out *netutil.EphemeralDiagResult) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgPorterDiag", nil)(out, &err)
	diag, err := r.visor.DmsgPorterDiag()
	if err != nil {
		return err
	}
	*out = *diag
	return nil
}
