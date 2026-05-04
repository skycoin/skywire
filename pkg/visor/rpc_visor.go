// Package visor pkg/visor/rpc.go
package visor

import (
	"errors"

	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// Health returns health information about the visor
// IsStartupComplete returns whether the visor has finished initializing all modules.
func (r *RPC) IsStartupComplete(_ *struct{}, out *bool) (err error) {
	*out = r.visor.IsStartupComplete()
	return nil
}

// EnableHypervisor starts the hypervisor HTTP server and DMSG listener.
// If persist is true, the change is also written to the config file.
func (r *RPC) EnableHypervisor(persist *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "EnableHypervisor", persist)(nil, &err)
	p := persist != nil && *persist
	return r.visor.EnableHypervisorPersist(p)
}

// DisableHypervisor stops the hypervisor HTTP server and disconnects remote visors.
// If persist is true, the change is also written to the config file.
func (r *RPC) DisableHypervisor(persist *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DisableHypervisor", persist)(nil, &err)
	p := persist != nil && *persist
	return r.visor.DisableHypervisorPersist(p)
}

// IsHypervisorEnabled returns whether the hypervisor is currently serving.
func (r *RPC) IsHypervisorEnabled(_ *struct{}, out *bool) (err error) {
	*out = r.visor.IsHypervisorEnabled()
	return nil
}

func (r *RPC) Health(_ *struct{}, out *HealthInfo) (err error) {
	defer rpcutil.LogCall(r.log, "Health", nil)(out, &err)

	healthInfo, err := r.visor.Health()
	if healthInfo != nil {
		*out = *healthInfo
	}

	return err
}

// RuntimeStats returns Go runtime statistics for the visor process.
func (r *RPC) RuntimeStats(_ *struct{}, out *RuntimeStatsInfo) (err error) {
	defer rpcutil.LogCall(r.log, "RuntimeStats", nil)(out, &err)

	stats, err := r.visor.RuntimeStats()
	if stats != nil {
		*out = *stats
	}

	return err
}

// Uptime returns for how long the visor has been running in seconds
func (r *RPC) Uptime(_ *struct{}, out *float64) (err error) {
	defer rpcutil.LogCall(r.log, "Uptime", nil)(out, &err)

	uptime, err := r.visor.Uptime()
	*out = uptime

	return err
}

// SetRewardAddress sets the reward address and privacy setting in reward.txt
func (r *RPC) SetRewardAddress(p string, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "SetRewardAddress", p)(out, &err)
	p, err = r.visor.SetRewardAddress(p)
	*out = p
	return err
}

// SetLANDmsgServer is called by the hypervisor to push LAN DMSG server info to this visor.
func (r *RPC) SetLANDmsgServer(info LANDmsgServerInfo, out *bool) (err error) {
	defer rpcutil.LogCall(r.log, "SetLANDmsgServer", info)(out, &err)
	err = r.visor.SetLANDmsgServer(info)
	*out = err == nil
	return err
}

// GetRewardAddress reads the reward address from reward.txt
func (r *RPC) GetRewardAddress(_ *struct{}, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "GetRewardAddress", nil)(out, &err)
	p, err := r.visor.GetRewardAddress()
	*out = p
	return err
}

// DeleteRewardAddress deletes the reward.txt
func (r *RPC) DeleteRewardAddress(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DeleteRewardAddress", nil)(nil, &err)
	return r.visor.DeleteRewardAddress()
}

// Summary provides an extra summary of the AppNode.
func (r *RPC) Summary(_ *struct{}, out *Summary) (err error) {
	defer rpcutil.LogCall(r.log, "Summary", nil)(out, &err)
	sum, err := r.visor.Summary()
	if err != nil {
		return err
	}
	*out = *sum
	return nil
}

// Overview provides a overview of the AppNode.
func (r *RPC) Overview(_ *struct{}, out *Overview) (err error) {
	defer rpcutil.LogCall(r.log, "Overview", nil)(out, &err)

	overview, err := r.visor.Overview()
	if overview != nil {
		*out = *overview
	}

	return err
}

// FetchUptimeTrackerData trying to fetch ut data
func (r *RPC) FetchUptimeTrackerData(pk string, data *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "FetchUptimeTrackerData", pk)(data, &err)
	rep, err := r.visor.FetchUptimeTrackerData(pk)
	*data = rep
	return err
}

// Reload reloads the config - without restarting the visor
func (r *RPC) Reload(_ *struct{}, _ *struct{}) (err error) {
	// @evanlinjin: do not defer this log statement, as the underlying visor.Logger will get closed.
	rpcutil.LogCall(r.log, "Reload", nil)(nil, nil)

	return r.visor.Reload()
}

// Shutdown shuts down visor.
func (r *RPC) Shutdown(_ *struct{}, _ *struct{}) (err error) {
	// @evanlinjin: do not defer this log statement, as the underlying visor.Logger will get closed.
	rpcutil.LogCall(r.log, "Shutdown", nil)(nil, nil)

	return r.visor.Shutdown()
}

// GetRuntimeConfig returns the visor's running config as JSON.
func (r *RPC) GetRuntimeConfig(_ *struct{}, out *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "GetRuntimeConfig", nil)(nil, &err)
	*out, err = r.visor.GetRuntimeConfig()
	return err
}

// SetRuntimeConfig validates rawJSON and writes it to the visor's
// on-disk config file. Visor restart is required for the change to
// take effect; this RPC does NOT trigger one.
func (r *RPC) SetRuntimeConfig(rawJSON *[]byte, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetRuntimeConfig", nil)(nil, &err)
	if rawJSON == nil {
		return errors.New("nil runtime config payload")
	}
	return r.visor.SetRuntimeConfig(*rawJSON)
}

// LocalTransportStats returns the visor's local per-transport
// bandwidth + latency rollup from the bbolt stats store.
func (r *RPC) LocalTransportStats(_ *struct{}, out *LocalTransportStatsResponse) (err error) {
	defer rpcutil.LogCall(r.log, "LocalTransportStats", nil)(nil, &err)
	resp, err := r.visor.LocalTransportStats()
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// LocalUptimeStats returns the visor's tier-uptime bitmaps from the
// bbolt stats store — process / dmsg / skynet, 5-minute resolution
// for the requested window. Mirror of /stats/uptime on the
// logserver, reachable through the hypervisor RPC chain so the hvui
// can fetch it without per-visor HTTP calls.
func (r *RPC) LocalUptimeStats(args *LocalUptimeArgs, out *LocalUptimeResponse) (err error) {
	defer rpcutil.LogCall(r.log, "LocalUptimeStats", nil)(nil, &err)
	if args == nil {
		args = &LocalUptimeArgs{}
	}
	resp, err := r.visor.LocalUptimeStats(*args)
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// FetchCXO probes the visor's lazy-on-demand CXO subscriber for the
// requested (feed, path) and returns the cached payload or a miss
// reason. Used by the CLI's FetchServiceURL to add a CXO step at
// the front of the RPC→DMSG→HTTP chain — when the visor already has
// a fresh subscription the CLI can skip the network round-trip
// entirely.
func (r *RPC) FetchCXO(args *FetchCXOArgs, out *FetchCXOResult) (err error) {
	defer rpcutil.LogCall(r.log, "FetchCXO", args)(nil, &err)
	if args == nil {
		args = &FetchCXOArgs{}
	}
	resp, err := r.visor.FetchCXO(*args)
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// RuntimeLogs returns the visor's accumulated runtime log buffer.
// Used by the hypervisor UI's "view logs" action and the
// `skywire cli visor logs` command.
func (r *RPC) RuntimeLogs(_ *struct{}, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "RuntimeLogs", nil)(out, &err)
	logs, err := r.visor.RuntimeLogs()
	*out = logs
	return err
}

// RuntimeLogsSince returns only entries whose log_line is strictly
// greater than since. Used by the hypervisor UI for diff-based live
// tailing. Caller passes the previous response's Latest as `since`.
func (r *RPC) RuntimeLogsSince(since *int64, out *RuntimeLogsDelta) (err error) {
	defer rpcutil.LogCall(r.log, "RuntimeLogsSince", since)(out, &err)
	d, err := r.visor.RuntimeLogsSince(*since)
	*out = d
	return err
}

// HostStats returns a host-level resource snapshot (CPU%, memory,
// disk, network, plus the visor process slice). Backs the
// hypervisor UI's Resource Monitor panel.
func (r *RPC) HostStats(_ *struct{}, out *HostStatsInfo) (err error) {
	defer rpcutil.LogCall(r.log, "HostStats", nil)(out, &err)
	stats, err := r.visor.HostStats()
	if stats != nil {
		*out = *stats
	}
	return err
}

// NetworkView returns the SD/TPD/UT-aggregated network table that
// `cli sd` prints. Backs the hypervisor UI's Network tab.
func (r *RPC) NetworkView(_ *struct{}, out *NetworkViewResponse) (err error) {
	defer rpcutil.LogCall(r.log, "NetworkView", nil)(out, &err)
	resp, err := r.visor.NetworkView()
	if resp != nil {
		*out = *resp
	}
	return err
}

// SkychatPasswordIsSet reports whether a password is currently set.
func (r *RPC) SkychatPasswordIsSet(_ *struct{}, out *bool) (err error) {
	defer rpcutil.LogCall(r.log, "SkychatPasswordIsSet", nil)(out, &err)
	v, err := r.visor.SkychatPasswordIsSet()
	*out = v
	return err
}

// SetSkychatPassword sets / changes the skychat password.
func (r *RPC) SetSkychatPassword(in *SkychatPasswordChangeIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetSkychatPassword", nil)(nil, &err)
	return r.visor.SetSkychatPassword(in.OldPassword, in.NewPassword)
}

// ClearSkychatPassword removes the skychat password (gate goes off).
func (r *RPC) ClearSkychatPassword(oldPassword *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "ClearSkychatPassword", nil)(nil, &err)
	return r.visor.ClearSkychatPassword(*oldPassword)
}

// SkychatLocalAddr returns the host:port skychat is bound to.
func (r *RPC) SkychatLocalAddr(_ *struct{}, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "SkychatLocalAddr", nil)(out, &err)
	addr, err := r.visor.SkychatLocalAddr()
	*out = addr
	return err
}

// GetConfigPath returns the filesystem path the visor loaded its config from.
func (r *RPC) GetConfigPath(_ *struct{}, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "GetConfigPath", nil)(out, &err)
	*out, err = r.visor.GetConfigPath()
	return err
}

// RemoteVisors return connected remote visors
func (r *RPC) RemoteVisors(_ *struct{}, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "RemoteVisor", nil)(out, &err)
	remoteVisors, err := r.visor.RemoteVisors()
	if remoteVisors != nil {
		*out = remoteVisors
	}
	return err
}

// Ports return list of all ports used by visor services and apps
func (r *RPC) Ports(_ *struct{}, out *map[string]PortDetail) (err error) {
	defer rpcutil.LogCall(r.log, "Ports", nil)(out, &err)
	ports, err := r.visor.Ports()
	if ports != nil {
		*out = ports
	}
	return err
}

// IsDMSGClientReady return status of dmsg client
func (r *RPC) IsDMSGClientReady(_ *struct{}, out *bool) (err error) {
	defer rpcutil.LogCall(r.log, "IsDMSGClientReady", nil)(out, &err)

	status, err := r.visor.IsDMSGClientReady()
	*out = status
	return err
}

// DMSGServers returns list of connected DMSG servers with latencies
func (r *RPC) DMSGServers(_ *struct{}, out *[]DMSGServerInfo) (err error) {
	defer rpcutil.LogCall(r.log, "DMSGServers", nil)(out, &err)

	servers, err := r.visor.DMSGServers()
	*out = servers
	return err
}

// TestVisor trying to test viosr by pinging to public visor.
func (r *RPC) TestVisor(conf PingConfig, out *[]TestResult) (err error) {
	defer rpcutil.LogCall(r.log, "TestVisor", conf)(out, &err)

	*out, err = r.visor.TestVisor(conf)
	return err
}

// ReinitiateModule reinitiate/restart modules
func (r *RPC) ReinitiateModule(module string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "ReinitiateModule", module)(nil, &err)

	return r.visor.ReinitiateModule(module)
}

// StartUIServer starts the embedded UI server.
func (r *RPC) StartUIServer(addr *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StartUIServer", addr)(nil, &err)

	addrStr := ""
	if addr != nil {
		addrStr = *addr
	}
	return r.visor.StartUIServer(addrStr)
}

// StopUIServer stops the embedded UI server.
func (r *RPC) StopUIServer(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StopUIServer", nil)(nil, &err)

	return r.visor.StopUIServer()
}

// UIServerStatus returns the status of the UI server.
func (r *RPC) UIServerStatus(_ *struct{}, out *UIServerStatus) (err error) {
	defer rpcutil.LogCall(r.log, "UIServerStatus", nil)(out, &err)

	status, err := r.visor.UIServerStatus()
	if err != nil {
		return err
	}
	*out = *status
	return nil
}
