// Package visor pkg/visor/rpc_client.go
package visor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/logserver"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	// ErrAlreadyServing is returned when an operation fails due to an operation
	// that is currently running.
	ErrAlreadyServing = errors.New("already serving")

	// ErrTimeout represents a timed-out call.
	ErrTimeout = errors.New("rpc client timeout")
)

// API provides methods to call an RPC Server.
// It implements API
type rpcClient struct {
	log     logrus.FieldLogger
	timeout time.Duration
	conn    io.ReadWriteCloser
	client  *rpc.Client
	prefix  string
	FixGob  bool
}

// NewRPCClient creates a new API.
func NewRPCClient(log logrus.FieldLogger, conn io.ReadWriteCloser, prefix string, timeout time.Duration) API {
	if log == nil {
		log = logging.MustGetLogger("visor_rpc_client")
	}
	return &rpcClient{
		log:     log,
		timeout: timeout,
		conn:    conn,
		client:  rpc.NewClient(conn),
		prefix:  prefix,
	}
}

// Close closes the RPC client connection.
func (rc *rpcClient) Close() error {
	if rc.client != nil {
		rc.log.Debug("Closing RPC client connection")
		err := rc.client.Close()
		if err != nil {
			rc.log.WithError(err).Debug("Error closing RPC client")
		}
		return err
	}
	return nil
}

// Call calls the internal rpc.Client with the serviceMethod arg prefixed.
func (rc *rpcClient) Call(method string, args, reply interface{}) error {
	ctx := context.Background()
	timeout := rc.timeout

	switch method {
	case "AddTransport":
		timeout = skyenv.TransportRPCTimeout
	case "Update":
		timeout = skyenv.UpdateRPCTimeout
	case "ServiceHealth":
		timeout = 2 * time.Minute
	}

	if timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, time.Now().Add(timeout))
		defer cancel()
	}

	select {
	case call := <-rc.client.Go(rc.prefix+"."+method, args, reply, nil).Done:
		return call.Error
	case <-ctx.Done():
		if err := rc.conn.Close(); err != nil {
			rc.log.WithError(err).Warn("Failed to close rpc client after timeout error.")
		}
		return ctx.Err()
	}
}

// Summary calls Summary.
func (rc *rpcClient) Summary() (*Summary, error) {
	out := new(Summary)
	err := rc.Call("Summary", &struct{}{}, out)
	return out, err
}

// Overview calls Overview.
func (rc *rpcClient) Overview() (*Overview, error) {
	out := new(Overview)
	err := rc.Call("Overview", &struct{}{}, out)
	return out, err
}

// Health calls Health
func (rc *rpcClient) Health() (*HealthInfo, error) {
	hi := &HealthInfo{}
	err := rc.Call("Health", &struct{}{}, hi)
	return hi, err
}

// IsStartupComplete calls IsStartupComplete
func (rc *rpcClient) IsStartupComplete() bool {
	var out bool
	if err := rc.Call("IsStartupComplete", &struct{}{}, &out); err != nil {
		return false
	}
	return out
}

// EnableHypervisor calls EnableHypervisor (runtime only, no persist).
func (rc *rpcClient) EnableHypervisor() error {
	persist := false
	return rc.Call("EnableHypervisor", &persist, &struct{}{})
}

// DisableHypervisor calls DisableHypervisor (runtime only, no persist).
func (rc *rpcClient) DisableHypervisor() error {
	persist := false
	return rc.Call("DisableHypervisor", &persist, &struct{}{})
}

// EnableHypervisorPersist calls EnableHypervisor with persist flag.
func (rc *rpcClient) EnableHypervisorPersist(persist bool) error {
	return rc.Call("EnableHypervisor", &persist, &struct{}{})
}

// DisableHypervisorPersist calls DisableHypervisor with persist flag.
func (rc *rpcClient) DisableHypervisorPersist(persist bool) error {
	return rc.Call("DisableHypervisor", &persist, &struct{}{})
}

// IsHypervisorEnabled calls IsHypervisorEnabled
func (rc *rpcClient) IsHypervisorEnabled() bool {
	var out bool
	if err := rc.Call("IsHypervisorEnabled", &struct{}{}, &out); err != nil {
		return false
	}
	return out
}

// Uptime calls Uptime
func (rc *rpcClient) Uptime() (float64, error) {
	var out float64
	err := rc.Call("Uptime", &struct{}{}, &out)
	return out, err
}

// UptimeHistory calls UptimeHistory.
func (rc *rpcClient) UptimeHistory(args UptimeHistoryArgs) (*UptimeHistoryResponse, error) {
	var resp UptimeHistoryResponse
	if err := rc.Call("UptimeHistory", &args, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SetRewardAddress implements API.
func (rc *rpcClient) SetRewardAddress(r string) (rConfig string, err error) {
	err = rc.Call("SetRewardAddress", &r, &rConfig)
	if err != nil {
		return "", err
	}
	return rConfig, err
}

// SetLANDmsgServer implements API.
func (rc *rpcClient) SetLANDmsgServer(info LANDmsgServerInfo) error {
	var ok bool
	return rc.Call("SetLANDmsgServer", &info, &ok)
}

// GetRewardAddress implements API.
func (rc *rpcClient) GetRewardAddress() (rConfig string, err error) {
	err = rc.Call("GetRewardAddress", &struct{}{}, &rConfig)
	return rConfig, err
}

// DeleteRewardAddress implements API.
func (rc *rpcClient) DeleteRewardAddress() (err error) {
	return rc.Call("DeleteRewardAddress", &struct{}{}, &struct{}{})
}

// Apps calls Apps.
func (rc *rpcClient) Apps() ([]*appserver.AppState, error) {
	states := make([]*appserver.AppState, 0)
	err := rc.Call("Apps", &struct{}{}, &states)
	return states, err
}

// App calls App.
func (rc *rpcClient) App(appName string) (*appserver.AppState, error) {
	var state *appserver.AppState
	err := rc.Call("App", appName, &state)
	return state, err
}

// StartApp calls StartApp.
func (rc *rpcClient) StartApp(appName string) error {
	return rc.StartAppWithMode(appName, "")
}

// StartAppWithMode calls StartApp with launcher mode override.
func (rc *rpcClient) StartAppWithMode(appName, launcherMode string) error {
	return rc.Call("StartApp", &StartAppIn{
		AppName:      appName,
		LauncherMode: launcherMode,
	}, &struct{}{})
}

// AddApp calls AddApp.
func (rc *rpcClient) AddApp(appName, binaryName string) error {
	return rc.Call("AddApp", &SetAppAddIn{
		AppName:    appName,
		BinaryName: binaryName,
	}, &struct{}{})
}

// RegisterApp calls RegisterApp.
func (rc *rpcClient) RegisterApp(procConf appcommon.ProcConfig) (appcommon.ProcKey, error) {
	var procKey appcommon.ProcKey
	err := rc.Call("RegisterApp", procConf, &procKey)
	return procKey, err
}

// DeregisterApp calls DeregisterApp.
func (rc *rpcClient) DeregisterApp(procKey appcommon.ProcKey) error {
	return rc.Call("DeregisterApp", procKey, &struct{}{})
}

// StopApp calls StopApp.
func (rc *rpcClient) StopApp(appName string) error {
	return rc.Call("StopApp", &appName, &struct{}{})
}

// KillApp calls KillApp.
func (rc *rpcClient) KillApp(appName string) error {
	return rc.Call("KillApp", &appName, &struct{}{})
}

// StartVPNClient calls StartVPNClient.
func (rc *rpcClient) StartVPNClient(pk cipher.PubKey) error {
	return rc.StartVPNClientWithMode(pk, "")
}

// StartVPNClientWithMode calls StartVPNClient with launcher mode override.
func (rc *rpcClient) StartVPNClientWithMode(pk cipher.PubKey, launcherMode string) error {
	return rc.Call("StartVPNClient", &StartVPNClientIn{
		PK:           pk,
		LauncherMode: launcherMode,
	}, &struct{}{})
}

// StopVPNClient calls StopVPNClient.
func (rc *rpcClient) StopVPNClient(appName string) error {
	return rc.Call("StopVPNClient", &appName, &struct{}{})
}

// FetchUptimeTrackerData calls FetchUptimeTrackerData.
func (rc *rpcClient) FetchUptimeTrackerData(pk string) ([]byte, error) {
	var data []byte
	err := rc.Call("FetchUptimeTrackerData", pk, &data)
	return data, err
}

// StartSkysocksClient calls StartSkysocksClient.
func (rc *rpcClient) StartSkysocksClient(pk string) error {
	return rc.Call("StartSkysocksClient", pk, &struct{}{})
}

// StopSkysocksCliens calls StopSkysocksCliens.
func (rc *rpcClient) StopSkysocksClients() error {
	return rc.Call("StopSkysocksClients", &struct{}{}, &struct{}{})
}

// SetAppDetailedStatus sets app's detailed state.
func (rc *rpcClient) SetAppDetailedStatus(appName, status string) error {
	return rc.Call("SetAppDetailedStatus", &SetAppStatusIn{
		AppName: appName,
		Status:  status,
	}, &struct{}{})
}

// SetAppError sets app's error.
func (rc *rpcClient) SetAppError(appName, appErr string) error {
	return rc.Call("SetAppError", &SetAppErrorIn{
		AppName: appName,
		Err:     appErr,
	}, &struct{}{})
}

// RestartApp calls `RestartApp`.
func (rc *rpcClient) RestartApp(appName string) error {
	return rc.Call("RestartApp", &appName, &struct{}{})
}

// SetAutoStart calls SetAutoStart.
func (rc *rpcClient) SetAutoStart(appName string, autostart bool) error {
	return rc.Call("SetAutoStart", &SetAutoStartIn{
		AppName:   appName,
		AutoStart: autostart,
	}, &struct{}{})
}

// SetAppWhitelist calls SetAppWhitelist.
func (rc *rpcClient) SetAppWhitelist(appName, whitelist string) error {
	return rc.Call("SetAppWhitelist", &SetAppWhitelistIn{
		AppName:   appName,
		Whitelist: whitelist,
	}, &struct{}{})
}

// SetAppPK calls SetAppPK.
func (rc *rpcClient) SetAppPK(appName string, pk cipher.PubKey) error {
	return rc.Call("SetAppPK", &SetAppPKIn{
		AppName: appName,
		PK:      pk,
	}, &struct{}{})
}

// SetAppKillswitch implements API.
func (rc *rpcClient) SetAppKillswitch(appName string, killswitch bool) error {
	return rc.Call("SetAppKillswitch", &SetAppBoolIn{
		AppName: appName,
		Val:     killswitch,
	}, &struct{}{})
}

// SetAppKillswitch implements API.
func (rc *rpcClient) SetAppNetworkInterface(appName, netifc string) error {
	return rc.Call("SetAppNetworkInterface", &SetAppNetworkInterfaceIn{
		AppName: appName,
		NetIfc:  netifc,
	}, &struct{}{})
}

// SetAppSecure implements API.
func (rc *rpcClient) SetAppSecure(appName string, isSecure bool) error {
	return rc.Call("SetAppSecure", &SetAppBoolIn{
		AppName: appName,
		Val:     isSecure,
	}, &struct{}{})
}

// SetAppAddress implements API.
func (rc *rpcClient) SetAppAddress(appName string, address string) error {
	return rc.Call("SetAppAddress", &SetAppStringIn{
		AppName: appName,
		Val:     address,
	}, &struct{}{})
}

// SetAppDNS implements API.
func (rc *rpcClient) SetAppDNS(appName string, dnsAddr string) error {
	return rc.Call("SetAppDNS", &SetAppStringIn{
		AppName: appName,
		Val:     dnsAddr,
	}, &struct{}{})
}

// DoCustomSetting implements API.
func (rc *rpcClient) DoCustomSetting(appName string, customSetting map[string]any) error {
	return rc.Call("DoCustomSetting", &SetAppMapIn{
		AppName: appName,
		Val:     customSetting,
	}, &struct{}{})
}

// DeleteApp implements API.
func (rc *rpcClient) DeleteApp(appName string) error {
	return rc.Call("DeleteApp", &AppNameIn{AppName: appName}, &struct{}{})
}

// SetAppEnv implements API.
func (rc *rpcClient) SetAppEnv(appName, key, value string) error {
	return rc.Call("SetAppEnv", &SetAppEnvIn{
		AppName: appName,
		Key:     key,
		Value:   value,
	}, &struct{}{})
}

// SetAppEnvBatch implements API.
func (rc *rpcClient) SetAppEnvBatch(appName string, env map[string]string) error {
	return rc.Call("SetAppEnvBatch", &SetAppEnvBatchIn{
		AppName: appName,
		Env:     env,
	}, &struct{}{})
}

// LogsSince calls LogsSince
func (rc *rpcClient) LogsSince(timestamp time.Time, appName string) ([]string, error) {
	res := make([]string, 0)

	err := rc.Call("LogsSince", &AppLogsRequest{
		TimeStamp: timestamp,
		AppName:   appName,
	}, &res)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (rc *rpcClient) GetAppStats(appName string) (appserver.AppStats, error) {
	var stats appserver.AppStats

	if err := rc.Call("GetAppStats", &appName, &stats); err != nil {
		return appserver.AppStats{}, err
	}

	return stats, nil
}

func (rc *rpcClient) GetAppError(appName string) (string, error) {
	var appErr string

	if err := rc.Call("GetAppError", &appName, &appErr); err != nil {
		return appErr, err
	}

	return appErr, nil
}

// GetAppConnectionsSummary get connections stats for the app.
func (rc *rpcClient) GetAppConnectionsSummary(appName string) ([]appserver.ConnectionSummary, error) {
	var summary []appserver.ConnectionSummary

	if err := rc.Call("GetAppConnectionsSummary", &appName, &summary); err != nil {
		return nil, err
	}

	return summary, nil
}

// TransportTypes calls TransportTypes.
func (rc *rpcClient) TransportTypes() ([]string, error) {
	var types []string
	err := rc.Call("TransportTypes", &struct{}{}, &types)
	return types, err
}

// Transports calls Transports.
func (rc *rpcClient) Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error) {
	transports := make([]*TransportSummary, 0)
	err := rc.Call("Transports", &TransportsIn{
		FilterTypes:   types,
		FilterPubKeys: pks,
		ShowLogs:      logs,
	}, &transports)
	return transports, err
}

// Transport calls Transport.
func (rc *rpcClient) Transport(tid uuid.UUID) (*TransportSummary, error) {
	var summary TransportSummary
	err := rc.Call("Transport", &tid, &summary)
	return &summary, err
}

// AddTransport calls AddTransport.
func (rc *rpcClient) AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration, label string, noRegister bool, skipLatencyProbe bool) (*TransportSummary, error) {
	var summary TransportSummary
	err := rc.Call("AddTransport", &AddTransportIn{
		RemotePK:         remote,
		TpType:           tpType,
		Timeout:          timeout,
		Label:            label,
		NoRegister:       noRegister,
		SkipLatencyProbe: skipLatencyProbe,
	}, &summary)

	return &summary, err
}

// SetSTCPAddr injects an STCP PK table entry at runtime.
func (rc *rpcClient) SetSTCPAddr(pk cipher.PubKey, addr string) error {
	return rc.Call("SetSTCPAddr", &SetSTCPAddrIn{PK: pk, Addr: addr}, &struct{}{})
}

// RemoveTransport calls RemoveTransport.
func (rc *rpcClient) RemoveTransport(tid uuid.UUID) error {
	return rc.Call("RemoveTransport", &tid, &struct{}{})
}

// RemoveAllTransports calls RemoveAllTransports.
func (rc *rpcClient) RemoveAllTransports() error {
	return rc.Call("RemoveAllTransports", &struct{}{}, &struct{}{})
}

func (rc *rpcClient) DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.Entry, error) {
	entries := make([]*transport.Entry, 0)
	err := rc.Call("DiscoverTransportsByPK", &pk, &entries)
	return entries, err
}

func (rc *rpcClient) DiscoverTransportByID(id uuid.UUID) (*transport.Entry, error) {
	var entry transport.Entry
	err := rc.Call("DiscoverTransportByID", &id, &entry)
	return &entry, err
}

// SetPublicAutoconnect implements API.
func (rc *rpcClient) SetPublicAutoconnect(pAc bool) error {
	return rc.Call("SetPublicAutoconnect", &pAc, &struct{}{})
}

// SetIsPublic implements API.
func (rc *rpcClient) SetIsPublic(isPublic bool) error {
	return rc.Call("SetIsPublic", &isPublic, &struct{}{})
}

// GetIsPublic implements API.
func (rc *rpcClient) GetIsPublic() bool {
	var out bool
	if err := rc.Call("GetIsPublic", &struct{}{}, &out); err != nil {
		return false
	}
	return out
}

// SetRuntimeConfig implements API.
func (rc *rpcClient) SetRuntimeConfig(rawJSON []byte) error {
	return rc.Call("SetRuntimeConfig", &rawJSON, &struct{}{})
}

// LocalTransportStats implements API.
func (rc *rpcClient) LocalTransportStats() (*LocalTransportStatsResponse, error) {
	var resp LocalTransportStatsResponse
	if err := rc.Call("LocalTransportStats", &struct{}{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// LocalUptimeStats implements API.
func (rc *rpcClient) LocalUptimeStats(args LocalUptimeArgs) (*LocalUptimeResponse, error) {
	var resp LocalUptimeResponse
	if err := rc.Call("LocalUptimeStats", &args, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// FetchCXO implements API.
func (rc *rpcClient) FetchCXO(args FetchCXOArgs) (*FetchCXOResult, error) {
	var resp FetchCXOResult
	if err := rc.Call("FetchCXO", &args, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetRuntimeConfig implements API.
func (rc *rpcClient) GetRuntimeConfig() ([]byte, error) {
	var out []byte
	err := rc.Call("GetRuntimeConfig", &struct{}{}, &out)
	return out, err
}

// GetConfigPath implements API.
func (rc *rpcClient) GetConfigPath() (string, error) {
	var out string
	err := rc.Call("GetConfigPath", &struct{}{}, &out)
	return out, err
}

// StartPublicAutoconnect implements API.
func (rc *rpcClient) StartPublicAutoconnect() error {
	return rc.Call("StartPublicAutoconnect", &struct{}{}, &struct{}{})
}

// StopPublicAutoconnect implements API.
func (rc *rpcClient) StopPublicAutoconnect() error {
	return rc.Call("StopPublicAutoconnect", &struct{}{}, &struct{}{})
}

// PublicAutoconnectStatus implements API.
func (rc *rpcClient) PublicAutoconnectStatus() (bool, error) {
	var status bool
	err := rc.Call("PublicAutoconnectStatus", &struct{}{}, &status)
	return status, err
}

// SetExistingTPOnly sets whether to only use existing transports for routing.
func (rc *rpcClient) SetExistingTPOnly(enabled bool) error {
	return rc.Call("SetExistingTPOnly", &enabled, &struct{}{})
}

// SetForceLocalRoutes sets whether to skip the route finder and use local route calculation.
func (rc *rpcClient) SetForceLocalRoutes(enabled bool) error {
	return rc.Call("SetForceLocalRoutes", &enabled, &struct{}{})
}

// SetMuxRoutes sets the number of parallel mux routes for new connections.
func (rc *rpcClient) SetMuxRoutes(n int) error {
	return rc.Call("SetMuxRoutes", &n, &struct{}{})
}

// SetMuxMode sets the weight distribution mode for mux transport selection.
func (rc *rpcClient) SetMuxMode(mode string) error {
	return rc.Call("SetMuxMode", &mode, &struct{}{})
}

// AddMuxRoute adds a leg to the app's active rg using the
// caller-supplied forward/reverse hop lists (same shape 'cli route
// calc' emits). srcPort = 0 picks the only matching rg, or errors
// with the candidate list when more than one is active for the app.
func (rc *rpcClient) AddMuxRoute(appName string, fwd, rev []routing.Hop, srcPort uint16) error {
	return rc.Call("AddMuxRoute", &MuxRouteInput{AppName: appName, Forward: fwd, Reverse: rev, SrcPort: srcPort}, &struct{}{})
}

// RemoveMuxRoute drops the leg over tpID from the app's active rg.
// srcPort disambiguates as in AddMuxRoute.
func (rc *rpcClient) RemoveMuxRoute(appName string, tpID uuid.UUID, srcPort uint16) error {
	return rc.Call("RemoveMuxRoute", &MuxRouteInput{AppName: appName, TransportID: tpID, SrcPort: srcPort}, &struct{}{})
}

// ActiveRoutes returns all active routes with app associations and live stats.
func (rc *rpcClient) ActiveRoutes() ([]AppRouteStatus, error) {
	var routes []AppRouteStatus
	err := rc.Call("ActiveRoutes", &struct{}{}, &routes)
	return routes, err
}

// RoutingRules calls RoutingRules.
func (rc *rpcClient) RoutingRules() ([]routing.Rule, error) {
	entries := make([]routing.Rule, 0)
	err := rc.Call("RoutingRules", &struct{}{}, &entries)
	return entries, err
}

// RoutingRule calls RoutingRule.
func (rc *rpcClient) RoutingRule(key routing.RouteID) (routing.Rule, error) {
	var rule routing.Rule
	err := rc.Call("RoutingRule", &key, &rule)
	return rule, err
}

// SaveRoutingRule calls SaveRoutingRule.
func (rc *rpcClient) SaveRoutingRule(rule routing.Rule) error {
	return rc.Call("SaveRoutingRule", &rule, &struct{}{})
}

// RemoveRoutingRule calls RemoveRoutingRule.
func (rc *rpcClient) RemoveRoutingRule(key routing.RouteID) error {
	return rc.Call("RemoveRoutingRule", &key, &struct{}{})
}

// RouteGroups calls RouteGroups.
func (rc *rpcClient) RouteGroups() ([]RouteGroupInfo, error) {
	var routegroups []RouteGroupInfo
	err := rc.Call("RouteGroups", &struct{}{}, &routegroups)
	return routegroups, err
}

// RouteGroupMuxInfo calls RouteGroupMuxInfo.
func (rc *rpcClient) RouteGroupMuxInfo(appName string) ([]MuxRouteGroupInfo, error) {
	var infos []MuxRouteGroupInfo
	err := rc.Call("RouteGroupMuxInfo", &appName, &infos)
	return infos, err
}

// FetchServiceData calls FetchServiceData.
func (rc *rpcClient) FetchServiceData(service, path string) ([]byte, error) {
	var data []byte
	err := rc.Call("FetchServiceData", &FetchServiceDataIn{Service: service, Path: path}, &data)
	return data, err
}

// ServiceHealth calls ServiceHealth.
func (rc *rpcClient) ServiceHealth() ([]ServiceHealthEntry, error) {
	var entries []ServiceHealthEntry
	err := rc.Call("ServiceHealth", &struct{}{}, &entries)
	return entries, err
}

// Reload calls Reload.
func (rc *rpcClient) Reload() error {
	return rc.Call("Reload", &struct{}{}, &struct{}{})
}

// Shutdown calls Shutdown.
func (rc *rpcClient) Shutdown() error {
	return rc.Call("Shutdown", &struct{}{}, &struct{}{})
}

// Exec calls Exec.
func (rc *rpcClient) Exec(command string) ([]byte, error) {
	output := make([]byte, 0)
	err := rc.Call("Exec", &command, &output)
	return output, err
}

// RuntimeLogs calls RuntimeLogs.
func (rc *rpcClient) RuntimeLogs() (string, error) {
	var logs string
	err := rc.Call("RuntimeLogs", &struct{}{}, &logs)
	return logs, err
}

// RuntimeLogsSince calls RuntimeLogsSince. Pass the previous
// response's Latest as `since` to receive only newly-arrived
// entries; pass 0 to fetch the full buffer.
func (rc *rpcClient) RuntimeLogsSince(since int64) (RuntimeLogsDelta, error) {
	var delta RuntimeLogsDelta
	err := rc.Call("RuntimeLogsSince", &since, &delta)
	return delta, err
}

// HostStats calls HostStats.
func (rc *rpcClient) HostStats() (*HostStatsInfo, error) {
	var stats HostStatsInfo
	if err := rc.Call("HostStats", &struct{}{}, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// NetworkView calls NetworkView.
func (rc *rpcClient) NetworkView() (*NetworkViewResponse, error) {
	var resp NetworkViewResponse
	if err := rc.Call("NetworkView", &struct{}{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SkychatPasswordIsSet calls SkychatPasswordIsSet.
func (rc *rpcClient) SkychatPasswordIsSet() (bool, error) {
	var out bool
	if err := rc.Call("SkychatPasswordIsSet", &struct{}{}, &out); err != nil {
		return false, err
	}
	return out, nil
}

// SetSkychatPassword calls SetSkychatPassword.
func (rc *rpcClient) SetSkychatPassword(oldPassword, newPassword string) error {
	return rc.Call("SetSkychatPassword", &SkychatPasswordChangeIn{
		OldPassword: oldPassword,
		NewPassword: newPassword,
	}, &struct{}{})
}

// ClearSkychatPassword calls ClearSkychatPassword.
func (rc *rpcClient) ClearSkychatPassword(oldPassword string) error {
	return rc.Call("ClearSkychatPassword", &oldPassword, &struct{}{})
}

// SkychatLocalAddr calls SkychatLocalAddr.
func (rc *rpcClient) SkychatLocalAddr() (string, error) {
	var addr string
	if err := rc.Call("SkychatLocalAddr", &struct{}{}, &addr); err != nil {
		return "", err
	}
	return addr, nil
}

// RuntimeStats calls RuntimeStats.
func (rc *rpcClient) RuntimeStats() (*RuntimeStatsInfo, error) {
	var stats RuntimeStatsInfo
	err := rc.Call("RuntimeStats", &struct{}{}, &stats)
	return &stats, err
}

// SetMinHops sets the min_hops from visor routing config
func (rc *rpcClient) SetMinHops(hops uint16) error {
	err := rc.Call("SetMinHops", &hops, &struct{}{})
	return err
}

// GetMinHops returns the visor's configured routing.min_hops value.
func (rc *rpcClient) GetMinHops() (uint16, error) {
	var out uint16
	if err := rc.Call("GetMinHops", &struct{}{}, &out); err != nil {
		return 0, err
	}
	return out, nil
}

// SetCalculateRoutes sets the calculate_routes from visor routing config
func (rc *rpcClient) SetCalculateRoutes(enabled bool) error {
	err := rc.Call("SetCalculateRoutes", &enabled, &struct{}{})
	return err
}

// GetCalculateRoutes gets the calculate_routes from visor routing config
func (rc *rpcClient) GetCalculateRoutes() (bool, error) {
	var enabled bool
	err := rc.Call("GetCalculateRoutes", &struct{}{}, &enabled)
	return enabled, err
}

// SetPersistentTransports sets the persistent_transports from visor routing config
func (rc *rpcClient) SetPersistentTransports(pts []transport.PersistentTransports) error {
	err := rc.Call("SetPersistentTransports", &pts, &struct{}{})
	return err
}

// GetPersistentTransports gets the persistent_transports from visor routing config
func (rc *rpcClient) GetPersistentTransports() ([]transport.PersistentTransports, error) {
	var tps []transport.PersistentTransports
	err := rc.Call("GetPersistentTransports", &struct{}{}, &tps)
	return tps, err
}

// GetTransportLogs gets transport log entries from the last N days
func (rc *rpcClient) GetTransportLogs(days int) ([]TransportLogEntry, error) {
	var entries []TransportLogEntry
	err := rc.Call("GetTransportLogs", &days, &entries)
	return entries, err
}

// SetLogRotationInterval sets the log_rotation_interval from visor config
func (rc *rpcClient) SetLogRotationInterval(d visorconfig.Duration) error {
	err := rc.Call("SetLogRotationInterval", &d, &struct{}{})
	return err
}

// GetLogRotationInterval gets the log_rotation_interval from visor config
func (rc *rpcClient) GetLogRotationInterval() (visorconfig.Duration, error) {
	var d visorconfig.Duration
	err := rc.Call("GetLogRotationInterval", &struct{}{}, &d)
	return d, err
}

// StatusMessage defines a status of visor update.
type StatusMessage struct {
	Text    string
	IsError bool
}

// VPNServers calls VPNServers.
func (rc *rpcClient) VPNServers(version, country string) ([]servicedisc.Service, error) {
	output := []servicedisc.Service{}
	err := rc.Call("VPNServers", &FilterServersIn{ // nolint
		Version: version,
		Country: country,
	}, &output)
	return output, err
}

// ProxyServers calls ProxyServers.
func (rc *rpcClient) ProxyServers(version, country string) ([]servicedisc.Service, error) {
	output := []servicedisc.Service{}
	err := rc.Call("ProxyServers", &FilterServersIn{ // nolint
		Version: version,
		Country: country,
	}, &output)
	return output, err
}

// DeregisterService calls DeregisterService.
func (rc *rpcClient) DeregisterService(pks []cipher.PubKey, serviceType string) error {
	return rc.Call("DeregisterService", &DeregisterServiceIn{
		PKs:         pks,
		ServiceType: serviceType,
	}, &struct{}{})
}

// RemoteVisors calls RemoteVisors.
func (rc *rpcClient) RemoteVisors() ([]string, error) {
	output := []string{}
	rc.Call("RemoteVisors", &struct{}{}, &output) // nolint
	return output, nil
}

// Ports calls Ports.
func (rc *rpcClient) Ports() (map[string]PortDetail, error) {
	output := map[string]PortDetail{}
	rc.Call("Ports", &struct{}{}, &output) // nolint
	return output, nil
}

// IsDMSGClientReady return availability of dsmg client
func (rc *rpcClient) IsDMSGClientReady() (bool, error) {
	var out bool
	err := rc.Call("IsDMSGClientReady", &struct{}{}, &out)
	return out, err
}

// DMSGServers returns list of connected DMSG servers with latencies
func (rc *rpcClient) DMSGServers() ([]DMSGServerInfo, error) {
	var out []DMSGServerInfo
	err := rc.Call("DMSGServers", &struct{}{}, &out)
	return out, err
}

// ConnectRawTCP calls ConnectRawTCP. network is "skynet" (default;
// empty string treated as skynet) or "dmsg".
func (rc *rpcClient) ConnectRawTCP(network string, remotePK cipher.PubKey, remotePort, localPort int) (uuid.UUID, error) {
	var out uuid.UUID
	err := rc.Call("ConnectRawTCP", &ConnectIn{
		Network:    network,
		RemotePK:   remotePK,
		RemotePort: remotePort,
		LocalPort:  localPort,
	}, &out)
	return out, err
}

// DisconnectRawTCP calls DisconnectRawTCP.
func (rc *rpcClient) DisconnectRawTCP(id uuid.UUID) error {
	err := rc.Call("DisconnectRawTCP", &id, &struct{}{})
	return err
}

// ListRawTCP calls ListRawTCP.
func (rc *rpcClient) ListRawTCP() (map[uuid.UUID]*appnet.RawTCPForwardConn, error) {
	var out map[uuid.UUID]*appnet.RawTCPForwardConn
	err := rc.Call("ListRawTCP", &struct{}{}, &out)
	return out, err
}

// RegisterTCPPort calls RegisterTCPPort.
func (rc *rpcClient) RegisterTCPPort(localPort int) error {
	return rc.Call("RegisterTCPPort", &localPort, &struct{}{})
}

// DeregisterTCPPort calls DeregisterTCPPort.
func (rc *rpcClient) DeregisterTCPPort(localPort int) error {
	err := rc.Call("DeregisterTCPPort", &localPort, &struct{}{})
	return err
}

// ListTCPPorts calls ListTCPPorts.
func (rc *rpcClient) ListTCPPorts() ([]int, error) {
	var out []int
	err := rc.Call("ListTCPPorts", &struct{}{}, &out)
	return out, err
}

// RegisterForwardedPort calls RegisterForwardedPort.
func (rc *rpcClient) RegisterForwardedPort(p ForwardedPort) error {
	return rc.Call("RegisterForwardedPort", &p, &struct{}{})
}

// UpdateForwardedPort calls UpdateForwardedPort.
func (rc *rpcClient) UpdateForwardedPort(p ForwardedPort) error {
	return rc.Call("UpdateForwardedPort", &p, &struct{}{})
}

// ListForwardedPorts calls ListForwardedPorts.
func (rc *rpcClient) ListForwardedPorts() ([]ForwardedPort, error) {
	var out []ForwardedPort
	err := rc.Call("ListForwardedPorts", &struct{}{}, &out)
	return out, err
}

// DialPing calls DialPing.
func (rc *rpcClient) DialPing(conf PingConfig) error {
	return rc.Call("DialPing", &conf, &struct{}{})
}

// Ping calls Ping.
func (rc *rpcClient) Ping(conf PingConfig) ([]time.Duration, error) {
	var latencies []time.Duration
	err := rc.Call("Ping", &conf, &latencies)
	return latencies, err
}

// PingOnce calls PingOnce.
func (rc *rpcClient) PingOnce(conf PingConfig) (time.Duration, error) {
	var latency time.Duration
	err := rc.Call("PingOnce", &conf, &latency)
	return latency, err
}

// StopPing calls StopPing.
func (rc *rpcClient) StopPing(pk cipher.PubKey) error {
	return rc.Call("StopPing", &pk, &struct{}{})
}

// StopAllPings calls StopAllPings.
func (rc *rpcClient) StopAllPings() (int, []string, error) {
	var out StopAllPingsOut
	err := rc.Call("StopAllPings", &struct{}{}, &out)
	return out.Stopped, out.Errors, err
}

// DialDmsgPing calls DialDmsgPing.
func (rc *rpcClient) DialDmsgPing(pk cipher.PubKey) error {
	return rc.Call("DialDmsgPing", &pk, &struct{}{})
}

// DmsgPing calls DmsgPing.
func (rc *rpcClient) DmsgPing(conf PingConfig) ([]time.Duration, error) {
	var latencies []time.Duration
	err := rc.Call("DmsgPing", &conf, &latencies)
	return latencies, err
}

// DmsgPingOnce calls DmsgPingOnce.
func (rc *rpcClient) DmsgPingOnce(conf PingConfig) (time.Duration, error) {
	var latency time.Duration
	err := rc.Call("DmsgPingOnce", &conf, &latency)
	return latency, err
}

// StopDmsgPing calls StopDmsgPing.
func (rc *rpcClient) StopDmsgPing(pk cipher.PubKey) error {
	return rc.Call("StopDmsgPing", &pk, &struct{}{})
}

// DialDmsgPingViaServer calls DialDmsgPingViaServer.
func (rc *rpcClient) DialDmsgPingViaServer(pk cipher.PubKey, serverPK cipher.PubKey) error {
	return rc.Call("DialDmsgPingViaServer", &DialDmsgPingViaServerIn{PK: pk, ServerPK: serverPK}, &struct{}{})
}

// GetDmsgPingServerPK calls GetDmsgPingServerPK.
func (rc *rpcClient) GetDmsgPingServerPK(pk cipher.PubKey) (cipher.PubKey, error) {
	var serverPK cipher.PubKey
	err := rc.Call("GetDmsgPingServerPK", &pk, &serverPK)
	return serverPK, err
}

// GetRemoteDmsgServers calls GetRemoteDmsgServers.
func (rc *rpcClient) GetRemoteDmsgServers(pk cipher.PubKey) ([]cipher.PubKey, error) {
	var servers []cipher.PubKey
	err := rc.Call("GetRemoteDmsgServers", &pk, &servers)
	return servers, err
}

// DialDmsgRPC is not supported over standard RPC - use gRPC StreamRemoteSystemStats instead.
func (rc *rpcClient) DialDmsgRPC(_ cipher.PubKey) (net.Conn, error) {
	return nil, fmt.Errorf("DialDmsgRPC not available over standard RPC, use gRPC StreamRemoteSystemStats")
}

// GetPreferredDmsgServer calls GetPreferredDmsgServer.
func (rc *rpcClient) GetPreferredDmsgServer(remotePK cipher.PubKey) (cipher.PubKey, error) {
	var serverPK cipher.PubKey
	err := rc.Call("GetPreferredDmsgServer", &remotePK, &serverPK)
	return serverPK, err
}

// BandwidthTest calls BandwidthTest.
func (rc *rpcClient) BandwidthTest(conf BandwidthTestConfig) (BandwidthResult, error) {
	var result BandwidthResult
	err := rc.Call("BandwidthTest", &conf, &result)
	return result, err
}

// DmsgBandwidthTest calls DmsgBandwidthTest.
func (rc *rpcClient) DmsgBandwidthTest(conf BandwidthTestConfig) (BandwidthResult, error) {
	var result BandwidthResult
	err := rc.Call("DmsgBandwidthTest", &conf, &result)
	return result, err
}

// TestVisor calls TestVisor.
func (rc *rpcClient) TestVisor(conf PingConfig) ([]TestResult, error) {
	var results []TestResult
	err := rc.Call("TestVisor", &conf, &results)
	return results, err
}

// TestProxy calls TestProxy.
func (rc *rpcClient) TestProxy(conf ProxyTestConfig) ([]ProxyTestResult, error) {
	var results []ProxyTestResult
	err := rc.Call("TestProxy", &conf, &results)
	return results, err
}

// ReinitiateModule calls ReinitiateModule.
func (rc *rpcClient) ReinitiateModule(module string) error {
	return rc.Call("ReinitiateModule", &module, &struct{}{})
}

// StartUIServer calls StartUIServer.
func (rc *rpcClient) StartUIServer(addr string) error {
	return rc.Call("StartUIServer", &addr, &struct{}{})
}

// StopUIServer calls StopUIServer.
func (rc *rpcClient) StopUIServer() error {
	return rc.Call("StopUIServer", &struct{}{}, &struct{}{})
}

// UIServerStatus calls UIServerStatus.
func (rc *rpcClient) UIServerStatus() (*UIServerStatus, error) {
	var status UIServerStatus
	err := rc.Call("UIServerStatus", &struct{}{}, &status)
	return &status, err
}

// EmbeddedProxies fetches the runtime state of the in-process
// resolving proxies (dmsgweb / skynetweb).
func (rc *rpcClient) EmbeddedProxies() (*EmbeddedProxiesStatus, error) {
	var status EmbeddedProxiesStatus
	err := rc.Call("EmbeddedProxies", &struct{}{}, &status)
	return &status, err
}

// SetEmbeddedProxyEnabled toggles a resolver on/off at runtime.
func (rc *rpcClient) SetEmbeddedProxyEnabled(kind string, enable bool) error {
	return rc.Call("SetEmbeddedProxyEnabled",
		&SetEmbeddedProxyEnabledRequest{Kind: kind, Enable: enable},
		&struct{}{})
}

// SetEmbeddedProxyUpstream changes the upstream SOCKS5 address at runtime.
func (rc *rpcClient) SetEmbeddedProxyUpstream(kind, addr string) error {
	return rc.Call("SetEmbeddedProxyUpstream",
		&SetEmbeddedProxyUpstreamRequest{Kind: kind, Addr: addr},
		&struct{}{})
}

// DmsgHTTP performs an HTTP request over dmsg using the visor's dmsg client.
func (rc *rpcClient) DmsgHTTP(req DmsgHTTPRequest) (*DmsgHTTPResponse, error) {
	var resp DmsgHTTPResponse
	err := rc.Call("DmsgHTTP", &req, &resp)
	return &resp, err
}

// SkynetHTTP performs an HTTP request over skynet using the visor's router.
func (rc *rpcClient) SkynetHTTP(req SkynetHTTPRequest) (*SkynetHTTPResponse, error) {
	var resp SkynetHTTPResponse
	err := rc.Call("SkynetHTTP", &req, &resp)
	return &resp, err
}

// DmsgProbe checks dmsg reachability of a remote PK on a given port.
func (rc *rpcClient) DmsgProbe(pk cipher.PubKey, port uint16) (bool, error) {
	var reachable bool
	err := rc.Call("DmsgProbe", &DmsgProbeRequest{PK: pk, Port: port}, &reachable)
	return reachable, err
}

// DmsgConnectAll triggers a one-shot connect-to-all-dmsg-servers action.
func (rc *rpcClient) DmsgConnectAll() (*DmsgConnectAllResult, error) {
	var resp DmsgConnectAllResult
	if err := rc.Call("DmsgConnectAll", &struct{}{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SetDmsgSessionsCount persists sessions_count and triggers a connect-all.
func (rc *rpcClient) SetDmsgSessionsCount(count int) (*DmsgConnectAllResult, error) {
	var resp DmsgConnectAllResult
	if err := rc.Call("SetDmsgSessionsCount", &count, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DmsgSessions returns the current dmsg session state of every dmsg client
// running inside the visor (main / route_setup / transport_setup).
func (rc *rpcClient) DmsgSessions() (*DmsgClientSessions, error) {
	var resp DmsgClientSessions
	if err := rc.Call("DmsgSessions", &struct{}{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RegisterCXOFeed implements API.
func (rc *rpcClient) RegisterCXOFeed(name string, dmsgPort uint16, description string) error {
	return rc.Call("RegisterCXOFeed", &RegisterCXOFeedRequest{
		Name:        name,
		DmsgPort:    dmsgPort,
		Description: description,
	}, &struct{}{})
}

// UnregisterCXOFeed implements API.
func (rc *rpcClient) UnregisterCXOFeed(name string) error {
	return rc.Call("UnregisterCXOFeed", &name, &struct{}{})
}

// ListCXOFeeds implements API.
func (rc *rpcClient) ListCXOFeeds() []logserver.CXOFeedEntry {
	var resp []logserver.CXOFeedEntry
	if err := rc.Call("ListCXOFeeds", &struct{}{}, &resp); err != nil {
		return nil
	}
	return resp
}

// PairAdd implements API.
func (rc *rpcClient) PairAdd(peerPK cipher.PubKey) error {
	return rc.Call("PairAdd", &PairAddRequest{PeerPK: peerPK}, &struct{}{})
}

// PairList implements API.
func (rc *rpcClient) PairList() ([]PairInfo, error) {
	var resp []PairInfo
	err := rc.Call("PairList", &struct{}{}, &resp)
	return resp, err
}

// PairRemove implements API.
func (rc *rpcClient) PairRemove(peerPK cipher.PubKey) error {
	return rc.Call("PairRemove", &peerPK, &struct{}{})
}

// PairMarkActive implements API.
func (rc *rpcClient) PairMarkActive(peerPK cipher.PubKey) error {
	return rc.Call("PairMarkActive", &peerPK, &struct{}{})
}

// PairSend implements API.
func (rc *rpcClient) PairSend(peerPK cipher.PubKey, text string) error {
	return rc.Call("PairSend", &PairSendRequest{PeerPK: peerPK, Text: text}, &struct{}{})
}

// PairPoll implements API.
func (rc *rpcClient) PairPoll(since time.Time) ([]PairMessage, error) {
	var resp []PairMessage
	err := rc.Call("PairPoll", &PairPollRequest{Since: since}, &resp)
	return resp, err
}

// TPSStatus returns the status of the embedded TPS.
func (rc *rpcClient) TPSStatus() (*TPSStatus, error) {
	var status TPSStatus
	err := rc.Call("TPSStatus", &struct{}{}, &status)
	return &status, err
}

// TPSAddTransport adds a transport on a target visor using the embedded TPS.
func (rc *rpcClient) TPSAddTransport(targetPK, remotePK cipher.PubKey, tpType string) (*TPSTransportResponse, error) {
	var resp TPSTransportResponse
	err := rc.Call("TPSAddTransport", &TPSAddTransportIn{
		TargetPK: targetPK,
		RemotePK: remotePK,
		TpType:   tpType,
	}, &resp)
	return &resp, err
}

// TPSRemoveTransport removes a transport on a target visor using the embedded TPS.
func (rc *rpcClient) TPSRemoveTransport(targetPK cipher.PubKey, tpID uuid.UUID) error {
	return rc.Call("TPSRemoveTransport", &TPSRemoveTransportIn{
		TargetPK: targetPK,
		TpID:     tpID,
	}, &struct{}{})
}

// TPSGetTransports gets transports from a target visor using the embedded TPS.
func (rc *rpcClient) TPSGetTransports(targetPK cipher.PubKey) ([]TPSTransportResponse, error) {
	var resp []TPSTransportResponse
	err := rc.Call("TPSGetTransports", &targetPK, &resp)
	return resp, err
}

// GetTransportSetupNodes returns the whitelisted transport setup node public keys.
func (rc *rpcClient) GetTransportSetupNodes() ([]cipher.PubKey, error) {
	var resp []cipher.PubKey
	err := rc.Call("GetTransportSetupNodes", &struct{}{}, &resp)
	return resp, err
}

// GetTransportSetupNodesSorted returns TPS nodes sorted by health (healthy first).
func (rc *rpcClient) GetTransportSetupNodesSorted() ([]cipher.PubKey, error) {
	var resp []cipher.PubKey
	err := rc.Call("GetTransportSetupNodesSorted", &struct{}{}, &resp)
	return resp, err
}

// GetRouteSetupNodesSorted returns RSN nodes sorted by health (healthy first).
func (rc *rpcClient) GetRouteSetupNodesSorted() ([]cipher.PubKey, error) {
	var resp []cipher.PubKey
	err := rc.Call("GetRouteSetupNodesSorted", &struct{}{}, &resp)
	return resp, err
}

// GetTPSHealth returns health status for all configured TPS nodes.
func (rc *rpcClient) GetTPSHealth() ([]NodeHealth, error) {
	var resp []NodeHealth
	err := rc.Call("GetTPSHealth", &struct{}{}, &resp)
	return resp, err
}

// GetRSNHealth returns health status for all configured RSN nodes.
func (rc *rpcClient) GetRSNHealth() ([]NodeHealth, error) {
	var resp []NodeHealth
	err := rc.Call("GetRSNHealth", &struct{}{}, &resp)
	return resp, err
}

// RouteSetupStats returns a snapshot of the embedded Route Setup Node's stats.
func (rc *rpcClient) RouteSetupStats() (*setupmetrics.StatsSnapshot, error) {
	var resp setupmetrics.StatsSnapshot
	if err := rc.Call("RouteSetupStats", &struct{}{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ResetRouteSetupStats clears all counters on the embedded RSN stats collector.
func (rc *rpcClient) ResetRouteSetupStats() error {
	return rc.Call("ResetRouteSetupStats", &struct{}{}, &struct{}{})
}

// TPSExternalHealthCheck dials an external TPS over dmsg and performs a health check.
func (rc *rpcClient) TPSExternalHealthCheck(tpsPK cipher.PubKey) error {
	return rc.Call("TPSExternalHealthCheck", &tpsPK, &struct{}{})
}

// TPSExternalAddTransport requests transport setup via an external TPS.
func (rc *rpcClient) TPSExternalAddTransport(tpsPK, targetPK, remotePK cipher.PubKey, tpType string) (*TPSTransportResponse, error) {
	var resp TPSTransportResponse
	err := rc.Call("TPSExternalAddTransport", &TPSExternalAddTransportIn{
		TPSPK:    tpsPK,
		TargetPK: targetPK,
		RemotePK: remotePK,
		TpType:   tpType,
	}, &resp)
	return &resp, err
}

// TPSExternalGetTransports gets transports from a target visor via an external TPS.
func (rc *rpcClient) TPSExternalGetTransports(tpsPK, targetPK cipher.PubKey) ([]TPSTransportResponse, error) {
	var resp []TPSTransportResponse
	err := rc.Call("TPSExternalGetTransports", &TPSExternalGetTransportsIn{
		TPSPK:    tpsPK,
		TargetPK: targetPK,
	}, &resp)
	return resp, err
}

// DHTStatus returns the DHT node's status.
func (rc *rpcClient) DHTStatus() (*DHTStatus, error) {
	var resp DHTStatus
	if err := rc.Call("DHTStatus", &struct{}{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DHTGet retrieves a value from the DHT.
func (rc *rpcClient) DHTGet(pk string, salt string) ([]byte, error) {
	var resp []byte
	err := rc.Call("DHTGet", &DHTGetIn{PK: pk, Salt: salt}, &resp)
	return resp, err
}

// DHTPut publishes a value to the DHT.
func (rc *rpcClient) DHTPut(value []byte, seq uint64, salt string) error {
	return rc.Call("DHTPut", &DHTPutIn{Value: value, Seq: seq, Salt: salt}, &struct{}{})
}

// DHTSetFullNode enables or disables full node mode.
func (rc *rpcClient) DHTSetFullNode(full bool) error {
	return rc.Call("DHTSetFullNode", &full, &struct{}{})
}

// DmsgPorterStats returns ephemeral port reservation counts.
func (rc *rpcClient) DmsgPorterStats() (*DmsgPorterStatus, error) {
	var resp DmsgPorterStatus
	if err := rc.Call("DmsgPorterStats", &struct{}{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DmsgPorterReset frees all ephemeral port reservations.
func (rc *rpcClient) DmsgPorterReset() (*DmsgPorterStatus, error) {
	var resp DmsgPorterStatus
	if err := rc.Call("DmsgPorterReset", &struct{}{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DmsgPorterDiag returns detailed ephemeral port diagnostics for the RSN porter.
func (rc *rpcClient) DmsgPorterDiag() (*netutil.EphemeralDiagResult, error) {
	var resp netutil.EphemeralDiagResult
	if err := rc.Call("DmsgPorterDiag", &struct{}{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AddHypervisor connects to a remote hypervisor at runtime.
func (rc *rpcClient) AddHypervisor(pk cipher.PubKey) error {
	return rc.Call("AddHypervisor", &pk, &struct{}{})
}

// DmsgSetMinSessions updates the minimum DMSG session count.
func (rc *rpcClient) DmsgSetMinSessions(n int) error {
	return rc.Call("DmsgSetMinSessions", &n, &struct{}{})
}

// DmsgReconnect forces DMSG session reconnection.
func (rc *rpcClient) DmsgReconnect() (int, error) {
	var resp int
	err := rc.Call("DmsgReconnect", &struct{}{}, &resp)
	return resp, err
}

// DHTGetAll returns all DHT items matching a salt as JSON.
func (rc *rpcClient) DHTGetAll(salt string) (string, error) {
	var resp string
	if err := rc.Call("DHTGetAll", &salt, &resp); err != nil {
		return "", err
	}
	return resp, nil
}

// DHTListWithTargets returns all DHT items matching a salt as JSON,
// each annotated with its storage target key.
func (rc *rpcClient) DHTListWithTargets(salt string) (string, error) {
	var resp string
	if err := rc.Call("DHTListWithTargets", &salt, &resp); err != nil {
		return "", err
	}
	return resp, nil
}

// DHTSync syncs items from a DHT full node.
func (rc *rpcClient) DHTSync(remotePK string, salt string) (int, error) {
	req := DHTSyncRequest{RemotePK: remotePK, Salt: salt}
	var resp int
	if err := rc.Call("DHTSync", &req, &resp); err != nil {
		return 0, err
	}
	return resp, nil
}

// DHTPeers returns the routing-table contents.
func (rc *rpcClient) DHTPeers() ([]DHTPeerInfo, error) {
	var resp []DHTPeerInfo
	if err := rc.Call("DHTPeers", &struct{}{}, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// DHTReconcile runs a one-shot reconcile against a remote full node.
func (rc *rpcClient) DHTReconcile(remotePK string, salt string) (int, int, error) {
	req := DHTSyncRequest{RemotePK: remotePK, Salt: salt}
	var resp DHTReconcileResult
	if err := rc.Call("DHTReconcile", &req, &resp); err != nil {
		return 0, 0, err
	}
	return resp.Pulled, resp.Pushed, nil
}

// TransportRPCCall proxies an RPC call to a remote visor over a transport.
// args is optional JSON-encoded RPC arguments.
func (rc *rpcClient) TransportRPCCall(remotePK cipher.PubKey, method string, args json.RawMessage) (json.RawMessage, error) {
	req := TransportRPCCallRequest{RemotePK: remotePK, Method: method, Args: args}
	var resp json.RawMessage
	if err := rc.Call("TransportRPCCall", &req, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// HVListVisors returns summaries of all visors connected to this hypervisor.
func (rc *rpcClient) HVListVisors() ([]HVVisorEntry, error) {
	var out []HVVisorEntry
	if err := rc.Call("HVListVisors", &struct{}{}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// HVVisorSummary returns a detailed summary of a specific remote visor.
func (rc *rpcClient) HVVisorSummary(pk cipher.PubKey) (*Summary, error) {
	var out Summary
	if err := rc.Call("HVVisorSummary", &pk, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HVStartApp starts an app on a remote visor.
func (rc *rpcClient) HVStartApp(pk cipher.PubKey, appName string) error {
	return rc.Call("HVStartApp", &HVAppArgs{PK: pk, AppName: appName}, &struct{}{})
}

// HVStopApp stops an app on a remote visor.
func (rc *rpcClient) HVStopApp(pk cipher.PubKey, appName string) error {
	return rc.Call("HVStopApp", &HVAppArgs{PK: pk, AppName: appName}, &struct{}{})
}

// HVSetMinHops sets min_hops on a remote visor.
func (rc *rpcClient) HVSetMinHops(pk cipher.PubKey, hops uint16) error {
	return rc.Call("HVSetMinHops", &HVMinHopsArgs{PK: pk, Hops: hops}, &struct{}{})
}

// HVSetRewardAddress sets the reward address on a remote visor.
func (rc *rpcClient) HVSetRewardAddress(pk cipher.PubKey, addr string) (string, error) {
	var out string
	if err := rc.Call("HVSetRewardAddress", &HVRewardArgs{PK: pk, Addr: addr}, &out); err != nil {
		return "", err
	}
	return out, nil
}

// HVRemoveTransport deletes a transport on a remote visor.
func (rc *rpcClient) HVRemoveTransport(pk cipher.PubKey, tid uuid.UUID) error {
	return rc.Call("HVRemoveTransport", &HVTransportArgs{PK: pk, TID: tid}, &struct{}{})
}

// HVRemoveRoutingRule deletes a routing rule on a remote visor.
func (rc *rpcClient) HVRemoveRoutingRule(pk cipher.PubKey, key routing.RouteID) error {
	return rc.Call("HVRemoveRoutingRule", &HVRoutingRuleArgs{PK: pk, Key: key}, &struct{}{})
}

// HVAddTransport creates a transport on a remote visor.
func (rc *rpcClient) HVAddTransport(pk, remote cipher.PubKey, tpType, label string, timeout time.Duration) (*TransportSummary, error) {
	var out TransportSummary
	if err := rc.Call("HVAddTransport", &HVAddTransportArgs{
		PK:      pk,
		Remote:  remote,
		TpType:  tpType,
		Label:   label,
		Timeout: timeout,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HVSetPublicAutoconnect toggles public_autoconnect on a remote visor.
func (rc *rpcClient) HVSetPublicAutoconnect(pk cipher.PubKey, enable bool) error {
	return rc.Call("HVSetPublicAutoconnect", &HVAutoconnectArgs{PK: pk, Enable: enable}, &struct{}{})
}

// HVSetMuxRoutes sets mux_routes on a remote visor.
func (rc *rpcClient) HVSetMuxRoutes(pk cipher.PubKey, n int) error {
	return rc.Call("HVSetMuxRoutes", &HVMuxArgs{PK: pk, N: n}, &struct{}{})
}

// HVSetCalculateRoutes toggles calculate_routes on a remote visor.
func (rc *rpcClient) HVSetCalculateRoutes(pk cipher.PubKey, enable bool) error {
	return rc.Call("HVSetCalculateRoutes", &HVCalcRoutesArgs{PK: pk, Enable: enable}, &struct{}{})
}

// HVReload reloads a remote visor.
func (rc *rpcClient) HVReload(pk cipher.PubKey) error {
	return rc.Call("HVReload", &pk, &struct{}{})
}

// HVShutdown shuts down a remote visor.
func (rc *rpcClient) HVShutdown(pk cipher.PubKey) error {
	return rc.Call("HVShutdown", &pk, &struct{}{})
}

// HVServiceHealth returns deployment service health for a remote visor.
func (rc *rpcClient) HVServiceHealth(pk cipher.PubKey) ([]ServiceHealthEntry, error) {
	var out []ServiceHealthEntry
	if err := rc.Call("HVServiceHealth", &pk, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// HVDmsgSessions reads the per-client dmsg sessions snapshot from a remote visor.
func (rc *rpcClient) HVDmsgSessions(pk cipher.PubKey) (*DmsgClientSessions, error) {
	var out DmsgClientSessions
	if err := rc.Call("HVDmsgSessions", &pk, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HVDmsgConnectAll triggers connect-all on a remote visor.
func (rc *rpcClient) HVDmsgConnectAll(pk cipher.PubKey) (*DmsgConnectAllResult, error) {
	var out DmsgConnectAllResult
	if err := rc.Call("HVDmsgConnectAll", &pk, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HVSetDmsgSessionsCount persists sessions_count and triggers connect-all on a remote visor.
func (rc *rpcClient) HVSetDmsgSessionsCount(pk cipher.PubKey, count int) (*DmsgConnectAllResult, error) {
	var out DmsgConnectAllResult
	if err := rc.Call("HVSetDmsgSessionsCount", &HVDmsgSessionsArgs{PK: pk, Count: count}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HVLogsSince fetches recent app logs from a remote visor.
func (rc *rpcClient) HVLogsSince(pk cipher.PubKey, since time.Time, appName string) ([]string, error) {
	var out []string
	if err := rc.Call("HVLogsSince", &HVLogsArgs{PK: pk, Since: since, AppName: appName}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// HVSetAutoStart toggles autostart for an app on a remote visor.
func (rc *rpcClient) HVSetAutoStart(pk cipher.PubKey, appName string, autostart bool) error {
	return rc.Call("HVSetAutoStart", &HVAutostartArgs{PK: pk, AppName: appName, Autostart: autostart}, &struct{}{})
}

// HVEmbeddedProxies returns embedded resolving proxy status from a remote visor.
func (rc *rpcClient) HVEmbeddedProxies(pk cipher.PubKey) (*EmbeddedProxiesStatus, error) {
	var out EmbeddedProxiesStatus
	if err := rc.Call("HVEmbeddedProxies", &pk, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// HVSetEmbeddedProxyEnabled flips a resolver on or off on a remote visor.
func (rc *rpcClient) HVSetEmbeddedProxyEnabled(pk cipher.PubKey, kind string, enable bool) error {
	return rc.Call("HVSetEmbeddedProxyEnabled", &HVProxyArgs{PK: pk, Kind: kind, Enable: enable}, &struct{}{})
}

// HVSetEmbeddedProxyUpstream sets a resolver's SOCKS5 fallthrough on a remote visor.
func (rc *rpcClient) HVSetEmbeddedProxyUpstream(pk cipher.PubKey, kind, addr string) error {
	return rc.Call("HVSetEmbeddedProxyUpstream", &HVProxyArgs{PK: pk, Kind: kind, Addr: addr}, &struct{}{})
}

// HVListTCPPorts returns skynet TCP ports on a remote visor.
func (rc *rpcClient) HVListTCPPorts(pk cipher.PubKey) ([]int, error) {
	var out []int
	if err := rc.Call("HVListTCPPorts", &pk, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// HVRegisterTCPPort registers a skynet TCP port on a remote visor.
func (rc *rpcClient) HVRegisterTCPPort(pk cipher.PubKey, port int) error {
	return rc.Call("HVRegisterTCPPort", &HVTCPPortArgs{PK: pk, Port: port}, &struct{}{})
}

// HVDeregisterTCPPort deregisters a skynet TCP port on a remote visor.
func (rc *rpcClient) HVDeregisterTCPPort(pk cipher.PubKey, port int) error {
	return rc.Call("HVDeregisterTCPPort", &HVTCPPortArgs{PK: pk, Port: port}, &struct{}{})
}

// HVListForwardedPorts returns forwarded ports on a remote visor.
func (rc *rpcClient) HVListForwardedPorts(pk cipher.PubKey) ([]ForwardedPort, error) {
	var out []ForwardedPort
	if err := rc.Call("HVListForwardedPorts", &pk, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// HVRegisterForwardedPort registers a forwarded port on a remote visor.
func (rc *rpcClient) HVRegisterForwardedPort(pk cipher.PubKey, p ForwardedPort) error {
	return rc.Call("HVRegisterForwardedPort", &HVForwardedPortArgs{PK: pk, Port: p}, &struct{}{})
}

// HVUpdateForwardedPort updates a forwarded port on a remote visor.
func (rc *rpcClient) HVUpdateForwardedPort(pk cipher.PubKey, p ForwardedPort) error {
	return rc.Call("HVUpdateForwardedPort", &HVForwardedPortArgs{PK: pk, Port: p}, &struct{}{})
}

// CheckAREntry checks if a PK is registered in the address resolver.
func (rc *rpcClient) CheckAREntry(pk string) ([]string, error) {
	var resp []string
	err := rc.Call("CheckAREntry", &pk, &resp)
	return resp, err
}

// ARSelfInfo returns the visor's own AR registration.
func (rc *rpcClient) ARSelfInfo() (*ARSelfRegistration, error) {
	var resp ARSelfRegistration
	if err := rc.Call("ARSelfInfo", &struct{}{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
