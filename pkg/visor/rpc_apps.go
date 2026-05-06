// Package visor pkg/visor/rpc.go
package visor

import (
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// LogsSince returns all logs from an specific app since the timestamp
func (r *RPC) LogsSince(in *AppLogsRequest, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "LogsSince", in)(out, &err)

	logs, err := r.visor.LogsSince(in.TimeStamp, in.AppName)
	*out = logs

	return err
}

// SetAppDetailedStatus sets app's detailed status.
func (r *RPC) SetAppDetailedStatus(in *SetAppStatusIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppDetailedStatus", in)(nil, &err)

	return r.visor.SetAppDetailedStatus(in.AppName, in.Status)
}

// SetAppError sets app's error.
func (r *RPC) SetAppError(in *SetAppErrorIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppError", in)(nil, &err)

	return r.visor.SetAppError(in.AppName, in.Err)
}

// App returns App registered on the Visor.
func (r *RPC) App(appName *string, reply *appserver.AppState) (err error) {
	defer rpcutil.LogCall(r.log, "App", nil)(reply, &err)

	app, err := r.visor.App(*appName)
	*reply = *app

	return err
}

// Apps returns list of Apps registered on the Visor.
func (r *RPC) Apps(_ *struct{}, reply *[]*appserver.AppState) (err error) {
	defer rpcutil.LogCall(r.log, "Apps", nil)(reply, &err)

	apps, err := r.visor.Apps()
	*reply = apps

	return err
}

// StartApp start App with provided name.
func (r *RPC) StartApp(in *StartAppIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StartApp", in)(nil, &err)

	return r.visor.StartAppWithMode(in.AppName, in.LauncherMode)
}

// AddApp add app to config
func (r *RPC) AddApp(in *SetAppAddIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "AddApp", in)(nil, &err)

	return r.visor.AddApp(in.AppName, in.BinaryName)
}

// DeleteApp removes an app entry from the launcher config (and stops it first if running).
func (r *RPC) DeleteApp(in *AppNameIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DeleteApp", in)(nil, &err)
	return r.visor.DeleteApp(in.AppName)
}

// DoCustomSetting set custom setting to apps arguments
func (r *RPC) DoCustomSetting(in *SetAppMapIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DoCustomSetting", in)(nil, &err)
	return r.visor.DoCustomSetting(in.AppName, in.Val)
}

// SetAppEnv sets/replaces/deletes a single KEY=value entry on an app's environment.
func (r *RPC) SetAppEnv(in *SetAppEnvIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppEnv", in)(nil, &err)
	return r.visor.SetAppEnv(in.AppName, in.Key, in.Value)
}

// SetAppEnvBatch is the multi-key counterpart to SetAppEnv.
func (r *RPC) SetAppEnvBatch(in *SetAppEnvBatchIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppEnvBatch", in)(nil, &err)
	return r.visor.SetAppEnvBatch(in.AppName, in.Env)
}

// SetAppArgs replaces the entire Args slice on an app.
func (r *RPC) SetAppArgs(in *SetAppArgsIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppArgs", in)(nil, &err)
	return r.visor.SetAppArgs(in.AppName, in.Args)
}

// SetAppEnvFull replaces the entire Env slice on an app.
func (r *RPC) SetAppEnvFull(in *SetAppEnvFullIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppEnvFull", in)(nil, &err)
	return r.visor.SetAppEnvFull(in.AppName, in.Env)
}

// SetAppLauncherMode persists the launcher-mode preference for an app.
func (r *RPC) SetAppLauncherMode(in *SetAppLauncherModeIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppLauncherMode", in)(nil, &err)
	return r.visor.SetAppLauncherMode(in.AppName, in.Mode)
}

// AppHelp returns the captured `<binary> --help` output for an app.
func (r *RPC) AppHelp(in *AppNameIn, reply *string) (err error) {
	defer rpcutil.LogCall(r.log, "AppHelp", in)(reply, &err)
	out, err := r.visor.AppHelp(in.AppName)
	if err != nil {
		return err
	}
	*reply = out
	return nil
}

// RegisterApp registers a App with provided proc config.
func (r *RPC) RegisterApp(procConf *appcommon.ProcConfig, reply *appcommon.ProcKey) (err error) {
	defer rpcutil.LogCall(r.log, "RegisterApp", procConf)(reply, &err)
	procKey, err := r.visor.RegisterApp(*procConf)
	*reply = procKey
	return err
}

// DeregisterApp de registers a App with provided proc key.
func (r *RPC) DeregisterApp(procKey *appcommon.ProcKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DeregisterApp", procKey)(nil, &err)
	return r.visor.DeregisterApp(*procKey)
}

// StopApp stops App with provided name.
func (r *RPC) StopApp(name *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StopApp", name)(nil, &err)

	return r.visor.StopApp(*name)
}

// KillApp kill App with provided name.
func (r *RPC) KillApp(name *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "KillApp", name)(nil, &err)

	return r.visor.KillApp(*name)
}

// StartVPNClient starts VPNClient App
func (r *RPC) StartVPNClient(in *StartVPNClientIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StartVPNClient", in)(nil, &err)

	return r.visor.StartVPNClientWithMode(in.PK, in.LauncherMode)
}

// StopVPNClient stops VPNClient App
func (r *RPC) StopVPNClient(name *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StopVPNClient", name)(nil, &err)

	return r.visor.StopVPNClient(*name)
}

// StartSkysocksClient starts SkysocksClient App
func (r *RPC) StartSkysocksClient(pk string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StartSkysocksClient", pk)(nil, &err)

	return r.visor.StartSkysocksClient(pk)
}

// StopSkysocksClients stops all SkysocksClient Apps
func (r *RPC) StopSkysocksClients(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StopSkysocksClients", nil)(nil, &err)

	return r.visor.StopSkysocksClients()
}

// RestartApp restarts App with provided name.
func (r *RPC) RestartApp(name *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RestartApp", name)(nil, &err)

	return r.visor.RestartApp(*name)
}

// SetAutoStart sets auto-start settings for an app.
func (r *RPC) SetAutoStart(in *SetAutoStartIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAutoStart", in)(nil, &err)

	return r.visor.SetAutoStart(in.AppName, in.AutoStart)
}

// SetAppWhitelist sets the connection whitelist for skysocks / vpn-server.
func (r *RPC) SetAppWhitelist(in *SetAppWhitelistIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppWhitelist", in)(nil, &err)

	return r.visor.SetAppWhitelist(in.AppName, in.Whitelist)
}

// SetAppNetworkInterface sets network interface for the app.
func (r *RPC) SetAppNetworkInterface(in *SetAppNetworkInterfaceIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppNetworkInterface", in)(nil, &err)

	return r.visor.SetAppNetworkInterface(in.AppName, in.NetIfc)
}

// SetAppPK sets PK for the app.
func (r *RPC) SetAppPK(in *SetAppPKIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppPK", in)(nil, &err)

	return r.visor.SetAppPK(in.AppName, in.PK)
}

// SetAppKillswitch sets killswitch flag for the app
func (r *RPC) SetAppKillswitch(in *SetAppBoolIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppKillswitch", in)(nil, &err)

	return r.visor.SetAppKillswitch(in.AppName, in.Val)
}

// SetAppSecure sets secure flag for the app
func (r *RPC) SetAppSecure(in *SetAppBoolIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppSecure", in)(nil, &err)

	return r.visor.SetAppSecure(in.AppName, in.Val)
}

// SetAppAddress sets addr flag for the app
func (r *RPC) SetAppAddress(in *SetAppStringIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppAddress", in)(nil, &err)

	return r.visor.SetAppAddress(in.AppName, in.Val)
}

// GetAppStats gets app runtime statistics.
func (r *RPC) GetAppStats(appName *string, out *appserver.AppStats) (err error) {
	defer rpcutil.LogCall(r.log, "GetAppStats", appName)(out, &err)

	stats, err := r.visor.GetAppStats(*appName)
	if err != nil {
		*out = stats
	}

	return err
}

// GetAppError gets app runtime error.
func (r *RPC) GetAppError(appName *string, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "GetAppError", appName)(out, &err)

	stats, err := r.visor.GetAppError(*appName)
	if err != nil {
		*out = stats
	}

	return err
}

// GetAppConnectionsSummary returns connections stats for the app.
func (r *RPC) GetAppConnectionsSummary(appName *string, out *[]appserver.ConnectionSummary) (err error) {
	defer rpcutil.LogCall(r.log, "GetAppConnectionsSummary", appName)(out, &err)

	summary, err := r.visor.GetAppConnectionsSummary(*appName)
	if summary != nil {
		*out = summary
	}

	return err
}
