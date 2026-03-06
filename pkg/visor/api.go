// Package visor pkg/visor/api.go
package visor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/gocarina/gocsv"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/tpviz"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// API represents visor API.
type API interface {
	//visor
	Overview() (*Overview, error)
	Summary() (*Summary, error)
	Health() (*HealthInfo, error)
	Uptime() (float64, error)
	Reload() error
	Shutdown() error
	RuntimeLogs() (string, error)
	RemoteVisors() ([]string, error)
	GetLogRotationInterval() (visorconfig.Duration, error)
	SetLogRotationInterval(visorconfig.Duration) error
	IsDMSGClientReady() (bool, error)
	DMSGServers() ([]DMSGServerInfo, error)
	Ports() (map[string]PortDetail, error)

	//reward setting
	SetRewardAddress(string) (string, error)
	GetRewardAddress() (string, error)
	DeleteRewardAddress() error

	//app controls
	App(appName string) (*appserver.AppState, error)
	Apps() ([]*appserver.AppState, error)
	StartApp(appName string) error
	StartAppWithMode(appName, launcherMode string) error
	AddApp(appName, binaryName string) error
	RegisterApp(procConf appcommon.ProcConfig) (appcommon.ProcKey, error)
	DeregisterApp(procKey appcommon.ProcKey) error
	StopApp(appName string) error
	KillApp(appName string) error
	SetAppDetailedStatus(appName, state string) error
	SetAppError(appName, stateErr string) error
	RestartApp(appName string) error
	SetAutoStart(appName string, autostart bool) error
	SetAppPassword(appName, password string) error
	SetAppPK(appName string, pk cipher.PubKey) error
	SetAppSecure(appName string, isSecure bool) error
	SetAppAddress(appName string, address string) error
	SetAppKillswitch(appName string, killswitch bool) error
	SetAppNetworkInterface(appName string, netifc string) error
	SetAppDNS(appName string, dnsaddr string) error
	DoCustomSetting(appName string, customSetting map[string]any) error
	LogsSince(timestamp time.Time, appName string) ([]string, error)
	GetAppStats(appName string) (appserver.AppStats, error)
	GetAppError(appName string) (string, error)
	GetAppConnectionsSummary(appName string) ([]appserver.ConnectionSummary, error)

	//vpn controls
	StartVPNClient(pk cipher.PubKey) error
	StartVPNClientWithMode(pk cipher.PubKey, launcherMode string) error
	StopVPNClient(appName string) error
	VPNServers(version, country string) ([]servicedisc.Service, error)

	//skysocks-client controls
	StartSkysocksClient(pk string) error
	StopSkysocksClients() error
	ProxyServers(version, country string) ([]servicedisc.Service, error)
	TestProxy(config ProxyTestConfig) ([]ProxyTestResult, error)

	//transport settings
	SetExistingTPOnly(enabled bool) error
	SetForceLocalRoutes(enabled bool) error

	//transports
	TransportTypes() ([]string, error)
	Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error)
	Transport(tid uuid.UUID) (*TransportSummary, error)
	AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration, label string, noRegister bool, skipLatencyProbe bool) (*TransportSummary, error)
	RemoveTransport(tid uuid.UUID) error
	RemoveAllTransports() error
	SetPublicAutoconnect(pAc bool) error
	StartPublicAutoconnect() error
	StopPublicAutoconnect() error
	PublicAutoconnectStatus() (bool, error)
	GetPersistentTransports() ([]transport.PersistentTransports, error)
	SetPersistentTransports([]transport.PersistentTransports) error
	GetTransportLogs(days int) ([]TransportLogEntry, error)
	//transport discovery
	DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.Entry, error)
	DiscoverTransportByID(id uuid.UUID) (*transport.Entry, error)

	//routing
	RoutingRules() ([]routing.Rule, error)
	RoutingRule(key routing.RouteID) (routing.Rule, error)
	SaveRoutingRule(rule routing.Rule) error
	RemoveRoutingRule(key routing.RouteID) error
	RouteGroups() ([]RouteGroupInfo, error)
	SetMinHops(uint16) error
	SetCalculateRoutes(enabled bool) error
	GetCalculateRoutes() (bool, error)
	SetSyncTPDData(enabled bool) error
	GetSyncTPDData() (bool, error)

	RegisterHTTPPort(localPort int) error
	DeregisterHTTPPort(localPort int) error
	ListHTTPPorts() ([]int, error)
	Connect(remotePK cipher.PubKey, remotePort, localPort int) (uuid.UUID, error)
	ConnectRawTCP(remotePK cipher.PubKey, remotePort, localPort int) (uuid.UUID, error)
	Disconnect(id uuid.UUID) error
	DisconnectRawTCP(id uuid.UUID) error
	List() (map[uuid.UUID]*appnet.ForwardConn, error)
	ListRawTCP() (map[uuid.UUID]*appnet.RawTCPForwardConn, error)
	DialPing(config PingConfig) error
	Ping(config PingConfig) ([]time.Duration, error)
	PingOnce(config PingConfig) (time.Duration, error)
	StopPing(pk cipher.PubKey) error
	StopAllPings() (int, []string, error)
	DialDmsgPing(pk cipher.PubKey) error
	DialDmsgPingViaServer(pk cipher.PubKey, serverPK cipher.PubKey) error
	DialDmsgRPC(pk cipher.PubKey) (net.Conn, error)
	DmsgPing(conf PingConfig) ([]time.Duration, error)
	DmsgPingOnce(conf PingConfig) (time.Duration, error)
	StopDmsgPing(pk cipher.PubKey) error
	GetDmsgPingServerPK(pk cipher.PubKey) (cipher.PubKey, error)
	GetRemoteDmsgServers(pk cipher.PubKey) ([]cipher.PubKey, error)
	GetPreferredDmsgServer(remotePK cipher.PubKey) (cipher.PubKey, error)
	BandwidthTest(conf BandwidthTestConfig) (BandwidthResult, error)
	DmsgBandwidthTest(conf BandwidthTestConfig) (BandwidthResult, error)

	TestVisor(config PingConfig) ([]TestResult, error)

	ReinitiateModule(module string) error

	//uptime-tracker tools
	FetchUptimeTrackerData(pk string) ([]byte, error)

	//service discovery management (network monitor functionality)
	DeregisterService(pks []cipher.PubKey, serviceType string) error

	//ui server controls
	StartUIServer(addr string) error
	StopUIServer() error
	UIServerStatus() (*UIServerStatus, error)

	//dmsg utilities
	DmsgHTTP(req DmsgHTTPRequest) (*DmsgHTTPResponse, error)

	// Embedded Transport Setup Node (TPS) controls
	TPSStatus() (*TPSStatus, error)
	TPSAddTransport(targetPK, remotePK cipher.PubKey, tpType string) (*TPSTransportResponse, error)
	TPSRemoveTransport(targetPK cipher.PubKey, tpID uuid.UUID) error
	TPSGetTransports(targetPK cipher.PubKey) ([]TPSTransportResponse, error)

	// External TPS operations (dial external TPS over dmsg)
	GetTransportSetupNodes() ([]cipher.PubKey, error)
	GetTransportSetupNodesSorted() ([]cipher.PubKey, error)
	GetRouteSetupNodesSorted() ([]cipher.PubKey, error)
	GetTPSHealth() ([]NodeHealth, error)
	GetRSNHealth() ([]NodeHealth, error)
	TPSExternalHealthCheck(tpsPK cipher.PubKey) error
	TPSExternalAddTransport(tpsPK, targetPK, remotePK cipher.PubKey, tpType string) (*TPSTransportResponse, error)
	TPSExternalGetTransports(tpsPK, targetPK cipher.PubKey) ([]TPSTransportResponse, error)

	// Close closes the API connection (for RPC clients)
	Close() error
}

// UIServerStatus contains the status of the UI server.
type UIServerStatus struct {
	Running   bool   `json:"running"`
	LocalAddr string `json:"local_addr,omitempty"`
	DmsgPort  uint16 `json:"dmsg_port,omitempty"`
}

// TPSStatus contains the status of the embedded Transport Setup Node.
type TPSStatus struct {
	Enabled bool          `json:"enabled"`
	PubKey  cipher.PubKey `json:"pub_key,omitempty"`
}

// TPSTransportResponse contains information about a transport managed by TPS.
type TPSTransportResponse struct {
	ID     uuid.UUID     `json:"id"`
	Local  cipher.PubKey `json:"local"`
	Remote cipher.PubKey `json:"remote"`
	Type   string        `json:"type"`
}

// HealthCheckable resource returns its health status as an integer
// that corresponds to HTTP status code returned from the resource
// 200 codes correspond to a healthy resource
type HealthCheckable interface {
	Health(ctx context.Context) (int, error)
}

// Overview provides a range of basic information about a Visor.
type Overview struct {
	PubKey              cipher.PubKey         `json:"local_pk"`
	BuildInfo           *buildinfo.Info       `json:"build_info"`
	AppProtoVersion     string                `json:"app_protocol_version"`
	Apps                []*appserver.AppState `json:"apps"`
	Transports          []*TransportSummary   `json:"transports"`
	RoutesCount         int                   `json:"routes_count"`
	LocalIP             string                `json:"local_ip"`
	PublicIP            string                `json:"public_ip"`
	IsSymmetricNAT      bool                  `json:"is_symmetic_nat"`
	CountryCode         string                `json:"country_code,omitempty"`
	RegionName          string                `json:"region_name,omitempty"`
	CityName            string                `json:"city_name,omitempty"`
	Latitude            float64               `json:"latitude,omitempty"`
	Longitude           float64               `json:"longitude,omitempty"`
	Hypervisors         []cipher.PubKey       `json:"hypervisors"`
	ConnectedHypervisor []cipher.PubKey       `json:"connected_hypervisor"`
}

// Overview implements API.
func (v *Visor) Overview() (*Overview, error) {
	var tSummaries []*TransportSummary
	var publicIP string
	var isSymmetricNAT bool
	if v == nil {
		return &Overview{}, ErrVisorNotAvailable
	}
	if v.tpM == nil {
		return &Overview{}, ErrTrpMangerNotAvailable
	}
	if v.router == nil {
		return &Overview{}, ErrRouterNotReady
	}
	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		tSummaries = append(tSummaries,
			newTransportSummary(v.tpM, tp, true, v.router.SetupIsTrusted(tp.Remote())))
		return true
	})

	if v.isStunReady() {
		switch v.stunClient.NATType {
		case stun.NATNone, stun.NATFull, stun.NATRestricted, stun.NATPortRestricted:
			publicIP = v.stunClient.PublicIP.IP()
			isSymmetricNAT = false
		case stun.NATSymmetric, stun.NATSymmetricUDPFirewall:
			isSymmetricNAT = true
			// Try to get IP from GeoIP service when STUN doesn't provide it
			if ip, err := GetIP(v.conf.GeoIP); err == nil && ip != "" {
				publicIP = ip
			}
		case stun.NATError, stun.NATUnknown, stun.NATBlocked:
			isSymmetricNAT = false
			// STUN failed, try GeoIP service as fallback
			if ip, err := GetIP(v.conf.GeoIP); err == nil && ip != "" {
				publicIP = ip
			} else {
				publicIP = v.stunClient.NATType.String()
			}
		}
	}

	apps := []*appserver.AppState{}
	if v.appL != nil {
		apps = v.appL.AppStates()
	}

	overview := &Overview{
		PubKey:          v.conf.PK,
		BuildInfo:       buildinfo.Get(),
		AppProtoVersion: supportedProtocolVersion,
		Apps:            apps,
		Transports:      tSummaries,
		RoutesCount:     v.router.RoutesCount(),
		PublicIP:        publicIP,
		IsSymmetricNAT:  isSymmetricNAT,
	}

	// Add geolocation data if available
	v.geoDataMu.RLock()
	if v.geoData != nil {
		overview.CountryCode = v.geoData.CountryCode
		overview.RegionName = v.geoData.RegionName
		overview.CityName = v.geoData.CityName
		overview.Latitude = v.geoData.Latitude
		overview.Longitude = v.geoData.Longitude
	}
	v.geoDataMu.RUnlock()

	localIPs, err := netutil.DefaultNetworkInterfaceIPs()
	if err != nil {
		return nil, err
	}

	if len(localIPs) > 0 {
		// should be okay to have the first one, in the case of
		// active network interface, there's usually just a single IP
		overview.LocalIP = localIPs[0].String()
	}

	overview.Hypervisors = v.conf.Hypervisors

	for connectedHV := range v.connectedHypervisors {
		overview.ConnectedHypervisor = append(overview.ConnectedHypervisor, connectedHV)
	}

	return overview, nil
}

// Summary provides detailed info including overview and health of the visor.
type Summary struct {
	Overview             *Overview                        `json:"overview"`
	Health               *HealthInfo                      `json:"health"`
	Uptime               float64                          `json:"uptime"`
	Routes               []routingRuleResp                `json:"routes"`
	IsHypervisor         bool                             `json:"is_hypervisor,omitempty"`
	DmsgStats            *dmsgtracker.DmsgClientSummary   `json:"dmsg_stats"`
	ConnectedDmsgServers []string                         `json:"connected_dmsg_servers"` // Deprecated: use DMSGServers instead
	DMSGServers          []DMSGServerInfo                 `json:"dmsg_servers"`           // Connected DMSG servers with latencies
	Online               bool                             `json:"online"`
	MinHops              uint16                           `json:"min_hops"`
	PersistentTransports []transport.PersistentTransports `json:"persistent_transports"`
	SkybianBuildVersion  string                           `json:"skybian_build_version,omitempty"` // Deprecated
	RewardAddress        string                           `json:"reward_address"`
	BuildTag             string                           `json:"build_tag"`
	ConfigVersion        string                           `json:"config_version"`
	PublicAutoconnect    bool                             `json:"public_autoconnect"`
}

// Summary implements API.
func (v *Visor) Summary() (*Summary, error) {
	overview, err := v.Overview()
	if err != nil {
		return nil, fmt.Errorf("overview")
	}

	health, err := v.Health()
	if err != nil {
		return nil, fmt.Errorf("health")
	}

	uptime, err := v.Uptime()
	if err != nil {
		return nil, fmt.Errorf("uptime")
	}

	routes, err := v.RoutingRules()
	if err != nil {
		return nil, fmt.Errorf("routes")
	}

	extraRoutes := make([]routingRuleResp, 0, len(routes))
	for _, route := range routes {
		extraRoutes = append(extraRoutes, routingRuleResp{
			Key:     route.KeyRouteID(),
			Rule:    hex.EncodeToString(route),
			Summary: route.Summary(),
		})
	}

	pts, err := v.conf.GetPersistentTransports()
	if err != nil {
		return nil, fmt.Errorf("pts")
	}

	dmsgStatValue := &dmsgtracker.DmsgClientSummary{}
	if v.isDTMReady() {
		dmsgTracker, _ := v.dtm.Get(v.conf.PK)
		dmsgStatValue = &dmsgTracker
	}

	// Get all connected DMSG servers (deprecated, for backward compatibility)
	var connectedDmsgServers []string
	if v.dmsgC != nil {
		connectedDmsgServers = v.dmsgC.ConnectedServersPK()
	}

	// Get DMSG servers with latency info
	dmsgServers, err := v.DMSGServers()
	if err != nil {
		v.log.WithError(err).Warn("Failed to get DMSG servers info")
		dmsgServers = nil
	}

	rewardAddress, err := v.GetRewardAddress()
	if err != nil {
		v.log.Warn(err)
	}

	summary := &Summary{
		Overview:             overview,
		Health:               health,
		Uptime:               uptime,
		Routes:               extraRoutes,
		MinHops:              v.conf.Routing.MinHops,
		PersistentTransports: pts,
		BuildTag:             runtime.GOOS + "_" + runtime.GOARCH,
		ConfigVersion:        v.conf.Common.Version,
		RewardAddress:        rewardAddress,
		PublicAutoconnect:    v.conf.Transport.PublicAutoconnect,
		DmsgStats:            dmsgStatValue,
		ConnectedDmsgServers: connectedDmsgServers,
		DMSGServers:          dmsgServers,
	}

	return summary, nil
}

// HealthInfo carries information about visor's services health represented as boolean value (i32 value)
type HealthInfo struct {
	ServicesHealth string `json:"services_health"`
}

// internalHealthInfo contains information of the status of the visor itself.
// It's thread-safe, and could be used in multiple goroutines
type internalHealthInfo int32

// newHealthInfo creates
func newInternalHealthInfo() *internalHealthInfo {
	return new(internalHealthInfo)
}

// init sets the internalHealthInfo status to initial value (2)
func (h *internalHealthInfo) init() {
	atomic.StoreInt32((*int32)(h), 2)
}

// set sets the internalHealthInfo status to true.
func (h *internalHealthInfo) set() {
	atomic.StoreInt32((*int32)(h), 1)
}

// unset sets the internalHealthInfo to false.
func (h *internalHealthInfo) unset() {
	atomic.StoreInt32((*int32)(h), 0)
}

// value gets the internalHealthInfo value
func (h *internalHealthInfo) value() string {
	val := atomic.LoadInt32((*int32)(h))
	switch val {
	case 0:
		return "connecting"
	case 1:
		return "healthy"
	default:
		return "connecting"
	}
}

// Health implements API.
func (v *Visor) Health() (*HealthInfo, error) {
	if v.isServicesHealthy == nil {
		return &HealthInfo{}, nil
	}
	return &HealthInfo{ServicesHealth: v.isServicesHealthy.value()}, nil
}

// Uptime implements API.
func (v *Visor) Uptime() (float64, error) {
	return time.Since(v.startedAt).Seconds(), nil
}

// SetRewardAddress implements API.
func (v *Visor) SetRewardAddress(p string) (string, error) {
	path := v.conf.LocalPath + "/" + visorconfig.RewardFile
	err := os.WriteFile(path, []byte(p), 0600)
	if err != nil {
		return p, fmt.Errorf("failed to write config to file. err=%v", err)
	}
	// generate survey after set/update reward address
	GenerateSurvey(v, v.log, false)
	return p, nil
}

// GetRewardAddress implements API.
func (v *Visor) GetRewardAddress() (string, error) {
	path := v.conf.LocalPath + "/" + visorconfig.RewardFile
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		file, err := os.Create(filepath.Clean(path))
		if err != nil {
			return "", fmt.Errorf("failed to create config file. err=%v", err)
		}
		err = file.Close()
		if err != nil {
			return "", fmt.Errorf("failed to close config file. err=%v", err)
		}
	}
	rConfig, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("failed to read config file. err=%v", err)
	}
	return string(rConfig), nil
}

// DeleteRewardAddress implements API.
func (v *Visor) DeleteRewardAddress() error {

	path := v.conf.LocalPath + "/" + visorconfig.RewardFile
	err := os.Remove(path)
	if err != nil {
		return fmt.Errorf("Error deleting file. err=%v", err)
	}
	return nil
}

// Apps implements API.
func (v *Visor) Apps() ([]*appserver.AppState, error) {
	// check app launcher availability
	if v.appL == nil {
		return nil, ErrAppLauncherNotAvailable
	}
	return v.appL.AppStates(), nil
}

// App implements API.
func (v *Visor) App(appName string) (*appserver.AppState, error) {
	// check app launcher availability
	if v.appL == nil {
		return nil, ErrAppLauncherNotAvailable
	}
	appState, ok := v.appL.AppState(appName)
	if !ok {
		return &appserver.AppState{}, ErrAppProcNotRunning
	}
	return appState, nil
}

// SkybianBuildVersion implements API.
func (v *Visor) SkybianBuildVersion() string {
	return os.Getenv("SKYBIAN_BUILD_VERSION")
}

// StartApp implements API.
func (v *Visor) StartApp(appName string) error {
	return v.StartAppWithMode(appName, "")
}

// StartAppWithMode implements API with launcher mode override.
// launcherMode can be "internal", "external", or "" for default behavior.
func (v *Visor) StartAppWithMode(appName, launcherMode string) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	var envs []string
	var err error
	if appName == visorconfig.VPNClientName {
		// todo: can we use some kind of app start hook that will be used for both autostart
		// and start? Reason: this is also called in init for autostart

		// check transport manager availability
		if v.tpM == nil {
			return ErrTrpMangerNotAvailable
		}
		maker := vpnEnvMaker(v.conf, v.dmsgC, v.dmsgDC, v.tpM.STCPRRemoteAddrs())
		envs, err = maker()
		if err != nil {
			return err
		}

		if v.GetVPNClientAddress() == "" {
			return errors.New("VPN server pub key is missing")
		}
	}
	// check process manager availability
	if v.procM != nil {
		return v.appL.StartAppWithMode(appName, nil, envs, launcherMode)
	}
	return ErrProcNotAvailable
}

// AddApp implement API.
func (v *Visor) AddApp(appName, binaryName string) error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	return v.conf.AddAppConfig(v.appL, appName, binaryName)
}

// RegisterApp implements API.
func (v *Visor) RegisterApp(procConf appcommon.ProcConfig) (appcommon.ProcKey, error) {
	// check process manager and app launcher availability
	if v.appL == nil {
		return appcommon.ProcKey{}, ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		return v.appL.RegisterApp(procConf)
	}
	return appcommon.ProcKey{}, ErrProcNotAvailable
}

// DeregisterApp implements API.
func (v *Visor) DeregisterApp(procKey appcommon.ProcKey) error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		return v.appL.DeregisterApp(procKey)
	}
	return ErrProcNotAvailable
}

// StopApp implements API.
func (v *Visor) StopApp(appName string) error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		_, err := v.appL.StopApp(appName) //nolint:errcheck,gosec
		return err
	}
	return ErrProcNotAvailable
}

// KillApp implements API.
func (v *Visor) KillApp(appName string) error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		return v.appL.KillApp(appName)
	}
	return ErrProcNotAvailable
}

// StartVPNClient implements API.
func (v *Visor) StartVPNClient(pk cipher.PubKey) error {
	return v.StartVPNClientWithMode(pk, "")
}

// StartVPNClientWithMode implements API with launcher mode override.
func (v *Visor) StartVPNClientWithMode(pk cipher.PubKey, launcherMode string) error {
	var envs []string
	var err error
	if v.tpM == nil {
		return ErrTrpMangerNotAvailable
	}
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if len(v.conf.Launcher.Apps) == 0 {
		return errors.New("no vpn app configuration found")
	}

	for index, app := range v.conf.Launcher.Apps {
		if app.Name == visorconfig.VPNClientName {
			// we set the args in memory and pass it in `v.appL.StartAppWithMode`
			// unlike the api method `StartApp` where `nil` is passed in `v.appL.StartApp` as args
			// but the args are set in the config
			v.conf.Launcher.Apps[index].Args = []string{"app", "vpn-client", "--srv", pk.Hex()}
			maker := vpnEnvMaker(v.conf, v.dmsgC, v.dmsgDC, v.tpM.STCPRRemoteAddrs())
			envs, err = maker()
			if err != nil {
				return err
			}

			if v.GetVPNClientAddress() == "" {
				return errors.New("VPN server pub key is missing")
			}

			// check process manager availability
			if v.procM != nil {
				return v.appL.StartAppWithMode(visorconfig.VPNClientName, v.conf.Launcher.Apps[index].Args, envs, launcherMode)
			}
			return ErrProcNotAvailable
		}
	}
	return errors.New("no vpn app configuration found")
}

// StopVPNClient implements API.
func (v *Visor) StopVPNClient(appName string) error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		_, err := v.appL.StopApp(appName) //nolint:errcheck,gosec
		return err
	}
	return ErrProcNotAvailable
}

// FetchUptimeTrackerData implements API
func (v *Visor) FetchUptimeTrackerData(pk string) ([]byte, error) {
	var body []byte
	var pubkey cipher.PubKey

	if pk != "" {
		err := pubkey.Set(pk)
		if err != nil {
			return body, fmt.Errorf("Invalid or missing public key")
		}
	}
	if v.uptimeTracker == nil {
		return body, fmt.Errorf("Uptime tracker module not available")
	}
	return v.uptimeTracker.FetchUptimes(context.TODO(), pk)
}

// StartSkysocksClient implements API.
func (v *Visor) StartSkysocksClient(serverKey string) error {
	var envs []string
	if v.tpM == nil {
		return ErrTrpMangerNotAvailable
	}
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if len(v.conf.Launcher.Apps) == 0 {
		return errors.New("no skysocks-client app configuration found")
	}

	for index, app := range v.conf.Launcher.Apps {
		if app.Name == visorconfig.SkysocksClientName {
			if v.GetSkysocksClientAddress() == "" && serverKey == "" {
				return errors.New("Skysocks server pub key is missing")
			}

			if serverKey != "" {
				var pk cipher.PubKey
				if err := pk.Set(serverKey); err != nil {
					return err
				}
				if err := v.SetAppPK(visorconfig.SkysocksClientName, pk); err != nil {
					return err
				}
				// we set the args in memory and pass it in `v.appL.StartApp`
				// unlike the api method `StartApp` where `nil` is passed in `v.appL.StartApp` as args
				// but the args are set in the config
				v.conf.Launcher.Apps[index].Args = []string{"app", "skysocks-client", "--srv", pk.Hex(), "--addr", visorconfig.SkysocksClientAddr}
			} else {
				var pk cipher.PubKey
				if err := pk.Set(v.GetSkysocksClientAddress()); err != nil {
					return err
				}
				v.conf.Launcher.Apps[index].Args = []string{"app", "skysocks-client", "--srv", pk.Hex(), "--addr", visorconfig.SkysocksClientAddr}
			}

			// check process manager availability
			if v.procM != nil {
				return v.appL.StartApp(visorconfig.SkysocksClientName, v.conf.Launcher.Apps[index].Args, envs)
			}
			return ErrProcNotAvailable
		}
	}
	return errors.New("no skysocks-client app configuration found")
}

// StopSkysocksClients implements API.
func (v *Visor) StopSkysocksClients() error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		for _, app := range v.conf.Launcher.Apps {
			for _, args := range app.Args {
				if args == visorconfig.SkysocksClientName {
					if _, err := v.appL.StopApp(app.Name); err != nil { //nolint:errcheck,gosec
						v.log.WithError(err).Warnf("Failed to stop app %s", app.Name)
					}
				}
			}
		}
		return nil
	}
	return ErrProcNotAvailable
}

// SetAppDetailedStatus implements API.
func (v *Visor) SetAppDetailedStatus(appName, status string) error {
	proc, ok := v.procM.ProcByName(appName)
	if !ok {
		return ErrAppProcNotRunning
	}

	proc.SetDetailedStatus(status)

	return nil
}

// SetAppError implements API.
func (v *Visor) SetAppError(appName, appErr string) error {
	proc, ok := v.procM.ProcByName(appName)
	if !ok {
		return ErrAppProcNotRunning
	}

	v.log.Infof("Setting error %v for app %v", appErr, appName)
	proc.SetError(appErr)

	return nil
}

// RestartApp implements API.
func (v *Visor) RestartApp(appName string) error {
	// check app launcher availability
	if v.appL == nil {
		v.log.Warn("app launcher not ready yet")
		return ErrAppLauncherNotAvailable
	}
	if _, ok := v.procM.ProcByName(appName); ok { //nolint:errcheck,gosec
		v.log.Infof("Updated %v password, restarting it", appName)
		return v.appL.RestartApp(appName, appName)
	}

	return nil
}

// SetAutoStart implements API.
func (v *Visor) SetAutoStart(appName string, autoStart bool) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}

	if _, ok := v.appL.AppState(appName); !ok {
		return ErrAppProcNotRunning
	}

	v.log.Infof("Saving auto start = %v for app %v to config", autoStart, appName)
	return v.conf.UpdateAppAutostart(v.appL, appName, autoStart)
}

// SetAppPassword implements API.
func (v *Visor) SetAppPassword(appName, password string) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	allowedToChangePassword := func(appName string) bool {
		allowedApps := map[string]struct{}{
			visorconfig.SkysocksName:  {},
			visorconfig.VPNClientName: {},
			visorconfig.VPNServerName: {},
		}

		_, ok := allowedApps[appName]
		return ok
	}

	if !allowedToChangePassword(appName) {
		return fmt.Errorf("app %s is not allowed to change password", appName)
	}

	v.log.Infof("Changing %s password to %q", appName, password)

	const (
		passcodeArgName = "--passcode"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, passcodeArgName, password); err != nil {
		return err
	}

	v.log.Infof("Updated %v password", appName)

	return nil
}

// SetAppNetworkInterface implements API.
func (v *Visor) SetAppNetworkInterface(appName, netifc string) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}

	if visorconfig.VPNServerName != appName {
		return fmt.Errorf("app %s is not allowed to set network interface", appName)
	}

	v.log.Infof("Changing %s network interface to %v", appName, netifc)

	const (
		netifcArgName = "--netifc"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, netifcArgName, netifc); err != nil {
		return err
	}

	v.log.Infof("Updated %v network interface", appName)

	return nil
}

// SetAppKillswitch implements API.
func (v *Visor) SetAppKillswitch(appName string, killswitch bool) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}

	if appName != visorconfig.VPNClientName {
		return fmt.Errorf("app %s is not allowed to set killswitch", appName)
	}

	v.log.Infof("Setting %s killswitch to %v", appName, killswitch)

	const (
		killSwitchArg = "--killswitch"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, killSwitchArg, killswitch); err != nil {
		return err
	}

	v.log.Infof("Updated %v killswitch state", appName)

	return nil
}

// SetAppSecure implements API.
func (v *Visor) SetAppSecure(appName string, isSecure bool) error {
	if appName != visorconfig.VPNServerName {
		return fmt.Errorf("app %s is not allowed to change 'secure' parameter", appName)
	}

	v.log.Infof("Setting %s secure to %v", appName, isSecure)

	const (
		secureArgName = "--secure"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, secureArgName, isSecure); err != nil {
		return err
	}
	v.log.Infof("Updated %v secure state", appName)

	return nil
}

// SetAppAddress implements API.
func (v *Visor) SetAppAddress(appName string, address string) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}

	if appName != visorconfig.SkychatName {
		return fmt.Errorf("app %s is not allowed to set addr", appName)
	}

	if len(address) < 5 || (address[:1] != ":" && address[:2] != "*:") {
		return fmt.Errorf("invalid addr value: %s", address)
	}

	forLocalhostOnly := address[:1] == ":"
	prefix := 2
	if forLocalhostOnly {
		prefix = 1
	}

	portNumber, err := strconv.Atoi(address[prefix:])
	if err != nil || portNumber < 1025 || portNumber > 65536 {
		return fmt.Errorf("invalid port number: %s", strconv.Itoa(portNumber))
	}

	v.log.Infof("Setting %s addr to %v", appName, address)

	const (
		addrArg = "--addr"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, addrArg, address); err != nil {
		return err
	}

	v.log.Infof("Updated %v addr state", appName)

	return nil
}

// SetAppPK implements API.
func (v *Visor) SetAppPK(appName string, pk cipher.PubKey) error {
	allowedToChangePK := func(appName string) bool {
		allowedApps := map[string]struct{}{
			visorconfig.SkysocksClientName: {},
			visorconfig.VPNClientName:      {},
		}

		_, ok := allowedApps[appName]
		return ok
	}

	if !allowedToChangePK(appName) {
		return fmt.Errorf("app %s is not allowed to change PK", appName)
	}

	v.log.Infof("Changing %s PK to %q", appName, pk)

	const (
		pkArgName = "--srv"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, pkArgName, pk.String()); err != nil {
		return err
	}

	v.log.Infof("Updated %v PK", appName)

	return nil
}

// SetAppDNS implements API.
func (v *Visor) SetAppDNS(appName string, dnsAddr string) error {
	allowedToChangePK := func(appName string) bool {
		allowedApps := map[string]struct{}{
			skyenv.VPNClientName: {},
		}

		_, ok := allowedApps[appName]
		return ok
	}

	if !allowedToChangePK(appName) {
		return fmt.Errorf("app %s is not allowed to change DNS Address", appName)
	}

	v.log.Infof("Changing %s DNS Address to %q", appName, dnsAddr)

	const (
		pkArgName = "--dns"
	)

	if err := v.conf.UpdateAppArg(v.appL, appName, pkArgName, dnsAddr); err != nil {
		return err
	}

	v.log.Infof("Updated %v DNS Address", appName)

	return nil
}

// DoCustomSetting implents API.
func (v *Visor) DoCustomSetting(appName string, customSetting map[string]any) error {
	fmt.Println(customSetting)
	v.log.Infof("Changing %s Settings to %v", appName, customSetting)
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if err := v.conf.DeleteAppArg(v.appL, appName); err != nil {
		v.log.Warn("An error occurs deleting old arguments.")
		return err
	}

	if value, ok := customSetting["appPort"]; ok && value != 0 {
		if err := v.conf.UpdateAppPort(v.appL, appName, customSetting["appPort"].(uint16)); err != nil {
			return err
		}
		delete(customSetting, "appPort")
	}

	if err := v.conf.UpdateAppArgBatch(v.appL, appName, customSetting); err != nil {
		return err
	}

	v.log.Info("Updated Settings.")

	return nil
}

// LogsSince implements API.
func (v *Visor) LogsSince(timestamp time.Time, appName string) ([]string, error) {
	proc, ok := v.procM.ProcByName(appName)
	if !ok {
		return nil, fmt.Errorf("proc of app name '%s' is not found", appName)
	}

	res, err := proc.Logs().LogsSince(timestamp)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// GetAppStats implements API.
func (v *Visor) GetAppStats(appName string) (appserver.AppStats, error) {
	stats, err := v.procM.Stats(appName)
	if err != nil {
		return appserver.AppStats{}, err
	}

	return stats, nil
}

// GetAppError implements API.
func (v *Visor) GetAppError(appName string) (string, error) {
	appErr, _ := v.procM.ErrorByName(appName)
	return appErr, nil
}

// GetAppConnectionsSummary implements API.
func (v *Visor) GetAppConnectionsSummary(appName string) ([]appserver.ConnectionSummary, error) {
	// check process manager availability
	if v.procM != nil {
		cSummary, err := v.procM.ConnectionsSummary(appName)
		if err != nil {
			return nil, err
		}
		return cSummary, nil
	}
	return nil, ErrProcNotAvailable
}

// VPNServers gets available public VPN server from service discovery URL
func (v *Visor) VPNServers(version, country string) ([]servicedisc.Service, error) {
	log := logging.MustGetLogger("vpnservers")
	vLog := logging.NewMasterLogger()
	vLog.SetLevel(logrus.InfoLevel)

	sdClient := servicedisc.NewClient(log, vLog, servicedisc.Config{
		Type:          servicedisc.ServiceTypeVPN,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		DiscAddr:      v.conf.Launcher.ServiceDisc,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}, &http.Client{Timeout: time.Duration(20) * time.Second}, "")
	vpnServers, err := sdClient.Services(context.Background(), 0, version, country)
	if err != nil {
		v.log.Error("Error getting public vpn servers: ", err)
		return nil, err
	}
	return vpnServers, nil
}

// ProxyServers gets available public VPN server from service discovery URL
func (v *Visor) ProxyServers(version, country string) ([]servicedisc.Service, error) {
	log := logging.MustGetLogger("proxyservers")
	vLog := logging.NewMasterLogger()
	vLog.SetLevel(logrus.InfoLevel)

	sdClient := servicedisc.NewClient(log, vLog, servicedisc.Config{
		Type:          servicedisc.ServiceTypeProxy,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		DiscAddr:      v.conf.Launcher.ServiceDisc,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}, &http.Client{Timeout: time.Duration(20) * time.Second}, "")
	proxyServers, err := sdClient.Services(context.Background(), 0, version, country)
	if err != nil {
		v.log.Error("Error getting public vpn servers: ", err)
		return nil, err
	}
	return proxyServers, nil
}

// PublicVisors gets available public public visors from service discovery URL
func (v *Visor) PublicVisors(version, country string) ([]servicedisc.Service, error) {
	log := logging.MustGetLogger("public_visors")
	vLog := logging.NewMasterLogger()
	vLog.SetLevel(logrus.InfoLevel)

	sdClient := servicedisc.NewClient(log, vLog, servicedisc.Config{
		Type:          servicedisc.ServiceTypeVisor,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		DiscAddr:      v.conf.Launcher.ServiceDisc,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}, &http.Client{Timeout: time.Duration(20) * time.Second}, "")
	publicVisors, err := sdClient.Services(context.Background(), 0, version, country)
	if err != nil {
		v.log.Error("Error getting public vpn servers: ", err)
		return nil, err
	}
	return publicVisors, nil
}

// DeregisterService deregisters the specified public keys from service discovery.
// This requires the visor's public key to be whitelisted as a network monitor in the service discovery.
// serviceType must be one of: "vpn", "visor", "skysocks" (or "proxy")
func (v *Visor) DeregisterService(pks []cipher.PubKey, serviceType string) error {
	if len(pks) == 0 {
		return fmt.Errorf("no public keys provided")
	}

	// Normalize service type
	sType := serviceType
	if sType == "proxy" {
		sType = "skysocks"
	}
	if sType != "vpn" && sType != "visor" && sType != "skysocks" {
		return fmt.Errorf("invalid service type %q: must be vpn, visor, or skysocks (proxy)", serviceType)
	}

	// Sign the visor's public key with its secret key (network monitor authentication)
	nmSign, err := cipher.SignPayload([]byte(v.conf.PK.Hex()), v.conf.SK)
	if err != nil {
		return fmt.Errorf("failed to sign payload: %w", err)
	}

	// Build the URL
	sdURL := v.conf.Launcher.ServiceDisc
	reqURL := fmt.Sprintf("%s/api/services/deregister/%s", sdURL, sType)

	// Convert public keys to hex strings
	pkStrings := make([]string, len(pks))
	for i, pk := range pks {
		pkStrings[i] = pk.Hex()
	}

	// Marshal the public keys to JSON
	jsonData, err := json.Marshal(pkStrings)
	if err != nil {
		return fmt.Errorf("failed to marshal public keys: %w", err)
	}

	// Create the request
	req, err := http.NewRequest(http.MethodDelete, reqURL, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("NM-PK", v.conf.PK.Hex())
	req.Header.Set("NM-Sign", nmSign.Hex())

	// Send the request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Check response
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	v.log.Infof("Successfully deregistered %d key(s) from service type %s", len(pks), sType)
	return nil
}

// RemoteVisors return list of connected remote visors
func (v *Visor) RemoteVisors() ([]string, error) {
	var visors []string
	for _, conn := range v.remoteVisors {
		visors = append(visors, conn.Addr.PK.String())
	}
	return visors, nil
}

// PortDetail type of port details
type PortDetail struct {
	Port string
	Type string
}

// DMSGServerInfo contains information about a connected DMSG server including latency.
type DMSGServerInfo struct {
	PK      cipher.PubKey `json:"pk"`
	Latency time.Duration `json:"latency"` // Round-trip latency via self-ping, 0 if not measured
}

// Ports return list of all ports used by visor services and apps
func (v *Visor) Ports() (map[string]PortDetail, error) {
	ctx := context.Background()
	var ports = make(map[string]PortDetail)

	if v.conf.Hypervisor != nil {
		ports["hypervisor"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.Hypervisor.HTTPAddr, ":")[1]), Type: "TCP"}
	}

	ports["dmsg_pty"] = PortDetail{Port: fmt.Sprint(v.conf.Dmsgpty.DmsgPort), Type: "DMSG"}
	ports["cli_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.CLIAddr, ":")[1]), Type: "TCP"}
	ports["proc_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.Launcher.ServerAddr, ":")[1]), Type: "TCP"}
	ports["stcp_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.STCP.ListeningAddress, ":")[1]), Type: "TCP"}

	if v.arClient != nil {
		sudphPort := v.arClient.Addresses(ctx)
		if sudphPort != "" {
			ports["sudph"] = PortDetail{Port: sudphPort, Type: "UDP"}
		}
	}
	if v.stunClient != nil {
		if v.stunClient.PublicIP != nil {
			ports["public_visor"] = PortDetail{Port: fmt.Sprint(v.stunClient.PublicIP.Port()), Type: "TCP"}
		}
	}
	if v.dmsgC != nil {
		dmsgSessions := v.dmsgC.AllSessions()
		for i, session := range dmsgSessions {
			ports[fmt.Sprintf("dmsg_session_%d", i)] = PortDetail{Port: strings.Split(session.LocalTCPAddr().String(), ":")[1], Type: "TCP"}
		}

		dmsgStreams := v.dmsgC.AllStreams()
		for i, stream := range dmsgStreams {
			ports[fmt.Sprintf("dmsg_stream_%d", i)] = PortDetail{Port: strings.Split(stream.LocalAddr().String(), ":")[1], Type: "DMSG"}
		}
	}
	if v.procM != nil {
		apps, _ := v.Apps() //nolint:errcheck,gosec
		for _, app := range apps {
			port, err := v.procM.GetAppPort(app.Name)
			if err == nil {
				ports[app.Name] = PortDetail{Port: fmt.Sprint(port), Type: "SKYNET"}

				switch app.Name {
				case "skysocks_client":
					ports["skysocks_client_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(skyenv.SkysocksClientAddr, ":")[1]), Type: "TCP"}
				case "skychat":
					ports["skychat_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(skyenv.SkychatAddr, ":")[1]), Type: "TCP"}
				}
			}
		}
	}
	return ports, nil
}

// SetExistingTPOnly implements API.
// Sets whether to only use existing transports for routing (no new transport creation).
func (v *Visor) SetExistingTPOnly(enabled bool) error {
	if v.router == nil {
		return errors.New("router not available")
	}
	v.router.SetExistingTPOnly(enabled)
	v.log.Infof("SetExistingTPOnly: %v", enabled)
	return nil
}

// SetForceLocalRoutes implements API.
// Sets whether to skip the route finder and use local route calculation.
func (v *Visor) SetForceLocalRoutes(enabled bool) error {
	if v.router == nil {
		return errors.New("router not available")
	}
	v.router.SetForceLocalRoutes(enabled)
	v.log.Infof("SetForceLocalRoutes: %v", enabled)
	return nil
}

// TransportTypes implements API.
func (v *Visor) TransportTypes() ([]string, error) {
	var types []string
	if v.tpM == nil {
		return types, ErrTrpMangerNotAvailable
	}
	for _, netType := range v.tpM.Networks() {
		types = append(types, string(netType))
	}
	return types, nil
}

// Transports implements API.
func (v *Visor) Transports(typeFilters []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error) {
	var result []*TransportSummary

	typeIncluded := func(tType types.Type) bool {
		if typeFilters != nil {
			for _, ft := range typeFilters {
				if string(tType) == ft {
					return true
				}
			}
			return false
		}
		return true
	}
	pkIncluded := func(localPK, remotePK cipher.PubKey) bool {
		if pks != nil {
			for _, fpk := range pks {
				if localPK == fpk || remotePK == fpk {
					return true
				}
			}
			return false
		}
		return true
	}
	if v.tpM != nil {
		v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
			if typeIncluded(tp.Type()) && pkIncluded(v.tpM.Local(), tp.Remote()) {
				result = append(result, newTransportSummary(v.tpM, tp, logs, v.router.SetupIsTrusted(tp.Remote())))
			}
			return true
		})
	}

	return result, nil
}

// Transport implements API.
func (v *Visor) Transport(tid uuid.UUID) (*TransportSummary, error) {
	tp := v.tpM.Transport(tid)
	if tp == nil {
		return nil, ErrNotFound
	}

	return newTransportSummary(v.tpM, tp, true, v.router.SetupIsTrusted(tp.Remote())), nil
}

// AddTransport implements API.
func (v *Visor) AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration, label string, noRegister bool, skipLatencyProbe bool) (*TransportSummary, error) {
	if v.tpM == nil {
		return nil, ErrTrpMangerNotAvailable
	}

	ctx := context.Background()

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second*20)
		defer cancel()
	}

	// Determine label - default to skycoin, use user if explicitly requested
	tpLabel := transport.LabelSkycoin
	if label == string(transport.LabelUser) {
		tpLabel = transport.LabelUser
	}

	// noRegister only valid for user-labeled transports
	if noRegister && tpLabel != transport.LabelUser {
		return nil, fmt.Errorf("--no-register flag is only valid for user-labeled transports")
	}

	v.log.Debugf("Saving transport to %v via %v with label %s (skipLatencyProbe=%v)", remote, tpType, tpLabel, skipLatencyProbe)

	opts := transport.SaveTransportOptions{
		NoRegister:       noRegister,
		SkipLatencyProbe: skipLatencyProbe,
	}

	tp, err := v.tpM.SaveTransportWithOptions(ctx, remote, types.Type(tpType), tpLabel, opts)
	if err != nil {
		return nil, err
	}

	v.log.Debugf("Saved transport to %v via %v, label %s", remote, tpType, tp.Entry.Label)

	return newTransportSummary(v.tpM, tp, false, v.router.SetupIsTrusted(tp.Remote())), nil
}

// RemoveTransport implements API.
func (v *Visor) RemoveTransport(tid uuid.UUID) error {
	v.tpM.DeleteTransport(tid)
	return nil
}

// RemoveAllTransports implements API
func (v *Visor) RemoveAllTransports() error {
	v.tpM.DeleteAllTransports()
	return nil
}

// DiscoverTransportsByPK implements API.
func (v *Visor) DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.Entry, error) {
	tpD := v.tpDiscClient()

	entries, err := tpD.GetTransportsByEdge(context.Background(), pk)
	if err != nil {
		return nil, err
	}

	return entries, nil
}

// DiscoverTransportByID implements API.
func (v *Visor) DiscoverTransportByID(id uuid.UUID) (*transport.Entry, error) {
	tpD := v.tpDiscClient()

	entry, err := tpD.GetTransportByID(context.Background(), id)
	if err != nil {
		return nil, err
	}

	return entry, nil
}

// GetTransportLogs implements API.
// Returns transport log entries from the last N days.
func (v *Visor) GetTransportLogs(days int) ([]TransportLogEntry, error) {
	if days <= 0 {
		return nil, nil
	}

	logDir := v.conf.Transport.LogStore.Location
	if logDir == "" {
		return nil, fmt.Errorf("transport log store location not configured")
	}

	var allEntries []TransportLogEntry
	now := time.Now().UTC()

	// Read log files for the specified number of days
	for i := 0; i < days; i++ {
		date := now.AddDate(0, 0, -i)
		filename := filepath.Join(logDir, fmt.Sprintf("%s.csv", date.Format("2006-01-02")))

		entries, err := readTransportLogFile(filename)
		if err != nil {
			// Skip files that don't exist or can't be read
			continue
		}
		allEntries = append(allEntries, entries...)
	}

	return allEntries, nil
}

// csvEntry matches the transport.CsvEntry struct format for gocsv parsing.
type csvEntry struct {
	TpID      uuid.UUID `csv:"tp_id"`
	RecvBytes *uint64   `csv:"recv"`
	SentBytes *uint64   `csv:"sent"`
	TimeStamp int64     `csv:"time_stamp"`
}

// readTransportLogFile reads transport log entries from a CSV file.
func readTransportLogFile(filename string) ([]TransportLogEntry, error) {
	f, err := os.Open(filename) //nolint:gosec // filename is from internal config
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck

	var csvEntries []*csvEntry
	if err := gocsv.UnmarshalFile(f, &csvEntries); err != nil {
		// Handle empty file case
		if errors.Is(err, gocsv.ErrEmptyCSVFile) {
			return nil, nil
		}
		return nil, err
	}

	var entries []TransportLogEntry
	for _, ce := range csvEntries {
		var recv, sent uint64
		if ce.RecvBytes != nil {
			recv = *ce.RecvBytes
		}
		if ce.SentBytes != nil {
			sent = *ce.SentBytes
		}
		entries = append(entries, TransportLogEntry{
			TpID:      ce.TpID,
			RecvBytes: recv,
			SentBytes: sent,
			Timestamp: ce.TimeStamp,
		})
	}

	return entries, nil
}

// RoutingRules implements API.
func (v *Visor) RoutingRules() ([]routing.Rule, error) {
	return v.router.Rules(), nil
}

// RouteHopInfo contains detailed information about a single hop in a route.
type RouteHopInfo struct {
	TpID   string
	From   string
	To     string
	TpType string
}

// PingConfig use as configuration for ping command
type PingConfig struct {
	PK          cipher.PubKey
	Tries       int
	PcktSize    int
	PubVisCount int
	LocalRoute  bool           // Skip route finder and use local route calculation
	TransportID string         // Optional: use specific transport (skips route calculation)
	ForwardHops []RouteHopInfo // Optional: explicit forward route (skips route calculation)
	ReverseHops []RouteHopInfo // Optional: explicit reverse route (skips route calculation)
}

// DialPing implements API.
func (v *Visor) DialPing(conf PingConfig) error {
	v.pingPcktSize = conf.PcktSize
	// waiting for at least one transport to initialize
	<-v.tpM.Ready()

	// Set local route calculation if requested
	if conf.LocalRoute {
		if err := v.SetForceLocalRoutes(true); err != nil {
			v.log.WithError(err).Warn("Failed to enable local route calculation")
		} else {
			defer func() {
				if err := v.SetForceLocalRoutes(false); err != nil {
					v.log.WithError(err).Warn("Failed to disable local route calculation")
				}
			}()
		}
	}

	addr := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: conf.PK,
		Port:   routing.Port(skyenv.SkyPingPort),
	}

	var err error
	var conn net.Conn

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var r = netutil.NewRetrier(v.log, 2*time.Second, netutil.DefaultMaxBackoff, 1, 2)
	err = r.Do(ctx, func() error {
		if len(conf.ForwardHops) > 0 && len(conf.ReverseHops) > 0 {
			// Use explicit route (skips route calculation)
			fwdHops := make([]appnet.RouteHopInfo, len(conf.ForwardHops))
			for i, h := range conf.ForwardHops {
				fwdHops[i] = appnet.RouteHopInfo{TpID: h.TpID, From: h.From, To: h.To, TpType: h.TpType}
			}
			revHops := make([]appnet.RouteHopInfo, len(conf.ReverseHops))
			for i, h := range conf.ReverseHops {
				revHops[i] = appnet.RouteHopInfo{TpID: h.TpID, From: h.From, To: h.To, TpType: h.TpType}
			}
			conn, err = appnet.PingContextWithRoute(ctx, conf.PK, addr, fwdHops, revHops)
		} else if conf.TransportID != "" {
			// Use specific transport (skips route calculation)
			conn, err = appnet.PingContextWithTransport(ctx, conf.PK, addr, conf.TransportID)
		} else {
			conn, err = appnet.PingContext(ctx, conf.PK, addr)
		}
		return err
	})
	if err != nil {
		return err
	}

	// Extract route hops from the connection if available
	var hops []cipher.PubKey
	var hopInfos []router.RouteHopInfo
	if skyConn, ok := conn.(*appnet.SkywireConn); ok {
		hops = skyConn.RouteHops()
		hopInfos = skyConn.RouteHopDetails()
	}

	v.pingConnMx.Lock()
	v.pingConns[conf.PK] = ping{
		conn:     conn,
		hops:     hops,
		hopInfos: hopInfos,
	}
	v.pingConnMx.Unlock()
	return nil
}

// Ping implements API.
// Measures round-trip time by sending ping data and waiting for an echo response.
func (v *Visor) Ping(conf PingConfig) ([]time.Duration, error) {
	v.pingConnMx.Lock()
	defer v.pingConnMx.Unlock()

	pingEntry, ok := v.pingConns[conf.PK]
	if !ok {
		return nil, fmt.Errorf("no ping connection for %s, call DialPing first", conf.PK)
	}

	latencies := []time.Duration{}
	data := make([]byte, conf.PcktSize*1024)

	for i := 1; i <= conf.Tries; i++ {
		conn := pingEntry.conn
		msg := PingMsg{
			Timestamp: time.Now(),
			PingPk:    conf.PK,
			Data:      data,
		}
		ping, err := json.Marshal(msg)
		if err != nil {
			return latencies, err
		}
		pingSizeMsg := PingSizeMsg{
			Size: len(ping),
		}
		size, err := json.Marshal(pingSizeMsg)
		if err != nil {
			return latencies, err
		}

		start := time.Now()

		// Send size message
		if _, err = conn.Write(size); err != nil {
			return latencies, fmt.Errorf("write size: %w", err)
		}

		// Read "ok" ack with timeout
		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		buf := make([]byte, 32*1024)
		if _, err = conn.Read(buf); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return latencies, fmt.Errorf("read ack: %w", err)
		}

		// Send ping data
		if _, err = conn.Write(ping); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return latencies, fmt.Errorf("write ping: %w", err)
		}

		// Read echo response with timeout
		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		if _, err = conn.Read(buf); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return latencies, fmt.Errorf("read echo: %w", err)
		}
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec

		rtt := time.Since(start)
		latencies = append(latencies, rtt)
	}
	return latencies, nil
}

// PingOnce implements API.
// Performs a single ping and returns the round-trip time.
func (v *Visor) PingOnce(conf PingConfig) (time.Duration, error) {
	v.pingConnMx.Lock()
	defer v.pingConnMx.Unlock()

	pingEntry, ok := v.pingConns[conf.PK]
	if !ok {
		return 0, fmt.Errorf("no ping connection for %s, call DialPing first", conf.PK)
	}

	data := make([]byte, conf.PcktSize*1024)
	conn := pingEntry.conn
	msg := PingMsg{
		Timestamp: time.Now(),
		PingPk:    conf.PK,
		Data:      data,
	}
	ping, err := json.Marshal(msg)
	if err != nil {
		return 0, err
	}
	pingSizeMsg := PingSizeMsg{
		Size: len(ping),
	}
	size, err := json.Marshal(pingSizeMsg)
	if err != nil {
		return 0, err
	}

	start := time.Now()

	// Send size message
	if _, err = conn.Write(size); err != nil {
		return 0, fmt.Errorf("write size: %w", err)
	}

	// Read "ok" ack with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	buf := make([]byte, 32*1024)
	if _, err = conn.Read(buf); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return 0, fmt.Errorf("read ack: %w", err)
	}

	// Send ping data
	if _, err = conn.Write(ping); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return 0, fmt.Errorf("write ping: %w", err)
	}

	// Read echo response with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	if _, err = conn.Read(buf); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return 0, fmt.Errorf("read echo: %w", err)
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec

	return time.Since(start), nil
}

// PingOnceWithEcho performs a single ping with optional full echo.
// Returns bytes sent, bytes received, latency, and error.
// If echoFull is true, server echoes full payload (for bandwidth testing).
func (v *Visor) PingOnceWithEcho(conf PingConfig, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error) {
	v.pingConnMx.Lock()
	defer v.pingConnMx.Unlock()

	pingEntry, ok := v.pingConns[conf.PK]
	if !ok {
		return 0, 0, 0, fmt.Errorf("no ping connection for %s, call DialPing first", conf.PK)
	}

	data := make([]byte, conf.PcktSize*1024)
	conn := pingEntry.conn
	msg := PingMsg{
		Timestamp: time.Now(),
		PingPk:    conf.PK,
		Data:      data,
	}
	ping, err := json.Marshal(msg)
	if err != nil {
		return 0, 0, 0, err
	}
	pingSizeMsg := PingSizeMsg{
		Size:     len(ping),
		EchoFull: echoFull,
	}
	size, err := json.Marshal(pingSizeMsg)
	if err != nil {
		return 0, 0, 0, err
	}

	start := time.Now()

	// Send size message
	if _, err = conn.Write(size); err != nil {
		return 0, 0, 0, fmt.Errorf("write size: %w", err)
	}
	bytesSent += uint64(len(size))

	// Read "ok" ack with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	buf := make([]byte, 32*1024)
	n, err := conn.Read(buf)
	if err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return bytesSent, bytesReceived, 0, fmt.Errorf("read ack: %w", err)
	}
	bytesReceived += uint64(n) //nolint:gosec

	// Send ping data
	if _, err = conn.Write(ping); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return bytesSent, bytesReceived, 0, fmt.Errorf("write ping: %w", err)
	}
	bytesSent += uint64(len(ping))

	// Read echo response with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	if echoFull {
		// Read full payload echo
		var received int
		for received < len(ping) {
			n, err = conn.Read(buf)
			if err != nil {
				conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
				return bytesSent, bytesReceived, 0, fmt.Errorf("read echo: %w", err)
			}
			received += n
			bytesReceived += uint64(n) //nolint:gosec
		}
	} else {
		// Read simple "pong" response
		n, err = conn.Read(buf)
		if err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return bytesSent, bytesReceived, 0, fmt.Errorf("read echo: %w", err)
		}
		bytesReceived += uint64(n) //nolint:gosec
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec

	return bytesSent, bytesReceived, time.Since(start), nil
}

// StopPing implements API.
func (v *Visor) StopPing(pk cipher.PubKey) error {
	v.pingConnMx.Lock()
	defer v.pingConnMx.Unlock()

	pingEntry, ok := v.pingConns[pk]
	if !ok || pingEntry.conn == nil {
		// Already stopped or never started
		delete(v.pingConns, pk)
		return nil
	}
	err := pingEntry.conn.Close()
	if err != nil {
		// Still delete the entry even if close fails
		delete(v.pingConns, pk)
		return err
	}
	delete(v.pingConns, pk)
	return nil
}

// StopAllPings stops all active ping connections and cleans up their routes.
// Returns the number of connections stopped, error messages, and any fatal error.
func (v *Visor) StopAllPings() (int, []string, error) {
	v.pingConnMx.Lock()
	defer v.pingConnMx.Unlock()

	var errs []string
	count := 0

	for pk, pingEntry := range v.pingConns {
		if pingEntry.conn != nil {
			if err := pingEntry.conn.Close(); err != nil {
				errs = append(errs, fmt.Sprintf("failed to close ping to %s: %v", pk, err))
			}
		}
		delete(v.pingConns, pk)
		count++
	}

	return count, errs, nil
}

// GetPingRoute returns the route hops for an established ping connection.
// Returns nil if no ping connection exists for the given public key.
func (v *Visor) GetPingRoute(pk cipher.PubKey) []cipher.PubKey {
	v.pingConnMx.Lock()
	defer v.pingConnMx.Unlock()

	if pingEntry, ok := v.pingConns[pk]; ok {
		return pingEntry.hops
	}
	return nil
}

// GetPingRouteDetails returns detailed route information for a ping connection,
// including transport IDs and types for each hop.
func (v *Visor) GetPingRouteDetails(pk cipher.PubKey) []router.RouteHopInfo {
	v.pingConnMx.Lock()
	defer v.pingConnMx.Unlock()

	if pingEntry, ok := v.pingConns[pk]; ok {
		return pingEntry.hopInfos
	}
	return nil
}

// GetLastRouteCalcTime returns the time spent calculating the last local route.
func (v *Visor) GetLastRouteCalcTime() time.Duration {
	if v.router == nil {
		return 0
	}
	return v.router.GetLastRouteCalcTime()
}

// DialDmsgPing implements API. Dials a remote visor over dmsg for ping.
// It prefers to use the lowest-latency DMSG server that both visors share.
func (v *Visor) DialDmsgPing(pk cipher.PubKey) error {
	if v.dmsgC == nil {
		return fmt.Errorf("dmsg client not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-v.dmsgC.Ready():
	}

	// Try to use the preferred (lowest-latency) server
	preferredServer, err := v.GetPreferredDmsgServer(pk)
	if err == nil && !preferredServer.Null() {
		// Use the preferred server
		v.log.WithField("server", preferredServer.String()[:16]+"...").
			WithField("remote", pk.String()[:16]+"...").
			Debug("Dialing DMSG ping via preferred server")
		return v.DialDmsgPingViaServer(pk, preferredServer)
	}

	// Fall back to default dial (dmsg client will pick a server)
	v.log.WithField("remote", pk.String()[:16]+"...").
		Debug("Dialing DMSG ping via default server selection")

	conn, err := v.dmsgC.Dial(ctx, dmsg.Addr{PK: pk, Port: visorconfig.DmsgPingPort})
	if err != nil {
		return fmt.Errorf("failed to dial dmsg ping: %w", err)
	}

	// Extract server PK from the dmsg stream
	var serverPK cipher.PubKey
	if stream, ok := conn.(*dmsg.Stream); ok {
		serverPK = stream.ServerPK()
	}

	v.dmsgPingMx.Lock()
	v.dmsgPingConns[pk] = ping{conn: conn, serverPK: serverPK}
	v.dmsgPingMx.Unlock()

	return nil
}

// DialDmsgPingViaServer implements API. Dials a remote visor over dmsg via a specific server.
func (v *Visor) DialDmsgPingViaServer(pk cipher.PubKey, serverPK cipher.PubKey) error {
	if v.dmsgC == nil {
		return fmt.Errorf("dmsg client not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-v.dmsgC.Ready():
	}

	// First ensure we have a session with the specified server
	_, err := v.dmsgC.EnsureAndObtainSession(ctx, serverPK)
	if err != nil {
		return fmt.Errorf("failed to connect to dmsg server %s: %w", serverPK, err)
	}

	// Get the session and dial through it
	session, ok := v.dmsgC.Session(serverPK)
	if !ok {
		return fmt.Errorf("no session with dmsg server %s", serverPK)
	}

	stream, err := session.DialStream(dmsg.Addr{PK: pk, Port: visorconfig.DmsgPingPort})
	if err != nil {
		return fmt.Errorf("failed to dial dmsg ping via server %s: %w", serverPK, err)
	}

	v.dmsgPingMx.Lock()
	v.dmsgPingConns[pk] = ping{conn: stream, serverPK: serverPK}
	v.dmsgPingMx.Unlock()

	return nil
}

// DialDmsgRPC implements API. Dials a remote visor's gRPC/RPC port over DMSG.
// Returns a net.Conn that can be used to create a gRPC client.
func (v *Visor) DialDmsgRPC(pk cipher.PubKey) (net.Conn, error) {
	if v.dmsgC == nil {
		return nil, fmt.Errorf("dmsg client not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-v.dmsgC.Ready():
	}

	v.log.WithField("remote", pk.String()[:16]+"...").
		Debug("Dialing remote visor RPC over DMSG")

	// Dial to the hypervisor/RPC port
	conn, err := v.dmsgC.Dial(ctx, dmsg.Addr{PK: pk, Port: visorconfig.DmsgHypervisorPort})
	if err != nil {
		return nil, fmt.Errorf("failed to dial dmsg RPC: %w", err)
	}

	return conn, nil
}

// GetDmsgPingServerPK implements API. Returns the DMSG server PK used for a ping connection.
func (v *Visor) GetDmsgPingServerPK(pk cipher.PubKey) (cipher.PubKey, error) {
	v.dmsgPingMx.Lock()
	defer v.dmsgPingMx.Unlock()

	pingEntry, ok := v.dmsgPingConns[pk]
	if !ok {
		return cipher.PubKey{}, fmt.Errorf("no dmsg ping connection for %s", pk)
	}

	return pingEntry.serverPK, nil
}

// GetPreferredDmsgServer returns the lowest-latency DMSG server that both the local
// and remote visor are connected to. Returns empty PK if no common server found.
func (v *Visor) GetPreferredDmsgServer(remotePK cipher.PubKey) (cipher.PubKey, error) {
	// Get servers the remote visor is connected to
	remoteServers, err := v.GetRemoteDmsgServers(remotePK)
	if err != nil {
		return cipher.PubKey{}, err
	}
	if len(remoteServers) == 0 {
		return cipher.PubKey{}, fmt.Errorf("remote visor not connected to any DMSG servers")
	}

	// Get our connected servers with latencies
	ourServers, err := v.DMSGServers()
	if err != nil {
		return cipher.PubKey{}, err
	}
	if len(ourServers) == 0 {
		return cipher.PubKey{}, fmt.Errorf("not connected to any DMSG servers")
	}

	// Build set of remote servers for O(1) lookup
	remoteServerSet := make(map[cipher.PubKey]bool)
	for _, s := range remoteServers {
		remoteServerSet[s] = true
	}

	// Find the lowest-latency server we share with the remote visor
	// ourServers is already sorted by latency (lowest first)
	for _, server := range ourServers {
		if remoteServerSet[server.PK] {
			v.log.WithField("server", server.PK.String()[:16]+"...").
				WithField("latency", server.Latency).
				Debug("Selected preferred DMSG server")
			return server.PK, nil
		}
	}

	return cipher.PubKey{}, fmt.Errorf("no common DMSG servers with remote visor")
}

// GetRemoteDmsgServers implements API. Gets the DMSG servers a remote visor is connected to.
func (v *Visor) GetRemoteDmsgServers(pk cipher.PubKey) ([]cipher.PubKey, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create a dmsg discovery client to query the discovery
	httpC := &http.Client{Timeout: 10 * time.Second}
	discClient := dmsgdisc.NewHTTP(v.conf.Dmsg.Discovery, httpC, v.log)

	entry, err := discClient.Entry(ctx, pk)
	if err != nil {
		return nil, fmt.Errorf("failed to get discovery entry for %s: %w", pk, err)
	}

	if entry.Client == nil {
		return nil, fmt.Errorf("no client entry for %s", pk)
	}

	return entry.Client.DelegatedServers, nil
}

// DmsgPing implements API. Measures round-trip time over dmsg connection.
func (v *Visor) DmsgPing(conf PingConfig) ([]time.Duration, error) {
	v.dmsgPingMx.Lock()
	defer v.dmsgPingMx.Unlock()

	pingEntry, ok := v.dmsgPingConns[conf.PK]
	if !ok {
		return nil, fmt.Errorf("no dmsg ping connection for %s, call DialDmsgPing first", conf.PK)
	}

	latencies := []time.Duration{}
	data := make([]byte, conf.PcktSize*1024)

	for i := 1; i <= conf.Tries; i++ {
		conn := pingEntry.conn
		msg := PingMsg{
			Timestamp: time.Now(),
			PingPk:    conf.PK,
			Data:      data,
		}
		pingData, err := json.Marshal(msg)
		if err != nil {
			return latencies, err
		}
		pingSizeMsg := PingSizeMsg{
			Size: len(pingData),
		}
		size, err := json.Marshal(pingSizeMsg)
		if err != nil {
			return latencies, err
		}

		start := time.Now()

		// Send size message
		if _, err = conn.Write(size); err != nil {
			return latencies, fmt.Errorf("write size: %w", err)
		}

		// Read "ok" ack with timeout
		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		buf := make([]byte, 32*1024)
		if _, err = conn.Read(buf); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return latencies, fmt.Errorf("read ack: %w", err)
		}

		// Send ping data
		if _, err = conn.Write(pingData); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return latencies, fmt.Errorf("write ping: %w", err)
		}

		// Read echo response with timeout
		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		if _, err = conn.Read(buf); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return latencies, fmt.Errorf("read echo: %w", err)
		}
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec

		rtt := time.Since(start)
		latencies = append(latencies, rtt)
	}
	return latencies, nil
}

// DmsgPingViaServer implements API. Performs a ping through a specific DMSG server.
// This is a convenience method that handles dial, ping, and cleanup.
func (v *Visor) DmsgPingViaServer(conf PingConfig, serverPK cipher.PubKey) ([]time.Duration, error) {
	// Dial the ping connection via the specific server
	if err := v.DialDmsgPingViaServer(conf.PK, serverPK); err != nil {
		return nil, fmt.Errorf("dial via server %s: %w", serverPK.String()[:16]+"...", err)
	}

	// Perform the ping
	latencies, err := v.DmsgPing(conf)

	// Always clean up the connection
	if stopErr := v.StopDmsgPing(conf.PK); stopErr != nil {
		v.log.WithError(stopErr).Debug("Failed to stop dmsg ping connection")
	}

	if err != nil {
		return nil, fmt.Errorf("ping via server %s: %w", serverPK.String()[:16]+"...", err)
	}

	return latencies, nil
}

// DmsgPingOnce implements API. Performs a single ping over dmsg connection.
func (v *Visor) DmsgPingOnce(conf PingConfig) (time.Duration, error) {
	v.dmsgPingMx.Lock()
	defer v.dmsgPingMx.Unlock()

	pingEntry, ok := v.dmsgPingConns[conf.PK]
	if !ok {
		return 0, fmt.Errorf("no dmsg ping connection for %s, call DialDmsgPing first", conf.PK)
	}

	data := make([]byte, conf.PcktSize*1024)
	conn := pingEntry.conn
	msg := PingMsg{
		Timestamp: time.Now(),
		PingPk:    conf.PK,
		Data:      data,
	}
	pingData, err := json.Marshal(msg)
	if err != nil {
		return 0, err
	}
	pingSizeMsg := PingSizeMsg{
		Size: len(pingData),
	}
	size, err := json.Marshal(pingSizeMsg)
	if err != nil {
		return 0, err
	}

	start := time.Now()

	// Send size message
	if _, err = conn.Write(size); err != nil {
		return 0, fmt.Errorf("write size: %w", err)
	}

	// Read "ok" ack with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	buf := make([]byte, 32*1024)
	if _, err = conn.Read(buf); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return 0, fmt.Errorf("read ack: %w", err)
	}

	// Send ping data
	if _, err = conn.Write(pingData); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return 0, fmt.Errorf("write ping: %w", err)
	}

	// Read echo response with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	if _, err = conn.Read(buf); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return 0, fmt.Errorf("read echo: %w", err)
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec

	return time.Since(start), nil
}

// DmsgPingOnceWithEcho performs a single dmsg ping with optional full echo.
// Returns bytes sent, bytes received, latency, and error.
// If echoFull is true, server echoes full payload (for bandwidth testing).
func (v *Visor) DmsgPingOnceWithEcho(conf PingConfig, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error) {
	v.dmsgPingMx.Lock()
	defer v.dmsgPingMx.Unlock()

	dmsgEntry, ok := v.dmsgPingConns[conf.PK]
	if !ok {
		return 0, 0, 0, fmt.Errorf("no dmsg ping connection for %s, call DialDmsgPing first", conf.PK)
	}

	data := make([]byte, conf.PcktSize*1024)
	conn := dmsgEntry.conn
	msg := PingMsg{
		Timestamp: time.Now(),
		PingPk:    conf.PK,
		Data:      data,
	}
	pingData, err := json.Marshal(msg)
	if err != nil {
		return 0, 0, 0, err
	}
	pingSizeMsg := PingSizeMsg{
		Size:     len(pingData),
		EchoFull: echoFull,
	}
	size, err := json.Marshal(pingSizeMsg)
	if err != nil {
		return 0, 0, 0, err
	}

	start := time.Now()

	// Send size message
	if _, err = conn.Write(size); err != nil {
		return 0, 0, 0, fmt.Errorf("write size: %w", err)
	}
	bytesSent += uint64(len(size))

	// Read "ok" ack with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	buf := make([]byte, 32*1024)
	n, err := conn.Read(buf)
	if err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return bytesSent, bytesReceived, 0, fmt.Errorf("read ack: %w", err)
	}
	bytesReceived += uint64(n) //nolint:gosec

	// Send ping data
	if _, err = conn.Write(pingData); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return bytesSent, bytesReceived, 0, fmt.Errorf("write ping: %w", err)
	}
	bytesSent += uint64(len(pingData))

	// Read echo response with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	if echoFull {
		// Read full payload echo
		var received int
		for received < len(pingData) {
			n, err = conn.Read(buf)
			if err != nil {
				conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
				return bytesSent, bytesReceived, 0, fmt.Errorf("read echo: %w", err)
			}
			received += n
			bytesReceived += uint64(n) //nolint:gosec
		}
	} else {
		// Read simple "pong" response
		n, err = conn.Read(buf)
		if err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return bytesSent, bytesReceived, 0, fmt.Errorf("read echo: %w", err)
		}
		bytesReceived += uint64(n) //nolint:gosec
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec

	return bytesSent, bytesReceived, time.Since(start), nil
}

// StopDmsgPing implements API.
func (v *Visor) StopDmsgPing(pk cipher.PubKey) error {
	v.dmsgPingMx.Lock()
	defer v.dmsgPingMx.Unlock()

	dmsgConn, ok := v.dmsgPingConns[pk]
	if !ok {
		return fmt.Errorf("no dmsg ping connection for %s", pk)
	}
	err := dmsgConn.conn.Close()
	if err != nil {
		return err
	}
	delete(v.dmsgPingConns, pk)
	return nil
}

// BandwidthTest implements API.
// Performs a bandwidth test over a skywire route by sending and receiving data for the specified duration.
func (v *Visor) BandwidthTest(conf BandwidthTestConfig) (BandwidthResult, error) {
	// First establish a ping connection if not already connected
	pingConf := PingConfig{
		PK:         conf.PK,
		Tries:      1,
		PcktSize:   conf.PacketSize,
		LocalRoute: conf.LocalRoute,
	}

	v.pingConnMx.Lock()
	_, exists := v.pingConns[conf.PK]
	v.pingConnMx.Unlock()

	if !exists {
		if err := v.DialPing(pingConf); err != nil {
			return BandwidthResult{}, fmt.Errorf("failed to dial: %w", err)
		}
	}

	v.pingConnMx.Lock()
	pingEntry, ok := v.pingConns[conf.PK]
	if !ok {
		v.pingConnMx.Unlock()
		return BandwidthResult{}, fmt.Errorf("no ping connection for %s", conf.PK)
	}
	conn := pingEntry.conn
	v.pingConnMx.Unlock()

	// Prepare packet with EchoFull flag for download measurement
	packetSize := conf.PacketSize
	if packetSize <= 0 {
		packetSize = 32 // Default 32KB packets for bandwidth test
	}
	data := make([]byte, packetSize*1024)

	var bytesSent, bytesReceived uint64
	start := time.Now()
	deadline := start.Add(conf.Duration)

	for time.Now().Before(deadline) {
		msg := PingMsg{
			Timestamp: time.Now(),
			PingPk:    conf.PK,
			Data:      data,
		}
		ping, err := json.Marshal(msg)
		if err != nil {
			return BandwidthResult{}, err
		}

		// Request full echo for download measurement
		pingSizeMsg := PingSizeMsg{
			Size:     len(ping),
			EchoFull: true,
		}
		size, err := json.Marshal(pingSizeMsg)
		if err != nil {
			return BandwidthResult{}, err
		}

		// Send size message
		if _, err = conn.Write(size); err != nil {
			return BandwidthResult{}, fmt.Errorf("write size: %w", err)
		}
		bytesSent += uint64(len(size))

		// Read "ok" ack
		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return BandwidthResult{}, fmt.Errorf("read ack: %w", err)
		}
		bytesReceived += uint64(n) //nolint:gosec

		// Send ping data
		if _, err = conn.Write(ping); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return BandwidthResult{}, fmt.Errorf("write ping: %w", err)
		}
		bytesSent += uint64(len(ping))

		// Read full echo response
		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		var received int
		for received < len(ping) {
			n, err = conn.Read(buf)
			if err != nil {
				conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
				return BandwidthResult{}, fmt.Errorf("read echo: %w", err)
			}
			received += n
			bytesReceived += uint64(n) //nolint:gosec
		}
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
	}

	duration := time.Since(start)
	durationSec := duration.Seconds()

	return BandwidthResult{
		BytesSent:     bytesSent,
		BytesReceived: bytesReceived,
		Duration:      duration,
		UploadSpeed:   float64(bytesSent) / 1024 / durationSec,
		DownloadSpeed: float64(bytesReceived) / 1024 / durationSec,
	}, nil
}

// DmsgBandwidthTest implements API.
// Performs a bandwidth test over dmsg by sending and receiving data for the specified duration.
func (v *Visor) DmsgBandwidthTest(conf BandwidthTestConfig) (BandwidthResult, error) {
	// First establish a dmsg ping connection if not already connected
	v.dmsgPingMx.Lock()
	_, exists := v.dmsgPingConns[conf.PK]
	v.dmsgPingMx.Unlock()

	if !exists {
		if err := v.DialDmsgPing(conf.PK); err != nil {
			return BandwidthResult{}, fmt.Errorf("failed to dial dmsg: %w", err)
		}
	}

	v.dmsgPingMx.Lock()
	dmsgEntry, ok := v.dmsgPingConns[conf.PK]
	if !ok {
		v.dmsgPingMx.Unlock()
		return BandwidthResult{}, fmt.Errorf("no dmsg ping connection for %s", conf.PK)
	}
	conn := dmsgEntry.conn
	v.dmsgPingMx.Unlock()

	// Prepare packet with EchoFull flag for download measurement
	packetSize := conf.PacketSize
	if packetSize <= 0 {
		packetSize = 32 // Default 32KB packets for bandwidth test
	}
	data := make([]byte, packetSize*1024)

	var bytesSent, bytesReceived uint64
	start := time.Now()
	deadline := start.Add(conf.Duration)

	for time.Now().Before(deadline) {
		msg := PingMsg{
			Timestamp: time.Now(),
			PingPk:    conf.PK,
			Data:      data,
		}
		ping, err := json.Marshal(msg)
		if err != nil {
			return BandwidthResult{}, err
		}

		// Request full echo for download measurement
		pingSizeMsg := PingSizeMsg{
			Size:     len(ping),
			EchoFull: true,
		}
		size, err := json.Marshal(pingSizeMsg)
		if err != nil {
			return BandwidthResult{}, err
		}

		// Send size message
		if _, err = conn.Write(size); err != nil {
			return BandwidthResult{}, fmt.Errorf("write size: %w", err)
		}
		bytesSent += uint64(len(size))

		// Read "ok" ack
		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return BandwidthResult{}, fmt.Errorf("read ack: %w", err)
		}
		bytesReceived += uint64(n) //nolint:gosec

		// Send ping data
		if _, err = conn.Write(ping); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return BandwidthResult{}, fmt.Errorf("write ping: %w", err)
		}
		bytesSent += uint64(len(ping))

		// Read full echo response
		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		var received int
		for received < len(ping) {
			n, err = conn.Read(buf)
			if err != nil {
				conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
				return BandwidthResult{}, fmt.Errorf("read echo: %w", err)
			}
			received += n
			bytesReceived += uint64(n) //nolint:gosec
		}
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
	}

	duration := time.Since(start)
	durationSec := duration.Seconds()

	return BandwidthResult{
		BytesSent:     bytesSent,
		BytesReceived: bytesReceived,
		Duration:      duration,
		UploadSpeed:   float64(bytesSent) / 1024 / durationSec,
		DownloadSpeed: float64(bytesReceived) / 1024 / durationSec,
	}, nil
}

// TestResult type of test result
type TestResult struct {
	PK     string
	Max    string
	Min    string
	Mean   string
	Status string
}

// ProxyTestConfig configures proxy testing
type ProxyTestConfig struct {
	Servers []cipher.PubKey `json:"servers"`  // Proxy servers to test
	TestURL string          `json:"test_url"` // URL to fetch through proxy (default: https://ip.skycoin.com)
	Timeout time.Duration   `json:"timeout"`  // Timeout per test (default: 30s)
}

// ProxyTestResult contains the result of a single proxy test
type ProxyTestResult struct {
	PK       string `json:"pk"`       // Proxy server public key
	Status   string `json:"status"`   // "OK", "FAIL", "TIMEOUT"
	Duration int64  `json:"duration"` // Duration in milliseconds
	IP       string `json:"ip"`       // IP returned by test
	Location string `json:"location"` // Geo location (City, Country)
	Error    string `json:"error"`    // Error message if failed
}

// TestVisor trying to test visor
func (v *Visor) TestVisor(conf PingConfig) ([]TestResult, error) {
	result := []TestResult{}
	if v.dmsgC == nil {
		return result, errors.New("dmsgC is not available")
	}

	publicVisors, err := v.dmsgC.AllEntries(context.TODO())
	if err != nil {
		return result, err
	}

	if conf.PubVisCount < len(publicVisors) {
		publicVisors = publicVisors[:conf.PubVisCount+1]
	}

	for _, publicVisor := range publicVisors {
		if publicVisor == v.conf.PK.Hex() {
			continue
		}

		if err := conf.PK.UnmarshalText([]byte(publicVisor)); err != nil {
			continue
		}
		err := v.DialPing(conf)
		if err != nil {
			result = append(result, TestResult{PK: conf.PK.String(), Max: fmt.Sprint(0), Min: fmt.Sprint(0), Mean: fmt.Sprint(0), Status: "Failed"})
			continue
		}
		latencies, err := v.Ping(conf)
		if err != nil {
			go v.StopPing(conf.PK) //nolint:errcheck,gosec
			result = append(result, TestResult{PK: conf.PK.String(), Max: fmt.Sprint(0), Min: fmt.Sprint(0), Mean: fmt.Sprint(0), Status: "Failed"})
			continue
		}
		var maxx, minn, mean, sumLatency time.Duration
		minn = time.Duration(10000000000)
		for _, latency := range latencies {
			if latency > maxx {
				maxx = latency
			}
			if latency < minn {
				minn = latency
			}
			sumLatency += latency
		}
		mean = sumLatency / time.Duration(len(latencies))
		result = append(result, TestResult{PK: conf.PK.String(), Max: fmt.Sprint(maxx), Min: fmt.Sprint(minn), Mean: fmt.Sprint(mean), Status: "Success"})
		v.StopPing(conf.PK) //nolint:errcheck,gosec
	}
	return result, nil
}

// TestProxy tests proxy servers by connecting through them and fetching a test URL
func (v *Visor) TestProxy(conf ProxyTestConfig) ([]ProxyTestResult, error) {
	results := make([]ProxyTestResult, 0, len(conf.Servers))

	// Set defaults
	if conf.TestURL == "" {
		conf.TestURL = "https://ip.skycoin.com"
	}
	if conf.Timeout == 0 {
		conf.Timeout = 30 * time.Second
	}

	// Get the skysocks-client address from config
	socksAddr := visorconfig.SkysocksClientAddr

	for _, serverPK := range conf.Servers {
		result := ProxyTestResult{
			PK: serverPK.String(),
		}

		start := time.Now()

		// Start skysocks-client with this server
		err := v.StartSkysocksClient(serverPK.String())
		if err != nil {
			result.Status = "FAIL"
			result.Error = fmt.Sprintf("failed to start client: %v", err)
			result.Duration = time.Since(start).Milliseconds()
			results = append(results, result)
			continue
		}

		// Give it a moment to establish connection
		time.Sleep(500 * time.Millisecond)

		// Create SOCKS5 dialer
		dialer, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
		if err != nil {
			v.StopSkysocksClients() //nolint:errcheck,gosec
			result.Status = "FAIL"
			result.Error = fmt.Sprintf("failed to create SOCKS dialer: %v", err)
			result.Duration = time.Since(start).Milliseconds()
			results = append(results, result)
			continue
		}

		// Create HTTP client with SOCKS transport
		httpTransport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		}
		httpClient := &http.Client{
			Transport: httpTransport,
			Timeout:   conf.Timeout,
		}

		// Make test request
		ctx, cancel := context.WithTimeout(context.Background(), conf.Timeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, conf.TestURL, nil)
		if err != nil {
			cancel()
			v.StopSkysocksClients() //nolint:errcheck,gosec
			result.Status = "FAIL"
			result.Error = fmt.Sprintf("failed to create request: %v", err)
			result.Duration = time.Since(start).Milliseconds()
			results = append(results, result)
			continue
		}

		resp, err := httpClient.Do(req)
		cancel()
		if err != nil {
			v.StopSkysocksClients() //nolint:errcheck,gosec
			if strings.Contains(err.Error(), "context deadline exceeded") {
				result.Status = "TIMEOUT"
			} else {
				result.Status = "FAIL"
			}
			result.Error = err.Error()
			result.Duration = time.Since(start).Milliseconds()
			results = append(results, result)
			continue
		}

		// Read response body
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck,gosec
		if err != nil {
			v.StopSkysocksClients() //nolint:errcheck,gosec
			result.Status = "FAIL"
			result.Error = fmt.Sprintf("failed to read response: %v", err)
			result.Duration = time.Since(start).Milliseconds()
			results = append(results, result)
			continue
		}

		// Parse response - try to extract IP and location
		// Expected format from ip.skycoin.com: {"ip":"1.2.3.4","country":"US","city":"New York",...}
		var ipData struct {
			IP      string `json:"ip"`
			Country string `json:"country"`
			City    string `json:"city"`
		}
		if err := json.Unmarshal(body, &ipData); err == nil {
			result.IP = ipData.IP
			if ipData.City != "" && ipData.Country != "" {
				result.Location = fmt.Sprintf("%s,%s", ipData.City, ipData.Country)
			} else if ipData.Country != "" {
				result.Location = ipData.Country
			}
		} else {
			// If JSON parsing fails, try to use the body as plain text IP
			result.IP = strings.TrimSpace(string(body))
		}

		result.Status = "OK"
		result.Duration = time.Since(start).Milliseconds()

		// Stop the client before next iteration
		v.StopSkysocksClients() //nolint:errcheck,gosec

		results = append(results, result)
	}

	return results, nil
}

// RoutingRule implements API.
func (v *Visor) RoutingRule(key routing.RouteID) (routing.Rule, error) {
	return v.router.Rule(key)
}

// SaveRoutingRule implements API.
func (v *Visor) SaveRoutingRule(rule routing.Rule) error {
	return v.router.SaveRule(rule)
}

// RemoveRoutingRule implements API.
func (v *Visor) RemoveRoutingRule(key routing.RouteID) error {
	v.router.DelRules([]routing.RouteID{key})
	return nil
}

// RouteGroups implements API.
func (v *Visor) RouteGroups() ([]RouteGroupInfo, error) {
	var routegroups []RouteGroupInfo

	rules := v.router.Rules()
	for _, rule := range rules {
		if rule.Type() != routing.RuleReverse {
			continue
		}

		fwdRID := rule.NextRouteID()
		rule, err := v.router.Rule(fwdRID)
		if err != nil {
			return nil, err
		}

		routegroups = append(routegroups, RouteGroupInfo{
			ConsumeRule: rule,
			FwdRule:     rule,
		})
	}

	return routegroups, nil
}

// Reload implements API.
func (v *Visor) Reload() error {
	return reload(v)
}

// Shutdown implements API.
func (v *Visor) Shutdown() error {
	defer os.Exit(0)
	return v.Close()
}

// RuntimeLogs returns visor runtime logs
func (v *Visor) RuntimeLogs() (string, error) {
	var builder strings.Builder
	builder.WriteString("[")
	logs, _ := v.logstore.GetLogs()
	builder.WriteString(strings.Join(logs, ","))
	builder.WriteString("]")
	return builder.String(), nil
}

// SetMinHops sets min_hops routing config of visor
func (v *Visor) SetMinHops(in uint16) error {
	v.router.SetMinHop(in)
	return v.conf.UpdateMinHops(in)
}

// SetCalculateRoutes sets calculate_routes routing config of visor
func (v *Visor) SetCalculateRoutes(enabled bool) error {
	// Update router's local route calculation setting
	v.router.SetForceLocalRoutes(enabled)
	return v.conf.UpdateCalculateRoutes(enabled)
}

// GetCalculateRoutes gets calculate_routes routing config of visor
func (v *Visor) GetCalculateRoutes() (bool, error) {
	return v.conf.GetCalculateRoutes(), nil
}

// SetSyncTPDData sets sync_tpd_data transport config of visor
func (v *Visor) SetSyncTPDData(enabled bool) error {
	// Update transport manager's TPD sync setting
	v.tpM.SetSyncTPDData(enabled)
	return v.conf.UpdateSyncTPDData(enabled)
}

// GetSyncTPDData gets sync_tpd_data transport config of visor
func (v *Visor) GetSyncTPDData() (bool, error) {
	return v.conf.GetSyncTPDData(), nil
}

// SetPersistentTransports sets min_hops routing config of visor
func (v *Visor) SetPersistentTransports(pTps []transport.PersistentTransports) error {
	v.tpM.SetPTpsCache(pTps)
	return v.conf.UpdatePersistentTransports(pTps)
}

// GetPersistentTransports gets min_hops routing config of visor
func (v *Visor) GetPersistentTransports() ([]transport.PersistentTransports, error) {
	return v.conf.GetPersistentTransports()
}

// SetLogRotationInterval sets log_rotation_interval config of visor
func (v *Visor) SetLogRotationInterval(d visorconfig.Duration) error {
	return v.conf.UpdateLogRotationInterval(d)
}

// GetLogRotationInterval gets log_rotation_interval config of visor
func (v *Visor) GetLogRotationInterval() (visorconfig.Duration, error) {
	return v.conf.GetLogRotationInterval()
}

// SetPublicAutoconnect sets public_autoconnect config of visor
func (v *Visor) SetPublicAutoconnect(pAc bool) error {
	return v.conf.UpdatePublicAutoconnect(pAc)
}

// PublicAutoconnectStatus returns whether public autoconnect is currently running
func (v *Visor) PublicAutoconnectStatus() (bool, error) {
	return v.IsPublicAutoconnectRunning(), nil
}

// GetVPNClientAddress get PK address of server set on vpn-client
func (v *Visor) GetVPNClientAddress() string {
	for _, v := range v.conf.Launcher.Apps {
		if v.Name == visorconfig.VPNClientName {
			for index := range v.Args {
				if v.Args[index] == "--srv" {
					return v.Args[index+1]
				}
			}
		}
	}
	return ""
}

// GetSkysocksClientAddress get PK address of server set on skysocks-client
func (v *Visor) GetSkysocksClientAddress() string {
	for _, v := range v.conf.Launcher.Apps {
		if v.Name == visorconfig.SkysocksClientAddr {
			for index := range v.Args {
				if v.Args[index] == "--srv" {
					return v.Args[index+1]
				}
			}
		}
	}
	return ""
}

// IsDMSGClientReady return availability of dsmg client
func (v *Visor) IsDMSGClientReady() (bool, error) {
	if v.isDTMReady() {
		dmsgTracker, _ := v.dtm.Get(v.conf.PK)
		if dmsgTracker.ServerPK.Hex()[:5] != "00000" {
			return true, nil
		}
	}
	return false, errors.New("dmsg client is not ready")
}

// DMSGServers returns list of connected DMSG servers with their latencies.
// Servers are sorted by latency (lowest first). Servers with latency 0 have not been measured yet.
func (v *Visor) DMSGServers() ([]DMSGServerInfo, error) {
	if v.dmsgC == nil {
		return nil, errors.New("dmsg client not available")
	}

	// Get connected servers
	serverPKs := v.dmsgC.ConnectedServersPK()
	if len(serverPKs) == 0 {
		return []DMSGServerInfo{}, nil
	}

	// Build list with latencies
	servers := make([]DMSGServerInfo, 0, len(serverPKs))
	v.dmsgServerLatenciesMu.RLock()
	for _, pkStr := range serverPKs {
		var pk cipher.PubKey
		if err := pk.Set(pkStr); err != nil {
			continue
		}
		info := DMSGServerInfo{
			PK:      pk,
			Latency: v.dmsgServerLatencies[pk],
		}
		servers = append(servers, info)
	}
	v.dmsgServerLatenciesMu.RUnlock()

	// Sort by latency (lowest first, 0/unmeasured at end)
	for i := 0; i < len(servers)-1; i++ {
		for j := i + 1; j < len(servers); j++ {
			si, sj := servers[i], servers[j]
			// 0 latency (unmeasured) goes to the end
			if si.Latency == 0 && sj.Latency > 0 {
				servers[i], servers[j] = servers[j], servers[i]
			} else if si.Latency > 0 && sj.Latency > 0 && sj.Latency < si.Latency {
				servers[i], servers[j] = servers[j], servers[i]
			}
		}
	}

	return servers, nil
}

// RegisterHTTPPort implements API.
func (v *Visor) RegisterHTTPPort(localPort int) error {
	v.allowedMX.Lock()
	defer v.allowedMX.Unlock()
	ok := isPortAvailable(v.log, localPort)
	if ok {
		return fmt.Errorf("No connection on local port :%v", localPort)
	}
	if v.allowedPorts[localPort] {
		return fmt.Errorf("Port :%v already registered", localPort)
	}
	v.allowedPorts[localPort] = true
	return nil
}

// DeregisterHTTPPort implements API.
func (v *Visor) DeregisterHTTPPort(localPort int) error {
	v.allowedMX.Lock()
	defer v.allowedMX.Unlock()
	if !v.allowedPorts[localPort] {
		return fmt.Errorf("Port :%v not registered", localPort)
	}
	delete(v.allowedPorts, localPort)
	return nil
}

// ListHTTPPorts implements API.
func (v *Visor) ListHTTPPorts() ([]int, error) {
	v.allowedMX.Lock()
	defer v.allowedMX.Unlock()
	keys := make([]int, 0, len(v.allowedPorts))
	for k := range v.allowedPorts {
		keys = append(keys, k)
	}
	return keys, nil
}

// Connect implements API.
func (v *Visor) Connect(remotePK cipher.PubKey, remotePort, localPort int) (uuid.UUID, error) {
	ok := isPortAvailable(v.log, localPort)
	if !ok {
		return uuid.UUID{}, fmt.Errorf(":%v local port already in use", localPort)
	}
	connApp := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: remotePK,
		Port:   routing.Port(skyenv.SkyForwardingServerPort),
	}
	conn, err := appnet.Dial(connApp)
	if err != nil {
		return uuid.UUID{}, err
	}
	remoteConn, err := appnet.WrapConn(conn)
	if err != nil {
		return uuid.UUID{}, err
	}

	cMsg := clientMsg{
		Port: remotePort,
	}

	clientMsg, err := json.Marshal(cMsg)
	if err != nil {
		return uuid.UUID{}, err
	}
	_, err = remoteConn.Write(clientMsg)
	if err != nil {
		return uuid.UUID{}, err
	}
	v.log.Debugf("Msg sent %s", clientMsg)

	buf := make([]byte, 32*1024)
	n, err := remoteConn.Read(buf)
	if err != nil {
		return uuid.UUID{}, err
	}
	var sReply serverReply
	err = json.Unmarshal(buf[:n], &sReply)
	if err != nil {
		return uuid.UUID{}, err
	}
	v.log.Debugf("Received: %v", sReply)

	if sReply.Error != nil {
		sErr := *sReply.Error
		v.log.WithError(fmt.Errorf("%s", sErr)).Error("Server closed with error")
		return uuid.UUID{}, fmt.Errorf("%s", sErr)
	}

	forwardConn := appnet.NewForwardConn(v.log, remoteConn, remotePort, localPort)
	forwardConn.Serve()
	return forwardConn.ID, nil
}

// Disconnect implements API.
func (v *Visor) Disconnect(id uuid.UUID) error {
	forwardConn := appnet.GetForwardConn(id)
	return forwardConn.Close()
}

// List implements API.
func (v *Visor) List() (map[uuid.UUID]*appnet.ForwardConn, error) {
	return appnet.GetAllForwardConns(), nil
}

// ConnectRawTCP implements API. Establishes a raw TCP port forwarding connection over skywire.
func (v *Visor) ConnectRawTCP(remotePK cipher.PubKey, remotePort, localPort int) (uuid.UUID, error) {
	ok := isPortAvailable(v.log, localPort)
	if !ok {
		return uuid.UUID{}, fmt.Errorf(":%v local port already in use", localPort)
	}
	connApp := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: remotePK,
		Port:   routing.Port(skyenv.SkyForwardingServerPort),
	}
	conn, err := appnet.Dial(connApp)
	if err != nil {
		return uuid.UUID{}, err
	}
	remoteConn, err := appnet.WrapConn(conn)
	if err != nil {
		return uuid.UUID{}, err
	}

	cMsg := clientMsg{
		Port:   remotePort,
		RawTCP: true,
	}

	clientMsgBytes, err := json.Marshal(cMsg)
	if err != nil {
		return uuid.UUID{}, err
	}
	_, err = remoteConn.Write(clientMsgBytes)
	if err != nil {
		return uuid.UUID{}, err
	}
	v.log.Debugf("Raw TCP msg sent %s", clientMsgBytes)

	buf := make([]byte, 32*1024)
	n, err := remoteConn.Read(buf)
	if err != nil {
		return uuid.UUID{}, err
	}
	var sReply serverReply
	err = json.Unmarshal(buf[:n], &sReply)
	if err != nil {
		return uuid.UUID{}, err
	}
	v.log.Debugf("Received: %v", sReply)

	if sReply.Error != nil {
		sErr := *sReply.Error
		v.log.WithError(fmt.Errorf("%s", sErr)).Error("Server closed with error")
		return uuid.UUID{}, fmt.Errorf("%s", sErr)
	}

	forwardConn, err := appnet.NewRawTCPForwardConn(v.log, remoteConn, remotePort, localPort)
	if err != nil {
		_ = remoteConn.Close() //nolint:errcheck
		return uuid.UUID{}, err
	}
	forwardConn.Serve()
	return forwardConn.ID, nil
}

// DisconnectRawTCP implements API.
func (v *Visor) DisconnectRawTCP(id uuid.UUID) error {
	forwardConn := appnet.GetRawTCPForwardConn(id)
	if forwardConn == nil {
		return fmt.Errorf("raw TCP forward connection not found: %s", id)
	}
	return forwardConn.Close()
}

// ListRawTCP implements API.
func (v *Visor) ListRawTCP() (map[uuid.UUID]*appnet.RawTCPForwardConn, error) {
	return appnet.GetAllRawTCPForwardConns(), nil
}

func isPortAvailable(log *logging.Logger, port int) bool {
	timeout := time.Second
	conn, err := net.DialTimeout("tcp", fmt.Sprintf(":%v", port), timeout)
	if err != nil {
		return true
	}
	if conn != nil {
		defer closeConn(log, conn)
		return false
	}
	return true
}

func isPortRegistered(port int, v *Visor) bool {
	ports, err := v.ListHTTPPorts()
	if err != nil {
		return false
	}
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

// ReinitiateModule implements API.
func (v *Visor) ReinitiateModule(module string) error {
	ctx := context.Background()
	ctx = context.WithValue(ctx, runtimeErrsKey, v.runtimeErrors)
	switch module {
	case "dmsg":
		return reinitiateDmsg(ctx, v)
	case "stcpr":
		reinitiateStpcr(ctx, v)
		return nil
	default:
		return fmt.Errorf("this module no allowed to reinitiate")
	}
}

func reinitiateStpcr(ctx context.Context, v *Visor) {
	v.tpM.InitClient(ctx, types.STCPR, 0)
}

func reinitiateDmsg(ctx context.Context, v *Visor) error {
	v.log.Info("Starting dmsg reinitialization with new client...")

	if err := shutdownDmsgDependentComponents(v, v.log); err != nil {
		v.log.WithError(err).Warn("Error during dependent components shutdown, continuing...")
	}

	if err := initDmsg(ctx, v, v.log); err != nil {
		return fmt.Errorf("failed to initialize new dmsg client: %w", err)
	}

	if err := initDmsgCtrl(ctx, v, v.log); err != nil {
		return fmt.Errorf("failed to reinitialize dmsg ctrl: %w", err)
	}

	if err := initDmsgTrackers(ctx, v, v.log); err != nil {
		return fmt.Errorf("failed to reinitialize dmsg trackers: %w", err)
	}

	if err := initDmsgHTTPLogServer(ctx, v, v.log); err != nil {
		return fmt.Errorf("failed to reinitialize dmsg http log server: %w", err)
	}

	if err := initDmsgpty(ctx, v, v.log); err != nil {
		return fmt.Errorf("failed to reinitialize dmsgpty: %w", err)
	}

	v.log.Info("Dmsg reinitialization completed successfully with new client")
	return nil
}

func shutdownDmsgDependentComponents(v *Visor, log *logging.Logger) error {
	// Order matters: close dependents first, then dmsg client itself
	components := []string{
		"router.serve", // a.k.a. dmsgpty
		"dmsghttp.logserver",
		"dmsg_tracker_manager",
		"dmsgctrl",
		"dmsg", // Close the dmsg client last, after all dependents
	}

	v.closeMu.Lock()
	defer v.closeMu.Unlock()

	var errs []error
	newCloseStack := make([]closer, 0, len(v.closeStack))

	for _, c := range v.closeStack {
		shouldClose := false
		for _, component := range components {
			if c.src == component {
				shouldClose = true
				break
			}
		}

		if shouldClose {
			if err := c.fn(); err != nil {
				log.WithError(err).WithField("component", c.src).Warn("Failed to close component")
				errs = append(errs, err)
			} else {
				log.WithField("component", c.src).Debug("Successfully closed component")
			}
		} else {
			newCloseStack = append(newCloseStack, c)
		}
	}

	v.closeStack = newCloseStack

	v.closeMu.Unlock()
	v.initLock.Lock()
	v.dtmReady = make(chan struct{})
	v.dtmReadyOnce = sync.Once{}
	v.initLock.Unlock()
	v.closeMu.Lock()

	if len(errs) > 0 {
		return fmt.Errorf("encountered %d errors during shutdown", len(errs))
	}
	return nil
}

// StartUIServer starts the embedded UI server on the specified address.
// If addr is empty, uses the configured LocalAddr or defaults to localhost:8081.
func (v *Visor) StartUIServer(addr string) error {
	v.uiServerMu.Lock()
	defer v.uiServerMu.Unlock()

	if v.uiServerRunning {
		return fmt.Errorf("UI server is already running on %s", v.uiServerAddr)
	}

	// Determine address
	if addr == "" {
		if v.conf.UIServer != nil && v.conf.UIServer.LocalAddr != "" {
			addr = v.conf.UIServer.LocalAddr
		} else {
			addr = "localhost:8081"
		}
	}

	// Create tpviz server
	tpvizCfg := tpviz.DefaultConfig()
	tpvizServer := tpviz.NewServer(tpvizCfg)

	// Set visor API directly via adapter
	adapter := &visorAPIAdapter{v: v}
	tpvizServer.SetVisorAPI(adapter, v.conf.PK.Hex())

	// Start cache refresh
	tpvizServer.Start()

	// Start HTTP server
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		tpvizServer.Stop()
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           tpvizServer.Handler(),
		ReadTimeout:       visorconfig.HTTPReadTimeout,
		WriteTimeout:      visorconfig.HTTPWriteTimeout,
		IdleTimeout:       visorconfig.HTTPIdleTimeout,
		ReadHeaderTimeout: visorconfig.HTTPReadHeaderTimeout,
	}

	go func() {
		v.log.Infof("UI server started on http://%s", addr)
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			v.log.WithError(err).Error("UI server exited with error")
		}
	}()

	v.uiServer = srv
	v.uiServerAddr = addr
	v.uiServerRunning = true

	return nil
}

// StopUIServer stops the embedded UI server if running.
func (v *Visor) StopUIServer() error {
	v.uiServerMu.Lock()
	defer v.uiServerMu.Unlock()

	if !v.uiServerRunning {
		return fmt.Errorf("UI server is not running")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := v.uiServer.Shutdown(ctx); err != nil {
		v.log.WithError(err).Warn("UI server shutdown error")
	}

	v.uiServer = nil
	v.uiServerAddr = ""
	v.uiServerRunning = false

	v.log.Info("UI server stopped")
	return nil
}

// UIServerStatus returns the current status of the UI server.
func (v *Visor) UIServerStatus() (*UIServerStatus, error) {
	v.uiServerMu.Lock()
	defer v.uiServerMu.Unlock()

	status := &UIServerStatus{
		Running: v.uiServerRunning,
	}

	if v.uiServerRunning {
		status.LocalAddr = v.uiServerAddr
	}

	// Include configured DMSG port if UI server config exists
	if v.conf.UIServer != nil {
		status.DmsgPort = v.conf.UIServer.DmsgPort
	}

	return status, nil
}

// DmsgHTTPRequest represents an HTTP request to be made over dmsg
type DmsgHTTPRequest struct {
	URL    string            `json:"url"`
	Method string            `json:"method"`
	Header map[string]string `json:"header,omitempty"`
	Body   []byte            `json:"body,omitempty"`
}

// DmsgHTTPResponse represents an HTTP response received over dmsg
type DmsgHTTPResponse struct {
	StatusCode int               `json:"status_code"`
	Status     string            `json:"status"`
	Header     map[string]string `json:"header,omitempty"`
	Body       []byte            `json:"body,omitempty"`
}

// DmsgHTTP implements API. Performs an HTTP request over dmsg using the visor's dmsg client.
func (v *Visor) DmsgHTTP(req DmsgHTTPRequest) (*DmsgHTTPResponse, error) {
	if v.dmsgC == nil {
		return nil, fmt.Errorf("dmsg client not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-v.dmsgC.Ready():
	}

	// Create HTTP transport using visor's dmsg client
	transport := dmsgHTTPTransport{
		ctx:   ctx,
		dmsgC: v.dmsgC,
	}

	httpClient := &http.Client{
		Transport: &transport,
		Timeout:   55 * time.Second,
	}

	// Build HTTP request
	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	for k, v := range req.Header {
		httpReq.Header.Set(k, v)
	}

	// Perform request
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Build response
	response := &DmsgHTTPResponse{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Header:     make(map[string]string),
		Body:       body,
	}

	for k, v := range resp.Header {
		if len(v) > 0 {
			response.Header[k] = v[0]
		}
	}

	return response, nil
}

// TPSStatus returns the status of the embedded Transport Setup Node.
func (v *Visor) TPSStatus() (*TPSStatus, error) {
	v.initLock.RLock()
	tps := v.embeddedTPS
	v.initLock.RUnlock()

	status := &TPSStatus{
		Enabled: tps != nil,
	}
	if tps != nil {
		status.PubKey = tps.pk
	}
	return status, nil
}

// TPSAddTransport uses the embedded TPS to add a transport on a target visor.
func (v *Visor) TPSAddTransport(targetPK, remotePK cipher.PubKey, tpType string) (*TPSTransportResponse, error) {
	v.initLock.RLock()
	tps := v.embeddedTPS
	v.initLock.RUnlock()

	if tps == nil {
		return nil, fmt.Errorf("embedded TPS not configured (tps_sk not set)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := tps.AddTransport(ctx, targetPK, remotePK, types.Type(tpType))
	if err != nil {
		return nil, err
	}

	return &TPSTransportResponse{
		ID:     resp.ID,
		Local:  resp.Local,
		Remote: resp.Remote,
		Type:   string(resp.Type),
	}, nil
}

// TPSRemoveTransport uses the embedded TPS to remove a transport on a target visor.
func (v *Visor) TPSRemoveTransport(targetPK cipher.PubKey, tpID uuid.UUID) error {
	v.initLock.RLock()
	tps := v.embeddedTPS
	v.initLock.RUnlock()

	if tps == nil {
		return fmt.Errorf("embedded TPS not configured (tps_sk not set)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return tps.RemoveTransport(ctx, targetPK, tpID)
}

// TPSGetTransports uses the embedded TPS to get transports from a target visor.
func (v *Visor) TPSGetTransports(targetPK cipher.PubKey) ([]TPSTransportResponse, error) {
	v.initLock.RLock()
	tps := v.embeddedTPS
	v.initLock.RUnlock()

	if tps == nil {
		return nil, fmt.Errorf("embedded TPS not configured (tps_sk not set)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := tps.GetTransports(ctx, targetPK)
	if err != nil {
		return nil, err
	}

	result := make([]TPSTransportResponse, len(resp))
	for i, tr := range resp {
		result[i] = TPSTransportResponse{
			ID:     tr.ID,
			Local:  tr.Local,
			Remote: tr.Remote,
			Type:   string(tr.Type),
		}
	}
	return result, nil
}

// GetTransportSetupNodes returns the whitelisted transport setup node public keys.
func (v *Visor) GetTransportSetupNodes() ([]cipher.PubKey, error) {
	return v.conf.Transport.TransportSetupPKs, nil
}

// GetTransportSetupNodesSorted returns TPS nodes sorted by health (healthy first, then by latency).
func (v *Visor) GetTransportSetupNodesSorted() ([]cipher.PubKey, error) {
	if v.nodeHealthTracker == nil {
		// Fall back to unsorted list if health tracker not initialized
		return v.conf.Transport.TransportSetupPKs, nil
	}
	return v.nodeHealthTracker.GetTPSNodesSorted(), nil
}

// GetRouteSetupNodesSorted returns RSN nodes sorted by health (healthy first, then by latency).
func (v *Visor) GetRouteSetupNodesSorted() ([]cipher.PubKey, error) {
	if v.nodeHealthTracker == nil {
		// Fall back to unsorted list if health tracker not initialized
		return v.conf.Routing.RouteSetupNodes, nil
	}
	return v.nodeHealthTracker.GetRSNNodesSorted(), nil
}

// GetTPSHealth returns health status for all configured TPS nodes.
func (v *Visor) GetTPSHealth() ([]NodeHealth, error) {
	if v.nodeHealthTracker == nil {
		return nil, fmt.Errorf("node health tracker not initialized")
	}
	return v.nodeHealthTracker.GetTPSHealth(), nil
}

// GetRSNHealth returns health status for all configured RSN nodes.
func (v *Visor) GetRSNHealth() ([]NodeHealth, error) {
	if v.nodeHealthTracker == nil {
		return nil, fmt.Errorf("node health tracker not initialized")
	}
	return v.nodeHealthTracker.GetRSNHealth(), nil
}

// TPSExternalHealthCheck dials an external TPS over dmsg and performs a health check.
func (v *Visor) TPSExternalHealthCheck(tpsPK cipher.PubKey) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := v.dmsgC.Dial(ctx, dmsg.Addr{PK: tpsPK, Port: skyenv.DmsgTransportSetupServicePort})
	if err != nil {
		return fmt.Errorf("failed to dial TPS %s: %w", tpsPK, err)
	}
	defer conn.Close() //nolint:errcheck

	client := rpc.NewClient(conn)
	defer client.Close() //nolint:errcheck

	var reply TPSHealthCheckReply
	if err := client.Call("SetupRPCGateway.HealthCheck", &TPSHealthCheckArgs{}, &reply); err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}

	if reply.Status != "OK" {
		return fmt.Errorf("health check returned non-OK status: %s", reply.Status)
	}
	return nil
}

// TPSHealthCheckArgs is empty input for health check.
type TPSHealthCheckArgs struct{}

// TPSHealthCheckReply is the health check response.
type TPSHealthCheckReply struct {
	Status string
}

// TPSExternalAddTransport dials an external TPS over dmsg and requests transport setup.
func (v *Visor) TPSExternalAddTransport(tpsPK, targetPK, remotePK cipher.PubKey, tpType string) (*TPSTransportResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := v.dmsgC.Dial(ctx, dmsg.Addr{PK: tpsPK, Port: skyenv.DmsgTransportSetupServicePort})
	if err != nil {
		return nil, fmt.Errorf("failed to dial TPS %s: %w", tpsPK, err)
	}
	defer conn.Close() //nolint:errcheck

	client := rpc.NewClient(conn)
	defer client.Close() //nolint:errcheck

	req := &TPSSetupRequest{
		TargetPK: targetPK,
		RemotePK: remotePK,
		Type:     tpType,
	}
	var reply TPSSetupResponse
	if err := client.Call("SetupRPCGateway.AddTransport", req, &reply); err != nil {
		return nil, fmt.Errorf("AddTransport RPC failed: %w", err)
	}

	return &TPSTransportResponse{
		ID:     reply.ID,
		Local:  reply.Local,
		Remote: reply.Remote,
		Type:   reply.Type,
	}, nil
}

// TPSSetupRequest is input for AddTransport RPC via external TPS.
type TPSSetupRequest struct {
	TargetPK cipher.PubKey
	RemotePK cipher.PubKey
	Type     string
}

// TPSSetupResponse is the response for AddTransport via external TPS.
type TPSSetupResponse struct {
	ID     uuid.UUID
	Local  cipher.PubKey
	Remote cipher.PubKey
	Type   string
}

// TPSExternalGetTransports dials an external TPS over dmsg and requests transport list.
func (v *Visor) TPSExternalGetTransports(tpsPK, targetPK cipher.PubKey) ([]TPSTransportResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := v.dmsgC.Dial(ctx, dmsg.Addr{PK: tpsPK, Port: skyenv.DmsgTransportSetupServicePort})
	if err != nil {
		return nil, fmt.Errorf("failed to dial TPS %s: %w", tpsPK, err)
	}
	defer conn.Close() //nolint:errcheck

	client := rpc.NewClient(conn)
	defer client.Close() //nolint:errcheck

	req := &TPSGetTransportsRequest{TargetPK: targetPK}
	var reply TPSGetTransportsResponse
	if err := client.Call("SetupRPCGateway.GetTransports", req, &reply); err != nil {
		return nil, fmt.Errorf("GetTransports RPC failed: %w", err)
	}

	result := make([]TPSTransportResponse, len(reply.Transports))
	for i, tr := range reply.Transports {
		result[i] = TPSTransportResponse(tr)
	}
	return result, nil
}

// TPSGetTransportsRequest is input for GetTransports RPC via external TPS.
type TPSGetTransportsRequest struct {
	TargetPK cipher.PubKey
}

// TPSGetTransportsResponse is the response for GetTransports via external TPS.
type TPSGetTransportsResponse struct {
	Transports []TPSSetupResponse
}

// dmsgHTTPTransport implements http.RoundTripper using the visor's dmsg client
type dmsgHTTPTransport struct {
	ctx   context.Context
	dmsgC *dmsg.Client
}

func (t *dmsgHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var hostAddr dmsg.Addr
	if err := hostAddr.Set(req.Host); err != nil {
		return nil, fmt.Errorf("invalid host address: %w", err)
	}
	if hostAddr.Port == 0 {
		hostAddr.Port = 80
	}

	stream, err := t.dmsgC.DialStream(req.Context(), hostAddr)
	if err != nil {
		return nil, err
	}

	if err = req.Write(stream); err != nil {
		_ = stream.Close() //nolint:errcheck
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(stream), req)
	if err != nil {
		_ = stream.Close() //nolint:errcheck
		return nil, err
	}

	// Wrap response body to close stream when done
	resp.Body = &dmsgStreamBody{
		ReadCloser: resp.Body,
		stream:     stream,
	}

	return resp, nil
}

type dmsgStreamBody struct {
	io.ReadCloser
	stream *dmsg.Stream
}

func (b *dmsgStreamBody) Close() error {
	err1 := b.ReadCloser.Close()
	err2 := b.stream.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
