// Package visor pkg/visor/rpc_client.go
package visor

import (
	"context"
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
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
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

// Uptime calls Uptime
func (rc *rpcClient) Uptime() (float64, error) {
	var out float64
	err := rc.Call("Uptime", &struct{}{}, &out)
	return out, err
}

// SetRewardAddress implements API.
func (rc *rpcClient) SetRewardAddress(r string) (rConfig string, err error) {
	err = rc.Call("SetRewardAddress", &r, &rConfig)
	if err != nil {
		return "", err
	}
	return rConfig, err
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

// SetAppPassword calls SetAppPassword.
func (rc *rpcClient) SetAppPassword(appName, password string) error {
	return rc.Call("SetAppPassword", &SetAppPasswordIn{
		AppName:  appName,
		Password: password,
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

// AddMuxRoute adds a mux route to an app's active connection.
func (rc *rpcClient) AddMuxRoute(appName string, tpID uuid.UUID) error {
	return rc.Call("AddMuxRoute", &MuxRouteInput{AppName: appName, TransportID: tpID}, &struct{}{})
}

// RemoveMuxRoute removes a mux route from an app's active connection.
func (rc *rpcClient) RemoveMuxRoute(appName string, tpID uuid.UUID) error {
	return rc.Call("RemoveMuxRoute", &MuxRouteInput{AppName: appName, TransportID: tpID}, &struct{}{})
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

// SetSyncTPDData sets the sync_tpd_data from visor transport config
func (rc *rpcClient) SetSyncTPDData(enabled bool) error {
	err := rc.Call("SetSyncTPDData", &enabled, &struct{}{})
	return err
}

// GetSyncTPDData gets the sync_tpd_data from visor transport config
func (rc *rpcClient) GetSyncTPDData() (bool, error) {
	var enabled bool
	err := rc.Call("GetSyncTPDData", &struct{}{}, &enabled)
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

// Connect calls Connect.
func (rc *rpcClient) Connect(remotePK cipher.PubKey, remotePort, localPort int) (uuid.UUID, error) {
	var out uuid.UUID
	err := rc.Call("Connect", &ConnectIn{
		RemotePK:   remotePK,
		RemotePort: remotePort,
		LocalPort:  localPort,
	}, &out)
	return out, err
}

// Disconnect calls Disconnect.
func (rc *rpcClient) Disconnect(id uuid.UUID) error {
	err := rc.Call("Disconnect", &id, &struct{}{})
	return err
}

// List calls List.
func (rc *rpcClient) List() (map[uuid.UUID]*appnet.ForwardConn, error) {
	var out map[uuid.UUID]*appnet.ForwardConn
	err := rc.Call("List", &struct{}{}, &out)
	return out, err
}

// ConnectRawTCP calls ConnectRawTCP.
func (rc *rpcClient) ConnectRawTCP(remotePK cipher.PubKey, remotePort, localPort int) (uuid.UUID, error) {
	var out uuid.UUID
	err := rc.Call("ConnectRawTCP", &ConnectIn{
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

// RegisterHTTPPort calls RegisterHTTPPort.
func (rc *rpcClient) RegisterHTTPPort(localPort int) error {
	return rc.Call("RegisterHTTPPort", &localPort, &struct{}{})
}

// DeregisterHTTPPort calls DeregisterHTTPPort.
func (rc *rpcClient) DeregisterHTTPPort(localPort int) error {
	err := rc.Call("DeregisterHTTPPort", &localPort, &struct{}{})
	return err
}

// ListHTTPPorts calls ListHTTPPorts.
func (rc *rpcClient) ListHTTPPorts() ([]int, error) {
	var out []int
	err := rc.Call("ListHTTPPorts", &struct{}{}, &out)
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

// DmsgHTTP performs an HTTP request over dmsg using the visor's dmsg client.
func (rc *rpcClient) DmsgHTTP(req DmsgHTTPRequest) (*DmsgHTTPResponse, error) {
	var resp DmsgHTTPResponse
	err := rc.Call("DmsgHTTP", &req, &resp)
	return &resp, err
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
