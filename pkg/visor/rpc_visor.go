// Package visor pkg/visor/rpc.go
package visor

import (
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

// RuntimeLogs returns the visor's accumulated runtime log buffer.
// Used by the hypervisor UI's "view logs" action and the
// `skywire cli visor logs` command.
func (r *RPC) RuntimeLogs(_ *struct{}, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "RuntimeLogs", nil)(out, &err)
	logs, err := r.visor.RuntimeLogs()
	*out = logs
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
