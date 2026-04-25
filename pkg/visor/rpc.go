// Package visor pkg/visor/rpc.go
package visor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/rpc"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

const (
	// RPCPrefix is the prefix used with all RPC calls.
	RPCPrefix = "app-visor"
	// HealthTimeout defines timeout for /health endpoint calls done from hypervisor.
	HealthTimeout = skyenv.HealthTimeout
	// InnerHealthTimeout defines timeout for /health endpoint calls done from visor.
	InnerHealthTimeout = skyenv.InnerHealthTimeout
)

var (
	// ErrNotImplemented occurs when a method is not implemented.
	ErrNotImplemented = errors.New("not implemented")

	// ErrNotFound is returned when a requested resource is not found.
	ErrNotFound = errors.New("not found")
)

// RPC defines RPC methods for Visor.
type RPC struct {
	visor API
	log   logrus.FieldLogger
}

func newRPCServer(v *Visor, remoteName string) (*rpc.Server, error) {
	rpcS := rpc.NewServer()
	rpcG := &RPC{
		visor: v,
		log:   v.MasterLogger().PackageLogger("visor_rpc:" + remoteName),
	}

	if err := rpcS.RegisterName(RPCPrefix, rpcG); err != nil {
		return nil, fmt.Errorf("failed to create visor RPC server: %w", err)
	}

	return rpcS, nil
}

/*
	<<< NODE HEALTH >>>
*/

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

/*
	<<< THIS NODE UPTIME >>>
*/

// Uptime returns for how long the visor has been running in seconds
func (r *RPC) Uptime(_ *struct{}, out *float64) (err error) {
	defer rpcutil.LogCall(r.log, "Uptime", nil)(out, &err)

	uptime, err := r.visor.Uptime()
	*out = uptime

	return err
}

/*
	<<< SKYCOIN REWARD ADDRESS SETTING >>>
*/

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

/*
	<<< APP LOGS >>>
*/

// AppLogsRequest represents a LogSince method request
type AppLogsRequest struct {
	// TimeStamp should be time.RFC3339Nano formatted
	TimeStamp time.Time `json:"time_stamp"`
	// AppName should match the app name in visor config
	AppName string `json:"app_name"`
}

// LogsSince returns all logs from an specific app since the timestamp
func (r *RPC) LogsSince(in *AppLogsRequest, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "LogsSince", in)(out, &err)

	logs, err := r.visor.LogsSince(in.TimeStamp, in.AppName)
	*out = logs

	return err
}

/*
	<<< NODE SUMMARY >>>
*/

// TransportSummary summarizes a Transport.
type TransportSummary struct {
	ID      uuid.UUID           `json:"id"`
	Local   cipher.PubKey       `json:"local_pk"`
	Remote  cipher.PubKey       `json:"remote_pk"`
	Type    types.Type          `json:"type"`
	Log     *transport.LogEntry `json:"log,omitempty"`
	IsSetup bool                `json:"is_setup"`
	Label   transport.Label     `json:"label"`
}

// TransportLogEntry represents a transport log entry with bandwidth data.
type TransportLogEntry struct {
	TpID      uuid.UUID `json:"tp_id"`
	RecvBytes uint64    `json:"recv_bytes"`
	SentBytes uint64    `json:"sent_bytes"`
	Timestamp int64     `json:"timestamp"`
}

func newTransportSummary(tm *transport.Manager, tp *transport.ManagedTransport, includeLogs, isSetup bool) *TransportSummary {
	summary := &TransportSummary{
		ID:      tp.Entry.ID,
		Local:   tm.Local(),
		Remote:  tp.Remote(),
		Type:    tp.Type(),
		IsSetup: isSetup,
		Label:   tp.Entry.Label,
	}
	if includeLogs {
		summary.Log = tp.LogEntry
	}
	return summary
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

/*
	<<< APP MANAGEMENT >>>
*/

// SetAppStatusIn is input for SetAppDetailedStatus.
type SetAppStatusIn struct {
	AppName string
	Status  string
}

// SetAppErrorIn is input for SetAppError.
type SetAppErrorIn struct {
	AppName string
	Err     string
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

// StartAppIn is input for StartApp.
type StartAppIn struct {
	AppName      string
	LauncherMode string // "internal", "external", or "" for default
}

// StartApp start App with provided name.
func (r *RPC) StartApp(in *StartAppIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StartApp", in)(nil, &err)

	return r.visor.StartAppWithMode(in.AppName, in.LauncherMode)
}

// SetAppAddIn is input for SetAppAdd.
type SetAppAddIn struct {
	AppName    string
	BinaryName string
}

// AddApp add app to config
func (r *RPC) AddApp(in *SetAppAddIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "AddApp", in)(nil, &err)

	return r.visor.AddApp(in.AppName, in.BinaryName)
}

// DoCustomSetting set custom setting to apps arguments
func (r *RPC) DoCustomSetting(in *SetAppMapIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DoCustomSetting", in)(nil, &err)
	return r.visor.DoCustomSetting(in.AppName, in.Val)
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

// StartVPNClientIn is input for StartVPNClient.
type StartVPNClientIn struct {
	PK           cipher.PubKey
	LauncherMode string // "internal", "external", or "" for default
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

// FetchUptimeTrackerData trying to fetch ut data
func (r *RPC) FetchUptimeTrackerData(pk string, data *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "FetchUptimeTrackerData", pk)(data, &err)
	rep, err := r.visor.FetchUptimeTrackerData(pk)
	*data = rep
	return err
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

// SetAutoStartIn is input for SetAutoStart.
type SetAutoStartIn struct {
	AppName   string
	AutoStart bool
}

// SetAutoStart sets auto-start settings for an app.
func (r *RPC) SetAutoStart(in *SetAutoStartIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAutoStart", in)(nil, &err)

	return r.visor.SetAutoStart(in.AppName, in.AutoStart)
}

// SetAppPasswordIn is input for SetAppPassword.
type SetAppPasswordIn struct {
	AppName  string
	Password string
}

// SetAppPassword sets password for the app.
func (r *RPC) SetAppPassword(in *SetAppPasswordIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppPassword", in)(nil, &err)

	return r.visor.SetAppPassword(in.AppName, in.Password)
}

// SetAppNetworkInterfaceIn is input for SetAppNetworkInterface.
type SetAppNetworkInterfaceIn struct {
	AppName string
	NetIfc  string
}

// SetAppNetworkInterface sets network interface for the app.
func (r *RPC) SetAppNetworkInterface(in *SetAppNetworkInterfaceIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetAppNetworkInterface", in)(nil, &err)

	return r.visor.SetAppNetworkInterface(in.AppName, in.NetIfc)
}

// SetAppPKIn is input for SetAppPK.
type SetAppPKIn struct {
	AppName string
	PK      cipher.PubKey
}

// SetAppBoolIn is input for SetApp boolean flags
type SetAppBoolIn struct {
	AppName string
	Val     bool
}

// SetAppStringIn is input for SetApp string flags
type SetAppStringIn struct {
	AppName string
	Val     string
}

// SetAppMapIn is input for SetApp map[string]string flags
type SetAppMapIn struct {
	AppName string
	Val     map[string]any
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

/*
	<<< TRANSPORT MANAGEMENT >>>
*/

// TransportTypes lists all transport types supported by the Visor.
func (r *RPC) TransportTypes(_ *struct{}, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "TransportTypes", nil)(out, &err)

	types, err := r.visor.TransportTypes()
	*out = types

	return err
}

// TransportsIn is input for Transports.
type TransportsIn struct {
	FilterTypes   []string
	FilterPubKeys []cipher.PubKey
	ShowLogs      bool
}

// Transports lists Transports of the Visor and provides a summary of each.
func (r *RPC) Transports(in *TransportsIn, out *[]*TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "Transports", in)(out, &err)

	transports, err := r.visor.Transports(in.FilterTypes, in.FilterPubKeys, in.ShowLogs)
	*out = transports

	return err
}

// Transport obtains a Transport Summary of Transport of given Transport ID.
func (r *RPC) Transport(in *uuid.UUID, out *TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "Transport", in)(out, &err)

	tp, err := r.visor.Transport(*in)
	if tp != nil {
		*out = *tp
	}

	return err
}

// AddTransportIn is input for AddTransport.
type AddTransportIn struct {
	RemotePK         cipher.PubKey
	TpType           string
	Timeout          time.Duration
	Label            string // "user" or "skycoin" (default: "skycoin")
	NoRegister       bool   // skip transport discovery registration (only valid for "user" label)
	SkipLatencyProbe bool   // deprecated: latency is now measured at the transport level
}

// AddTransport creates a transport for the visor.
func (r *RPC) AddTransport(in *AddTransportIn, out *TransportSummary) (err error) {
	defer rpcutil.LogCall(r.log, "AddTransport", in)(out, &err)

	tp, err := r.visor.AddTransport(in.RemotePK, in.TpType, in.Timeout, in.Label, in.NoRegister, in.SkipLatencyProbe)
	if tp != nil {
		*out = *tp
	}

	return err
}

// SetSTCPAddrIn is input for SetSTCPAddr.
type SetSTCPAddrIn struct {
	PK   cipher.PubKey
	Addr string
}

// SetSTCPAddr injects an STCP PK table entry at runtime.
func (r *RPC) SetSTCPAddr(in *SetSTCPAddrIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetSTCPAddr", in)(nil, &err)
	return r.visor.SetSTCPAddr(in.PK, in.Addr)
}

// RemoveTransport removes a Transport from the visor.
func (r *RPC) RemoveTransport(tid *uuid.UUID, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveTransport", tid)(nil, &err)

	return r.visor.RemoveTransport(*tid)
}

// RemoveAllTransports removes all Transports from the visor.
func (r *RPC) RemoveAllTransports(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveAllTransports", nil)(nil, &err)

	return r.visor.RemoveAllTransports()
}

/*
	<<< AVAILABLE TRANSPORTS >>>
*/

// DiscoverTransportsByPK obtains available transports via the transport discovery via given public key.
func (r *RPC) DiscoverTransportsByPK(pk *cipher.PubKey, out *[]*transport.Entry) (err error) {
	defer rpcutil.LogCall(r.log, "DiscoverTransportsByPK", pk)(out, &err)

	entries, err := r.visor.DiscoverTransportsByPK(*pk)
	*out = entries

	return err
}

// DiscoverTransportByID obtains available transports via the transport discovery via a given transport ID.
func (r *RPC) DiscoverTransportByID(id *uuid.UUID, out *transport.Entry) (err error) {
	defer rpcutil.LogCall(r.log, "DiscoverTransportByID", id)(out, &err)

	entry, err := r.visor.DiscoverTransportByID(*id)
	if entry != nil {
		*out = *entry
	}

	return err
}

/*
	<<< ROUTES MANAGEMENT >>>
*/

// RoutingRules obtains all routing rules of the RoutingTable.
func (r *RPC) RoutingRules(_ *struct{}, out *[]routing.Rule) (err error) {
	defer rpcutil.LogCall(r.log, "RoutingRules", nil)(out, &err)

	*out, err = r.visor.RoutingRules()
	return err
}

// RoutingRule obtains a routing rule of given RouteID.
func (r *RPC) RoutingRule(key *routing.RouteID, rule *routing.Rule) (err error) {
	defer rpcutil.LogCall(r.log, "RoutingRule", key)(rule, &err)

	*rule, err = r.visor.RoutingRule(*key)
	return err
}

// SaveRoutingRule saves a routing rule.
func (r *RPC) SaveRoutingRule(in *routing.Rule, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SaveRoutingRule", in)(nil, &err)

	return r.visor.SaveRoutingRule(*in)
}

// RemoveRoutingRule removes a RoutingRule based on given RouteID key.
func (r *RPC) RemoveRoutingRule(key *routing.RouteID, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveRoutingRule", key)(nil, &err)

	return r.visor.RemoveRoutingRule(*key)
}

/*
	<<< ROUTEGROUPS MANAGEMENT >>>
	>>> TODO(evanlinjin): Implement.
*/

// RouteGroupInfo is a human-understandable representation of a RouteGroup.
type RouteGroupInfo struct {
	ConsumeRuleID routing.RouteID               `json:"consume_rule_id"`
	FwdRuleID     routing.RouteID               `json:"fwd_rule_id"`
	Desc          routing.RouteDescriptorFields `json:"desc"`
	FwdNextTpID   string                        `json:"fwd_next_tp_id,omitempty"`
	// Hops is the stored forward route path (transport IDs, edges, types)
	// for this route group, populated when the route group is active.
	Hops []RouteHopInfo `json:"hops,omitempty"`
}

// RouteGroups retrieves routegroups via rules of the routing table.
func (r *RPC) RouteGroups(_ *struct{}, out *[]RouteGroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "RouteGroups", nil)(out, &err)

	rgs, err := r.visor.RouteGroups()
	*out = rgs

	return err
}

// FetchServiceDataIn is the input for FetchServiceData RPC.
type FetchServiceDataIn struct {
	Service string
	Path    string
}

// FetchServiceData proxies a GET request to a deployment service.
func (r *RPC) FetchServiceData(in *FetchServiceDataIn, out *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "FetchServiceData", in)(out, &err)
	data, err := r.visor.FetchServiceData(in.Service, in.Path)
	*out = data
	return err
}

// ServiceHealth checks all deployment services.
func (r *RPC) ServiceHealth(_ *struct{}, out *[]ServiceHealthEntry) (err error) {
	defer rpcutil.LogCall(r.log, "ServiceHealth", nil)(out, &err)
	entries, err := r.visor.ServiceHealth()
	*out = entries
	return err
}

/*
	<<< VISOR MANAGEMENT >>>
*/

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

// SetMinHops sets min_hops in visor's routing config
func (r *RPC) SetMinHops(n *uint16, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetMinHops", *n)
	err = r.visor.SetMinHops(*n)
	return
}

// SetCalculateRoutes sets calculate_routes in visor's routing config
func (r *RPC) SetCalculateRoutes(enabled *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetCalculateRoutes", *enabled)(nil, &err)
	err = r.visor.SetCalculateRoutes(*enabled)
	return
}

// GetCalculateRoutes gets calculate_routes from visor's routing config
func (r *RPC) GetCalculateRoutes(_ *struct{}, out *bool) (err error) {
	defer rpcutil.LogCall(r.log, "GetCalculateRoutes", nil)(out, &err)
	*out, err = r.visor.GetCalculateRoutes()
	return
}

// SetSyncTPDData sets sync_tpd_data in visor's transport config
func (r *RPC) SetSyncTPDData(enabled *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetSyncTPDData", *enabled)(nil, &err)
	err = r.visor.SetSyncTPDData(*enabled)
	return
}

// GetSyncTPDData gets sync_tpd_data from visor's transport config
func (r *RPC) GetSyncTPDData(_ *struct{}, out *bool) (err error) {
	defer rpcutil.LogCall(r.log, "GetSyncTPDData", nil)(out, &err)
	*out, err = r.visor.GetSyncTPDData()
	return
}

// GetPersistentTransports gets persistent_transports from visor's routing config
func (r *RPC) GetPersistentTransports(_ *struct{}, out *[]transport.PersistentTransports) (err error) {
	defer rpcutil.LogCall(r.log, "GetPersistentTransports", nil)(out, &err)

	pTs, err := r.visor.GetPersistentTransports()
	*out = pTs
	return err
}

// SetPersistentTransports sets persistent_transports in visor's routing config
func (r *RPC) SetPersistentTransports(pTs *[]transport.PersistentTransports, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetPersistentTransports", *pTs)(nil, &err)
	err = r.visor.SetPersistentTransports(*pTs)
	return err
}

// GetTransportLogs returns transport log entries from the last N days.
func (r *RPC) GetTransportLogs(days *int, out *[]TransportLogEntry) (err error) {
	defer rpcutil.LogCall(r.log, "GetTransportLogs", *days)(out, &err)
	entries, err := r.visor.GetTransportLogs(*days)
	if err != nil {
		return err
	}
	*out = entries
	return nil
}

// SetPublicAutoconnect sets public_autoconnect in visor's routing config
func (r *RPC) SetPublicAutoconnect(pAc *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetPublicAutoconnect", *pAc)(nil, &err)
	err = r.visor.SetPublicAutoconnect(*pAc)
	return err
}

// SetIsPublic sets the is_public field in the visor config and flushes.
func (r *RPC) SetIsPublic(isPublic *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetIsPublic", *isPublic)(nil, &err)
	return r.visor.SetIsPublic(*isPublic)
}

// GetIsPublic returns whether the visor is configured as public.
func (r *RPC) GetIsPublic(_ *struct{}, out *bool) (err error) {
	*out = r.visor.GetIsPublic()
	return nil
}

// GetRuntimeConfig returns the visor's running config as JSON.
func (r *RPC) GetRuntimeConfig(_ *struct{}, out *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "GetRuntimeConfig", nil)(nil, &err)
	*out, err = r.visor.GetRuntimeConfig()
	return err
}

// StartPublicAutoconnect starts the public autoconnect routine
func (r *RPC) StartPublicAutoconnect(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StartPublicAutoconnect", nil)(nil, &err)
	return r.visor.StartPublicAutoconnect()
}

// StopPublicAutoconnect stops the public autoconnect routine
func (r *RPC) StopPublicAutoconnect(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StopPublicAutoconnect", nil)(nil, &err)
	return r.visor.StopPublicAutoconnect()
}

// PublicAutoconnectStatus returns whether public autoconnect is running
func (r *RPC) PublicAutoconnectStatus(_ *struct{}, out *bool) (err error) {
	defer rpcutil.LogCall(r.log, "PublicAutoconnectStatus", nil)(out, &err)
	status, err := r.visor.PublicAutoconnectStatus()
	*out = status
	return err
}

// SetExistingTPOnly sets whether to only use existing transports for routing
func (r *RPC) SetExistingTPOnly(enabled *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetExistingTPOnly", *enabled)(nil, &err)
	err = r.visor.SetExistingTPOnly(*enabled)
	return err
}

// SetForceLocalRoutes sets whether to skip the route finder and use local route calculation
func (r *RPC) SetForceLocalRoutes(enabled *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetForceLocalRoutes", *enabled)(nil, &err)
	err = r.visor.SetForceLocalRoutes(*enabled)
	return err
}

// SetMuxRoutes sets the number of parallel mux routes for new connections
func (r *RPC) SetMuxRoutes(n *int, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetMuxRoutes", *n)(nil, &err)
	err = r.visor.SetMuxRoutes(*n)
	return err
}

// SetMuxMode sets the weight distribution mode for mux transport selection
func (r *RPC) SetMuxMode(mode *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetMuxMode", *mode)(nil, &err)
	err = r.visor.SetMuxMode(*mode)
	return err
}

// ActiveRoutes returns all active routes with app associations and live stats
func (r *RPC) ActiveRoutes(_ *struct{}, out *[]AppRouteStatus) (err error) {
	defer rpcutil.LogCall(r.log, "ActiveRoutes", nil)(out, &err)
	routes, err := r.visor.ActiveRoutes()
	if routes != nil {
		*out = routes
	}
	return err
}

// MuxRouteInput is input for AddMuxRoute and RemoveMuxRoute
type MuxRouteInput struct {
	AppName     string
	TransportID uuid.UUID
}

// AddMuxRoute adds a mux route to an app's active connection
func (r *RPC) AddMuxRoute(in *MuxRouteInput, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "AddMuxRoute", in)(nil, &err)
	return r.visor.AddMuxRoute(in.AppName, in.TransportID)
}

// RemoveMuxRoute removes a mux route from an app's active connection
func (r *RPC) RemoveMuxRoute(in *MuxRouteInput, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveMuxRoute", in)(nil, &err)
	return r.visor.RemoveMuxRoute(in.AppName, in.TransportID)
}

// FilterServersIn is input for VPNServers and ProxyServers
type FilterServersIn struct {
	Version string
	Country string
}

// VPNServers gets available public VPN server from service discovery URL
func (r *RPC) VPNServers(vc *FilterServersIn, out *[]servicedisc.Service) (err error) {
	defer rpcutil.LogCall(r.log, "VPNServers", nil)(out, &err)
	vpnServers, err := r.visor.VPNServers(vc.Version, vc.Country)
	if vpnServers != nil {
		*out = vpnServers
	}
	return err
}

// ProxyServers gets available socks5 proxy servers from service discovery URL
func (r *RPC) ProxyServers(vc *FilterServersIn, out *[]servicedisc.Service) (err error) {
	defer rpcutil.LogCall(r.log, "ProxyServers", nil)(out, &err)
	proxyServers, err := r.visor.ProxyServers(vc.Version, vc.Country)
	if proxyServers != nil {
		*out = proxyServers
	}
	return err
}

// DeregisterServiceIn is the input for DeregisterService RPC method
type DeregisterServiceIn struct {
	PKs         []cipher.PubKey
	ServiceType string
}

// DeregisterService deregisters services from service discovery
func (r *RPC) DeregisterService(in *DeregisterServiceIn, out *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DeregisterService", in)(out, &err)
	return r.visor.DeregisterService(in.PKs, in.ServiceType)
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

// ConnectIn is input for Connect.
type ConnectIn struct {
	RemotePK   cipher.PubKey
	RemotePort int
	LocalPort  int
}

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

// DialPing dials to the ping module using the provided pk as a hop.
func (r *RPC) DialPing(conf PingConfig, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DialPing", conf)(nil, &err)

	return r.visor.DialPing(conf)
}

// Ping pings the connected route via DialPing.
func (r *RPC) Ping(conf PingConfig, out *[]time.Duration) (err error) {
	defer rpcutil.LogCall(r.log, "Ping", conf)(out, &err)

	*out, err = r.visor.Ping(conf)
	return err
}

// PingOnce performs a single ping on the connected route.
func (r *RPC) PingOnce(conf PingConfig, out *time.Duration) (err error) {
	defer rpcutil.LogCall(r.log, "PingOnce", conf)(out, &err)

	*out, err = r.visor.PingOnce(conf)
	return err
}

// StopPing stops the ping conn.
func (r *RPC) StopPing(pk *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StopPing", pk)(nil, &err)

	return r.visor.StopPing(*pk)
}

// StopAllPingsOut is the output for StopAllPings RPC.
type StopAllPingsOut struct {
	Stopped int
	Errors  []string
}

// StopAllPings stops all active ping connections.
func (r *RPC) StopAllPings(_ *struct{}, out *StopAllPingsOut) (err error) {
	defer rpcutil.LogCall(r.log, "StopAllPings", nil)(out, &err)

	count, errs, err := r.visor.StopAllPings()
	out.Stopped = count
	out.Errors = errs
	return err
}

// DialDmsgPing dials to a remote visor over dmsg for ping.
func (r *RPC) DialDmsgPing(pk *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DialDmsgPing", pk)(nil, &err)

	return r.visor.DialDmsgPing(*pk)
}

// DmsgPing pings over dmsg connection.
func (r *RPC) DmsgPing(conf PingConfig, out *[]time.Duration) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgPing", conf)(out, &err)

	*out, err = r.visor.DmsgPing(conf)
	return err
}

// DmsgPingOnce performs a single ping over dmsg connection.
func (r *RPC) DmsgPingOnce(conf PingConfig, out *time.Duration) (err error) {
	defer rpcutil.LogCall(r.log, "DmsgPingOnce", conf)(out, &err)

	*out, err = r.visor.DmsgPingOnce(conf)
	return err
}

// StopDmsgPing stops the dmsg ping conn.
func (r *RPC) StopDmsgPing(pk *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "StopDmsgPing", pk)(nil, &err)

	return r.visor.StopDmsgPing(*pk)
}

// DialDmsgPingViaServerIn is the input for DialDmsgPingViaServer.
type DialDmsgPingViaServerIn struct {
	PK       cipher.PubKey
	ServerPK cipher.PubKey
}

// DialDmsgPingViaServer dials to a remote visor over dmsg via a specific server.
func (r *RPC) DialDmsgPingViaServer(in *DialDmsgPingViaServerIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DialDmsgPingViaServer", in)(nil, &err)

	return r.visor.DialDmsgPingViaServer(in.PK, in.ServerPK)
}

// GetDmsgPingServerPK returns the DMSG server PK used for a ping connection.
func (r *RPC) GetDmsgPingServerPK(pk *cipher.PubKey, out *cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetDmsgPingServerPK", pk)(out, &err)

	*out, err = r.visor.GetDmsgPingServerPK(*pk)
	return err
}

// GetRemoteDmsgServers returns the DMSG servers a remote visor is connected to.
func (r *RPC) GetRemoteDmsgServers(pk *cipher.PubKey, out *[]cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetRemoteDmsgServers", pk)(out, &err)

	*out, err = r.visor.GetRemoteDmsgServers(*pk)
	return err
}

// GetPreferredDmsgServer returns the lowest-latency DMSG server shared with a remote visor.
func (r *RPC) GetPreferredDmsgServer(remotePK *cipher.PubKey, out *cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetPreferredDmsgServer", remotePK)(out, &err)

	*out, err = r.visor.GetPreferredDmsgServer(*remotePK)
	return err
}

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

// TestVisor trying to test viosr by pinging to public visor.
func (r *RPC) TestVisor(conf PingConfig, out *[]TestResult) (err error) {
	defer rpcutil.LogCall(r.log, "TestVisor", conf)(out, &err)

	*out, err = r.visor.TestVisor(conf)
	return err
}

// TestProxy tests proxy servers by connecting through them.
func (r *RPC) TestProxy(conf ProxyTestConfig, out *[]ProxyTestResult) (err error) {
	defer rpcutil.LogCall(r.log, "TestProxy", conf)(out, &err)

	*out, err = r.visor.TestProxy(conf)
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

// SetEmbeddedProxyEnabledRequest is the RPC argument for SetEmbeddedProxyEnabled.
type SetEmbeddedProxyEnabledRequest struct {
	Kind   string
	Enable bool
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

// SetEmbeddedProxyUpstreamRequest is the RPC argument for SetEmbeddedProxyUpstream.
type SetEmbeddedProxyUpstreamRequest struct {
	Kind string
	Addr string // upstream SOCKS5 address, "" to clear
}

// SetEmbeddedProxyUpstream changes the upstream SOCKS5 address for a resolver.
func (r *RPC) SetEmbeddedProxyUpstream(req *SetEmbeddedProxyUpstreamRequest, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetEmbeddedProxyUpstream", req)(nil, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.SetEmbeddedProxyUpstream(req.Kind, req.Addr)
}

// DmsgHTTP performs an HTTP request over dmsg using the visor's dmsg client.
// DmsgProbeRequest is the RPC argument for DmsgProbe.
type DmsgProbeRequest struct {
	PK   cipher.PubKey
	Port uint16
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

// TPSAddTransportIn is the input for TPSAddTransport RPC.
type TPSAddTransportIn struct {
	TargetPK cipher.PubKey
	RemotePK cipher.PubKey
	TpType   string
}

// TPSRemoveTransportIn is the input for TPSRemoveTransport RPC.
type TPSRemoveTransportIn struct {
	TargetPK cipher.PubKey
	TpID     uuid.UUID
}

// TPSStatus returns the status of the embedded TPS.
func (r *RPC) TPSStatus(_ *struct{}, out *TPSStatus) (err error) {
	defer rpcutil.LogCall(r.log, "TPSStatus", nil)(out, &err)

	status, err := r.visor.TPSStatus()
	if err != nil {
		return err
	}
	*out = *status
	return nil
}

// TPSAddTransport adds a transport on a target visor using the embedded TPS.
func (r *RPC) TPSAddTransport(in *TPSAddTransportIn, out *TPSTransportResponse) (err error) {
	defer rpcutil.LogCall(r.log, "TPSAddTransport", in)(out, &err)

	resp, err := r.visor.TPSAddTransport(in.TargetPK, in.RemotePK, in.TpType)
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// TPSRemoveTransport removes a transport on a target visor using the embedded TPS.
func (r *RPC) TPSRemoveTransport(in *TPSRemoveTransportIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "TPSRemoveTransport", in)(nil, &err)
	return r.visor.TPSRemoveTransport(in.TargetPK, in.TpID)
}

// TPSGetTransports gets transports from a target visor using the embedded TPS.
func (r *RPC) TPSGetTransports(targetPK *cipher.PubKey, out *[]TPSTransportResponse) (err error) {
	defer rpcutil.LogCall(r.log, "TPSGetTransports", targetPK)(out, &err)

	resp, err := r.visor.TPSGetTransports(*targetPK)
	if err != nil {
		return err
	}
	*out = resp
	return nil
}

// GetTransportSetupNodes returns the whitelisted transport setup node public keys.
func (r *RPC) GetTransportSetupNodes(_ *struct{}, out *[]cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetTransportSetupNodes", nil)(out, &err)

	nodes, err := r.visor.GetTransportSetupNodes()
	if err != nil {
		return err
	}
	*out = nodes
	return nil
}

// GetTransportSetupNodesSorted returns TPS nodes sorted by health (healthy first).
func (r *RPC) GetTransportSetupNodesSorted(_ *struct{}, out *[]cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetTransportSetupNodesSorted", nil)(out, &err)

	nodes, err := r.visor.GetTransportSetupNodesSorted()
	if err != nil {
		return err
	}
	*out = nodes
	return nil
}

// GetRouteSetupNodesSorted returns RSN nodes sorted by health (healthy first).
func (r *RPC) GetRouteSetupNodesSorted(_ *struct{}, out *[]cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetRouteSetupNodesSorted", nil)(out, &err)

	nodes, err := r.visor.GetRouteSetupNodesSorted()
	if err != nil {
		return err
	}
	*out = nodes
	return nil
}

// GetTPSHealth returns health status for all configured TPS nodes.
func (r *RPC) GetTPSHealth(_ *struct{}, out *[]NodeHealth) (err error) {
	defer rpcutil.LogCall(r.log, "GetTPSHealth", nil)(out, &err)

	health, err := r.visor.GetTPSHealth()
	if err != nil {
		return err
	}
	*out = health
	return nil
}

// GetRSNHealth returns health status for all configured RSN nodes.
func (r *RPC) GetRSNHealth(_ *struct{}, out *[]NodeHealth) (err error) {
	defer rpcutil.LogCall(r.log, "GetRSNHealth", nil)(out, &err)

	health, err := r.visor.GetRSNHealth()
	if err != nil {
		return err
	}
	*out = health
	return nil
}

// RouteSetupStats returns a snapshot of the embedded Route Setup Node's
// request statistics.
func (r *RPC) RouteSetupStats(_ *struct{}, out *setupmetrics.StatsSnapshot) (err error) {
	defer rpcutil.LogCall(r.log, "RouteSetupStats", nil)(out, &err)
	snap, err := r.visor.RouteSetupStats()
	if err != nil {
		return err
	}
	*out = *snap
	return nil
}

// ResetRouteSetupStats clears all counters on the embedded RSN stats collector.
func (r *RPC) ResetRouteSetupStats(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "ResetRouteSetupStats", nil)(nil, &err)
	return r.visor.ResetRouteSetupStats()
}

// TPSExternalHealthCheck dials an external TPS over dmsg and performs a health check.
func (r *RPC) TPSExternalHealthCheck(tpsPK *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "TPSExternalHealthCheck", tpsPK)(nil, &err)
	return r.visor.TPSExternalHealthCheck(*tpsPK)
}

// TPSExternalAddTransportIn is the input for TPSExternalAddTransport RPC.
type TPSExternalAddTransportIn struct {
	TPSPK    cipher.PubKey
	TargetPK cipher.PubKey
	RemotePK cipher.PubKey
	TpType   string
}

// TPSExternalAddTransport requests transport setup via an external TPS.
func (r *RPC) TPSExternalAddTransport(in *TPSExternalAddTransportIn, out *TPSTransportResponse) (err error) {
	defer rpcutil.LogCall(r.log, "TPSExternalAddTransport", in)(out, &err)

	resp, err := r.visor.TPSExternalAddTransport(in.TPSPK, in.TargetPK, in.RemotePK, in.TpType)
	if err != nil {
		return err
	}
	*out = *resp
	return nil
}

// TPSExternalGetTransportsIn is the input for TPSExternalGetTransports RPC.
type TPSExternalGetTransportsIn struct {
	TPSPK    cipher.PubKey
	TargetPK cipher.PubKey
}

// TPSExternalGetTransports gets transports from a target visor via an external TPS.
func (r *RPC) TPSExternalGetTransports(in *TPSExternalGetTransportsIn, out *[]TPSTransportResponse) (err error) {
	defer rpcutil.LogCall(r.log, "TPSExternalGetTransports", in)(out, &err)

	resp, err := r.visor.TPSExternalGetTransports(in.TPSPK, in.TargetPK)
	if err != nil {
		return err
	}
	*out = resp
	return nil
}

// DHTStatus returns the DHT node's status.
func (r *RPC) DHTStatus(_ *struct{}, out *DHTStatus) (err error) {
	defer rpcutil.LogCall(r.log, "DHTStatus", nil)(out, &err)
	status, err := r.visor.DHTStatus()
	if err != nil {
		return err
	}
	*out = *status
	return nil
}

// DHTGetIn is the input for DHTGet.
type DHTGetIn struct {
	PK   string `json:"pk"`
	Salt string `json:"salt"`
}

// DHTGet retrieves a value from the DHT.
func (r *RPC) DHTGet(in *DHTGetIn, out *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "DHTGet", in)(out, &err)
	data, err := r.visor.DHTGet(in.PK, in.Salt)
	if err != nil {
		return err
	}
	*out = data
	return nil
}

// DHTPutIn is the input for DHTPut.
type DHTPutIn struct {
	Value []byte `json:"value"`
	Seq   uint64 `json:"seq"`
	Salt  string `json:"salt"`
}

// DHTPut publishes a value to the DHT.
func (r *RPC) DHTPut(in *DHTPutIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DHTPut", in)(nil, &err)
	return r.visor.DHTPut(in.Value, in.Seq, in.Salt)
}

// DHTSetFullNode enables or disables full node mode.
func (r *RPC) DHTSetFullNode(in *bool, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DHTSetFullNode", in)(nil, &err)
	return r.visor.DHTSetFullNode(*in)
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

// AddHypervisor connects to a remote hypervisor at runtime.
func (r *RPC) AddHypervisor(in *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "AddHypervisor", in)(nil, &err)
	return r.visor.AddHypervisor(*in)
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

// CheckAREntry checks if a PK is registered in the address resolver.
func (r *RPC) CheckAREntry(pk *string, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "CheckAREntry", pk)(out, &err)
	result, err := r.visor.CheckAREntry(*pk)
	if err != nil {
		return err
	}
	*out = result
	return nil
}

// DHTGetAll returns all DHT items matching a salt as JSON.
func (r *RPC) DHTGetAll(salt *string, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "DHTGetAll", salt)(out, &err)
	result, err := r.visor.DHTGetAll(*salt)
	if err != nil {
		return err
	}
	*out = result
	return nil
}

// DHTSyncRequest is the request for DHTSync.
type DHTSyncRequest struct {
	RemotePK string `json:"remote_pk"`
	Salt     string `json:"salt"`
}

// DHTSync syncs items from a DHT full node.
func (r *RPC) DHTSync(req *DHTSyncRequest, out *int) (err error) {
	defer rpcutil.LogCall(r.log, "DHTSync", req)(out, &err)
	n, err := r.visor.DHTSync(req.RemotePK, req.Salt)
	if err != nil {
		return err
	}
	*out = n
	return nil
}

// TransportRPCCallRequest is the request for TransportRPCCall.
type TransportRPCCallRequest struct {
	RemotePK cipher.PubKey   `json:"remote_pk"`
	Method   string          `json:"method"`
	Args     json.RawMessage `json:"args,omitempty"` // JSON-encoded RPC arguments
}

// TransportRPCCall dials a remote visor's transport RPC and calls the
// specified method. The local visor acts as a proxy — it opens a VStream
// over an existing transport to the remote visor and forwards the RPC.
// The remote visor must have this visor's PK in its hypervisor/dmsgpty whitelist.
func (r *RPC) TransportRPCCall(req *TransportRPCCallRequest, out *json.RawMessage) (err error) {
	defer rpcutil.LogCall(r.log, "TransportRPCCall", req)(out, &err)

	v, ok := r.visor.(*Visor)
	if !ok || v.tpM == nil {
		return fmt.Errorf("transport manager not available")
	}

	log := logging.MustGetLogger("transport_rpc_proxy")
	rpcC, dialErr := DialTransportRPC(req.RemotePK, v.tpM, log)
	if dialErr != nil {
		return dialErr
	}
	defer rpcC.Close() //nolint:errcheck,gosec

	// Call the remote method, get raw JSON result.
	var rpcArgs interface{} = &struct{}{}
	if len(req.Args) > 0 {
		rpcArgs = &req.Args
	}
	var result json.RawMessage
	if callErr := rpcC.Call(req.Method, rpcArgs, &result); callErr != nil {
		return fmt.Errorf("remote RPC %s: %w", req.Method, callErr)
	}
	*out = result
	return nil
}

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

// HVAppArgs identifies an app on a remote visor.
type HVAppArgs struct {
	PK      cipher.PubKey
	AppName string
}

// HVMinHopsArgs sets min_hops on a remote visor.
type HVMinHopsArgs struct {
	PK   cipher.PubKey
	Hops uint16
}

// HVRewardArgs sets the reward address on a remote visor.
type HVRewardArgs struct {
	PK   cipher.PubKey
	Addr string
}

// HVTransportArgs identifies a transport on a remote visor.
type HVTransportArgs struct {
	PK  cipher.PubKey
	TID uuid.UUID
}

// HVRoutingRuleArgs identifies a routing rule on a remote visor.
type HVRoutingRuleArgs struct {
	PK  cipher.PubKey
	Key routing.RouteID
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

// HVAddTransportArgs creates a transport on a remote visor.
type HVAddTransportArgs struct {
	PK      cipher.PubKey
	Remote  cipher.PubKey
	TpType  string
	Label   string
	Timeout time.Duration
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

// HVAutoconnectArgs toggles public_autoconnect on a remote visor.
type HVAutoconnectArgs struct {
	PK     cipher.PubKey
	Enable bool
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
