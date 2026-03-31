// Package visor pkg/visor/api.go
package visor

import (
	"context"
	"net"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
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
	RuntimeStats() (*RuntimeStatsInfo, error)
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

	// LAN DMSG server
	SetLANDmsgServer(LANDmsgServerInfo) error

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
	SetMuxRoutes(n int) error
	SetMuxMode(mode string) error

	//transports
	TransportTypes() ([]string, error)
	Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error)
	Transport(tid uuid.UUID) (*TransportSummary, error)
	AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration, label string, noRegister bool, skipLatencyProbe bool) (*TransportSummary, error)
	SetSTCPAddr(pk cipher.PubKey, addr string) error
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
	ActiveRoutes() ([]AppRouteStatus, error)
	AddMuxRoute(appName string, tpID uuid.UUID) error
	RemoveMuxRoute(appName string, tpID uuid.UUID) error
	ServiceHealth() ([]ServiceHealthEntry, error)
	FetchServiceData(service, path string) ([]byte, error)
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

// HealthInfo carries information about visor's services health represented as boolean value (i32 value)
type HealthInfo struct {
	ServicesHealth string `json:"services_health"`
}

// RuntimeStatsInfo carries Go runtime statistics for the visor process.
type RuntimeStatsInfo struct {
	NumGoroutine int    `json:"num_goroutine"`
	NumCPU       int    `json:"num_cpu"`
	GOMAXPROCS   int    `json:"gomaxprocs"`
	GoVersion    string `json:"go_version"`
	// Memory stats in bytes
	MemAlloc      uint64 `json:"mem_alloc"`
	MemTotalAlloc uint64 `json:"mem_total_alloc"`
	MemSys        uint64 `json:"mem_sys"`
	MemHeapAlloc  uint64 `json:"mem_heap_alloc"`
	MemHeapSys    uint64 `json:"mem_heap_sys"`
	NumGC         uint32 `json:"num_gc"`
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

// AppRouteStatus combines route status with the app that owns it.
type AppRouteStatus struct {
	AppName string             `json:"app_name"`
	Route   router.RouteStatus `json:"route"`
}

// ServiceHealthEntry represents the health status of a deployment service.
type ServiceHealthEntry struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Status    string `json:"status"`
	Version   string `json:"version,omitempty"`
	LatencyMs int64  `json:"latency_ms"`
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

// TPSHealthCheckArgs is empty input for health check.
type TPSHealthCheckArgs struct{}

// TPSHealthCheckReply is the health check response.
type TPSHealthCheckReply struct {
	Status string
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

// TPSGetTransportsRequest is input for GetTransports RPC via external TPS.
type TPSGetTransportsRequest struct {
	TargetPK cipher.PubKey
}

// TPSGetTransportsResponse is the response for GetTransports via external TPS.
type TPSGetTransportsResponse struct {
	Transports []TPSSetupResponse
}
