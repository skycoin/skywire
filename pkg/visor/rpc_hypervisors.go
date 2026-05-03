// Package visor pkg/visor/rpc.go
package visor

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// HVListVisors returns summaries of all visors connected to this hypervisor.
func (r *RPC) HVListVisors(_ *struct{}, out *[]HVVisorEntry) (err error) {
	defer rpcutil.LogCall(r.log, "HVListVisors", nil)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	entries, lErr := v.HVListVisors()
	if lErr != nil {
		return lErr
	}
	*out = entries
	return nil
}

// HVVisorSummary returns a detailed summary of a specific remote visor.
func (r *RPC) HVVisorSummary(pk *cipher.PubKey, out *Summary) (err error) {
	defer rpcutil.LogCall(r.log, "HVVisorSummary", pk)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	summary, sErr := v.HVVisorSummary(*pk)
	if sErr != nil {
		return sErr
	}
	*out = *summary
	return nil
}

// HVStartApp starts an app on a remote visor.
func (r *RPC) HVStartApp(in *HVAppArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVStartApp", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVStartApp(in.PK, in.AppName)
}

// HVStopApp stops an app on a remote visor.
func (r *RPC) HVStopApp(in *HVAppArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVStopApp", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVStopApp(in.PK, in.AppName)
}

// HVSetMinHops sets min_hops on a remote visor.
func (r *RPC) HVSetMinHops(in *HVMinHopsArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVSetMinHops", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVSetMinHops(in.PK, in.Hops)
}

// HVSetRewardAddress sets the reward address on a remote visor.
func (r *RPC) HVSetRewardAddress(in *HVRewardArgs, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "HVSetRewardAddress", in)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	conf, err := v.HVSetRewardAddress(in.PK, in.Addr)
	if err != nil {
		return err
	}
	*out = conf
	return nil
}

// HVRemoveTransport deletes a transport on a remote visor.
func (r *RPC) HVRemoveTransport(in *HVTransportArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVRemoveTransport", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVRemoveTransport(in.PK, in.TID)
}

// HVRemoveRoutingRule deletes a routing rule on a remote visor.
func (r *RPC) HVRemoveRoutingRule(in *HVRoutingRuleArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVRemoveRoutingRule", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVRemoveRoutingRule(in.PK, in.Key)
}

// HVAddTransport creates a new transport on a remote visor.
func (r *RPC) HVAddTransport(in *HVAddTransportArgs, out *TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "HVAddTransport", in)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	res, addErr := v.HVAddTransport(in.PK, in.Remote, in.TpType, in.Label, in.Timeout)
	if addErr != nil {
		return addErr
	}
	*out = *res
	return nil
}

// HVSetPublicAutoconnect toggles public_autoconnect on a remote visor.
func (r *RPC) HVSetPublicAutoconnect(in *HVAutoconnectArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVSetPublicAutoconnect", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVSetPublicAutoconnect(in.PK, in.Enable)
}

// HVSetMuxRoutes sets mux_routes on a remote visor.
func (r *RPC) HVSetMuxRoutes(in *HVMuxArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVSetMuxRoutes", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVSetMuxRoutes(in.PK, in.N)
}

// HVSetCalculateRoutes toggles calculate_routes on a remote visor.
func (r *RPC) HVSetCalculateRoutes(in *HVCalcRoutesArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVSetCalculateRoutes", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVSetCalculateRoutes(in.PK, in.Enable)
}

// HVReload reloads a remote visor.
func (r *RPC) HVReload(pk *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVReload", pk)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVReload(*pk)
}

// HVShutdown shuts down a remote visor.
func (r *RPC) HVShutdown(pk *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVShutdown", pk)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVShutdown(*pk)
}

// HVServiceHealth returns deployment service health for a remote visor.
func (r *RPC) HVServiceHealth(pk *cipher.PubKey, out *[]ServiceHealthEntry) (err error) {
	defer rpcutil.LogCall(r.log, "HVServiceHealth", pk)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	entries, hErr := v.HVServiceHealth(*pk)
	if hErr != nil {
		return hErr
	}
	*out = entries
	return nil
}

// HVDmsgSessions returns the per-client dmsg sessions snapshot of a remote visor.
func (r *RPC) HVDmsgSessions(pk *cipher.PubKey, out *DmsgClientSessions) (err error) {
	defer rpcutil.LogCall(r.log, "HVDmsgSessions", pk)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	res, dErr := v.HVDmsgSessions(*pk)
	if dErr != nil {
		return dErr
	}
	*out = *res
	return nil
}

// HVDmsgConnectAll triggers connect-all on a remote visor.
func (r *RPC) HVDmsgConnectAll(pk *cipher.PubKey, out *DmsgConnectAllResult) (err error) {
	defer rpcutil.LogCall(r.log, "HVDmsgConnectAll", pk)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	res, dErr := v.HVDmsgConnectAll(*pk)
	if dErr != nil {
		return dErr
	}
	*out = *res
	return nil
}

// HVSetDmsgSessionsCount persists sessions_count and triggers connect-all on a remote visor.
func (r *RPC) HVSetDmsgSessionsCount(in *HVDmsgSessionsArgs, out *DmsgConnectAllResult) (err error) {
	defer rpcutil.LogCall(r.log, "HVSetDmsgSessionsCount", in)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	res, dErr := v.HVSetDmsgSessionsCount(in.PK, in.Count)
	if dErr != nil {
		return dErr
	}
	*out = *res
	return nil
}

// HVLogsSince fetches recent app logs from a remote visor.
func (r *RPC) HVLogsSince(in *HVLogsArgs, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "HVLogsSince", in)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	logs, lErr := v.HVLogsSince(in.PK, in.Since, in.AppName)
	if lErr != nil {
		return lErr
	}
	*out = logs
	return nil
}

// HVSetAutoStart toggles autostart on a remote visor.
func (r *RPC) HVSetAutoStart(in *HVAutostartArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVSetAutoStart", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVSetAutoStart(in.PK, in.AppName, in.Autostart)
}

// HVEmbeddedProxies returns embedded resolving proxy status from a remote visor.
func (r *RPC) HVEmbeddedProxies(pk *cipher.PubKey, out *EmbeddedProxiesStatus) (err error) {
	defer rpcutil.LogCall(r.log, "HVEmbeddedProxies", pk)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	res, pErr := v.HVEmbeddedProxies(*pk)
	if pErr != nil {
		return pErr
	}
	*out = *res
	return nil
}

// HVSetEmbeddedProxyEnabled flips a resolver on or off on a remote visor.
func (r *RPC) HVSetEmbeddedProxyEnabled(in *HVProxyArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVSetEmbeddedProxyEnabled", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVSetEmbeddedProxyEnabled(in.PK, in.Kind, in.Enable)
}

// HVSetEmbeddedProxyUpstream sets a resolver's SOCKS5 fallthrough on a remote visor.
func (r *RPC) HVSetEmbeddedProxyUpstream(in *HVProxyArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVSetEmbeddedProxyUpstream", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVSetEmbeddedProxyUpstream(in.PK, in.Kind, in.Addr)
}

// HVListTCPPorts returns registered skynet TCP ports on a remote visor.
func (r *RPC) HVListTCPPorts(pk *cipher.PubKey, out *[]int) (err error) {
	defer rpcutil.LogCall(r.log, "HVListTCPPorts", pk)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	ports, lErr := v.HVListTCPPorts(*pk)
	if lErr != nil {
		return lErr
	}
	*out = ports
	return nil
}

// HVRegisterTCPPort registers a skynet TCP port on a remote visor.
func (r *RPC) HVRegisterTCPPort(in *HVTCPPortArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVRegisterTCPPort", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVRegisterTCPPort(in.PK, in.Port)
}

// HVDeregisterTCPPort deregisters a skynet TCP port on a remote visor.
func (r *RPC) HVDeregisterTCPPort(in *HVTCPPortArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVDeregisterTCPPort", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVDeregisterTCPPort(in.PK, in.Port)
}

// HVListForwardedPorts returns forwarded ports on a remote visor.
func (r *RPC) HVListForwardedPorts(pk *cipher.PubKey, out *[]ForwardedPort) (err error) {
	defer rpcutil.LogCall(r.log, "HVListForwardedPorts", pk)(out, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	ports, lErr := v.HVListForwardedPorts(*pk)
	if lErr != nil {
		return lErr
	}
	*out = ports
	return nil
}

// HVRegisterForwardedPort registers a forwarded port on a remote visor.
func (r *RPC) HVRegisterForwardedPort(in *HVForwardedPortArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVRegisterForwardedPort", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVRegisterForwardedPort(in.PK, in.Port)
}

// HVUpdateForwardedPort updates a forwarded port on a remote visor.
func (r *RPC) HVUpdateForwardedPort(in *HVForwardedPortArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "HVUpdateForwardedPort", in)(nil, &err)
	v, ok := r.visor.(*Visor)
	if !ok {
		return fmt.Errorf("not a visor instance")
	}
	return v.HVUpdateForwardedPort(in.PK, in.Port)
}
