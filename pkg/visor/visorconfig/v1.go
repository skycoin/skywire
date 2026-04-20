// Package visorconfig pkg/visor/visorconfig/v1.go
package visorconfig

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// V1 is visor config
type V1 struct {
	*Common
	mu sync.RWMutex

	Dmsg          *dmsgc.DmsgConfig   `json:"dmsg"`
	Dmsgpty       *Dmsgpty            `json:"dmsgpty,omitempty"`
	UIServer      *UIServer           `json:"ui_server,omitempty"`
	LogServer     *LogServer          `json:"log_server,omitempty"`
	DmsgWeb       *DmsgWebConfig      `json:"dmsg_web,omitempty"`
	SkynetWeb     *SkynetWebConfig    `json:"skynet_web,omitempty"`
	Rewards       *RewardsConfig      `json:"rewards,omitempty"`
	DHT           *DHTConfig          `json:"dht,omitempty"`
	STCP          *network.STCPConfig `json:"skywire-tcp,omitempty"`
	Transport     *Transport          `json:"transport"`
	Routing       *Routing            `json:"routing"`
	UptimeTracker *UptimeTracker      `json:"uptime_tracker,omitempty"`
	Launcher      *Launcher           `json:"launcher"`

	SurveyWhitelist     []cipher.PubKey `json:"survey_whitelist"`
	UserSurveyWhitelist []cipher.PubKey `json:"user_survey_whitelist,omitempty"` // user-added keys, preserved across config refresh
	Hypervisors         []cipher.PubKey `json:"hypervisors"`
	CLIAddr             string          `json:"cli_addr"`

	LogLevel             string                           `json:"log_level"`
	LocalPath            string                           `json:"local_path"`
	StunServers          []string                         `json:"stun_servers"`
	ShutdownTimeout      Duration                         `json:"shutdown_timeout,omitempty"` // time value, examples: 10s, 1m, etc
	IsPublic             bool                             `json:"is_public"`
	PublicVisorConfig    *PublicVisorConfig               `json:"public_visor,omitempty"`
	GeoIP                string                           `json:"geoip"`
	PersistentTransports []transport.PersistentTransports `json:"persistent_transports"`
	ConfService          string                           `json:"conf_service,omitempty"`      // HTTP URL for config bootstrap service
	ConfServiceDmsg      string                           `json:"conf_service_dmsg,omitempty"` // DMSG URL for config bootstrap service
	RewardAddress        string                           `json:"reward_address,omitempty"`
	RewardSystem         string                           `json:"reward_system,omitempty"`
	RewardSystemDmsg     string                           `json:"reward_system_dmsg,omitempty"`
	MemoryLimit          string                           `json:"memory_limit,omitempty"` // Go memory limit (e.g., "256MiB", "auto" for 60% of available RAM)

	Hypervisor *HypervisorConfig `json:"hypervisor,omitempty"`
}

// Dmsgpty configures the dmsgpty-host.
type Dmsgpty struct {
	DmsgPort  uint16          `json:"dmsg_port"`
	CLINet    string          `json:"cli_network"`
	CLIAddr   string          `json:"cli_address"`
	Whitelist []cipher.PubKey `json:"whitelist"`
}

// UIServer configures the visor UI server (serves tp-viz and other UIs).
type UIServer struct {
	Enable        bool            `json:"enable"`         // Enable the UI server
	LocalAddr     string          `json:"local_addr"`     // Local HTTP address (default: localhost:8081)
	DmsgPort      uint16          `json:"dmsg_port"`      // DMSG port to serve on (default: 81, 0 to disable)
	DmsgWhitelist []cipher.PubKey `json:"dmsg_whitelist"` // Keys allowed to access via DMSG
	SurveyDir     string          `json:"survey_dir"`     // Directory with visor surveys for IP-based grouping
}

// LogServer configures the dmsghttp log server's optional localhost endpoint.
// When LocalAddr is set (non-empty), the log server also serves on localhost without authentication.
type LogServer struct {
	// LocalAddr enables serving on localhost (e.g., "localhost:8002" or "127.0.0.1:8002").
	// If empty, localhost serving is disabled (dmsg-only mode).
	LocalAddr string `json:"local_addr"`
}

// RewardsConfig configures the reward system UI when hosted by the visor.
type RewardsConfig struct {
	Enable          bool            `json:"enable"`
	WorkDir         string          `json:"work_dir"`
	Whitelist       []cipher.PubKey `json:"whitelist,omitempty"`
	CanonicalDomain string          `json:"canonical_domain,omitempty"`
	SkycoinNode     string          `json:"skycoin_node,omitempty"`
	LoginNode       string          `json:"login_node,omitempty"`
}

// DHTConfig configures the Kademlia DHT subsystem.
// DHT is always enabled when DMSG is available — this config only
// controls optional parameters like full node mode and trust tiers.
type DHTConfig struct {
	// BootstrapPKs are public keys of seed DHT nodes to contact on startup.
	// If empty, deployment service PKs are used automatically.
	BootstrapPKs []cipher.PubKey `json:"bootstrap_pks,omitempty"`
	// FullNode stores all DHT items regardless of XOR distance (few needed).
	FullNode bool `json:"full_node,omitempty"`
	// WhitelistedPKs are publisher keys whose data is always replicated and never evicted.
	WhitelistedPKs []cipher.PubKey `json:"whitelisted_pks,omitempty"`
	// TrustedPKs are publisher keys that get full replication unless abuse is detected.
	TrustedPKs []cipher.PubKey `json:"trusted_pks,omitempty"`
	// PersistPath is the bbolt file for local persistence. Empty = in-memory only.
	// Default: "<local_path>/dht.db" (set automatically if omitted).
	PersistPath string `json:"persist_path,omitempty"`
	// RedisAddr enables Redis persistence (deployment services only).
	// Format: "host:port". Empty = no Redis.
	RedisAddr string `json:"redis_addr,omitempty"`
	// RedisPassword for authenticated Redis connections.
	RedisPassword string `json:"redis_password,omitempty"`
	// RedisDB is the Redis database number.
	RedisDB int `json:"redis_db,omitempty"`
}

// DmsgWebConfig enables the embedded `.dmsg` resolving proxy hosted by
// the visor. When Enable is true, the visor serves a SOCKS5 proxy on
// ProxyPort plus an HTTP bridge on WebPort; browsers pointed at the
// SOCKS5 proxy can load http://somekey.dmsg URLs directly. Reuses the
// visor's own dmsg client for the outbound fetches, so no extra dmsg
// identity or session load is incurred.
//
// This is the in-process version of the `skywire dmsg web` CLI, so
// the same domain-suffix resolving behavior is available without
// running a separate utility alongside the visor.
type DmsgWebConfig struct {
	// Enable must be true for the resolver to start.
	Enable bool `json:"enable"`
	// ProxyPort is the local SOCKS5 listener. Default 4445.
	ProxyPort uint `json:"proxy_port,omitempty"`
	// WebPort is the local HTTP bridge the SOCKS5 proxy rewrites
	// matched hosts to. Default 8080.
	WebPort uint `json:"web_port,omitempty"`
	// DomainSuffix is the TLD treated as DMSG addresses. Default ".dmsg".
	DomainSuffix string `json:"domain_suffix,omitempty"`
	// UpstreamSOCKS, if set, forwards non-matching CONNECTs to this
	// upstream SOCKS5 server (e.g. "127.0.0.1:1080" for a chained
	// skysocks-client). Empty = direct connect.
	UpstreamSOCKS string `json:"upstream_socks,omitempty"`
}

// SkynetWebConfig enables the embedded `.skynet` resolving proxy — the
// skywire-routing counterpart to DmsgWebConfig. Browsers pointed at
// the SOCKS5 listener can load http://<pk>.skynet[:<port>] URLs,
// which the visor serves by dialing a route to the remote visor's
// skynet server and piping bytes through. Reuses the visor's own
// router, so no additional routing setup is needed.
//
// Ports default to non-conflicting values with DmsgWebConfig so both
// resolvers can run simultaneously on a single visor.
type SkynetWebConfig struct {
	// Enable must be true for the resolver to start.
	Enable bool `json:"enable"`
	// ProxyPort is the local SOCKS5 listener. Default 4446.
	ProxyPort uint `json:"proxy_port,omitempty"`
	// WebPort is the local HTTP bridge port. Default 8081.
	WebPort uint `json:"web_port,omitempty"`
	// DomainSuffix is the TLD treated as skynet addresses. Default ".skynet".
	DomainSuffix string `json:"domain_suffix,omitempty"`
	// UpstreamSOCKS forwards non-matching CONNECTs to this upstream.
	UpstreamSOCKS string `json:"upstream_socks,omitempty"`
	// RouteTimeout is the keepalive duration for routes created by the
	// resolving proxy. Routes idle longer than this are expired by GC.
	// Zero means use DefaultRouteKeepAlive (10 min). A very large value
	// (e.g. "8760h") effectively keeps routes alive until the visor stops.
	RouteTimeout Duration `json:"route_timeout,omitempty"`
}

// Transport defines a transport config.
type Transport struct {
	Discovery             string          `json:"discovery"`
	DiscoveryDmsg         string          `json:"discovery_dmsg,omitempty"` // DMSG-HTTP URL for transport discovery (fallback pair with discovery)
	AddressResolver       string          `json:"address_resolver"`
	AddressResolverDmsg   string          `json:"address_resolver_dmsg,omitempty"` // DMSG-HTTP URL for address resolver
	PublicAutoconnect     bool            `json:"public_autoconnect"`
	TransportSetupPKs     []cipher.PubKey `json:"transport_setup"`
	UserTransportSetupPKs []cipher.PubKey `json:"user_transport_setup,omitempty"` // user-added keys, preserved across config refresh
	TPSetupSK             *cipher.SecKey  `json:"tps_sk,omitempty"`
	TPSDmsg               *TPSDmsgConfig  `json:"tps_dmsg,omitempty"`
	LogStore              *LogStore       `json:"log_store"`
	StcprPort             int             `json:"stcpr_port"`
	SudphPort             int             `json:"sudph_port"`
	// SyncTPDData enables syncing all transport discovery data on transport re-registration.
	// When enabled, the visor receives the full TPD dataset in the registration response
	// for use in local route calculation.
	SyncTPDData bool `json:"sync_tpd_data,omitempty"`
	// CXOFeedPK is the public key of the TPD's CXO feed for transport data.
	// When set and DMSG is available, the visor subscribes to the feed for
	// push-based transport updates instead of HTTP polling.
	CXOFeedPK string `json:"cxo_feed_pk,omitempty"`
	// ARTransportLimit controls address resolver registration for privacy:
	//   0 (default): stay registered indefinitely
	//   N > 0: deregister from AR after N transports are established
	//   N < 0: never register with AR at all
	// When deregistered, the visor cannot receive new inbound transports
	// but can still initiate outbound connections.
	ARTransportLimit int `json:"ar_transport_limit,omitempty"`
}

// TPSDmsgConfig configures the embedded Transport Setup Node's dmsg client.
// If nil, defaults are used: MinSessions=0 (connect to all servers), ServerType="" (all types).
type TPSDmsgConfig struct {
	// MinSessions is the minimum number of dmsg server sessions.
	// 0 means connect to ALL available servers (recommended for TPS).
	MinSessions int `json:"min_sessions"`
	// ServerType filters which dmsg servers to connect to: "official", "community", or "" for all.
	ServerType string `json:"server_type"`
}

// LogStore configures a LogStore.
type LogStore struct {
	// Type defines the log store type. Valid values: file, memory.
	Type             string   `json:"type"`
	Location         string   `json:"location"`
	RotationInterval Duration `json:"rotation_interval"` // time value, examples: 10s, 1m, 1h etc
}

// Routing configures routing.
type Routing struct {
	RouteSetupNodes     []cipher.PubKey `json:"route_setup_nodes,omitempty"`
	UserRouteSetupNodes []cipher.PubKey `json:"user_route_setup_nodes,omitempty"` // user-added keys, preserved across config refresh
	RouteSetupSK        *cipher.SecKey  `json:"route_setup_sk,omitempty"`         // Embedded route setup-node secret key
	RouteFinder         string          `json:"route_finder"`
	RouteFinderDmsg     string          `json:"route_finder_dmsg,omitempty"`
	RouteFinderTimeout  Duration        `json:"route_finder_timeout,omitempty"`
	MinHops             uint16          `json:"min_hops"`
	// CalculateRoutes enables local route calculation instead of using the route finder service.
	// When enabled, routes are calculated locally using cached TPD data.
	// Can be overridden at runtime with --use-rf flag.
	CalculateRoutes bool `json:"calculate_routes,omitempty"`
	// MuxRoutes is the number of parallel routes to establish per connection.
	// 0 or 1 = single route (default), >1 = route multiplexing across transports.
	MuxRoutes int `json:"mux_routes,omitempty"`
}

// UptimeTracker configures uptime tracker.
type UptimeTracker struct {
	Addr     string `json:"addr"`
	AddrDmsg string `json:"addr_dmsg,omitempty"`
}

// PublicVisorConfig configures public visor behavior and service discovery registration.
type PublicVisorConfig struct {
	// RegistrationTimeout is how long to wait for an external STCPR connection
	// before deregistering from service discovery. This validates the visor is
	// actually reachable from the internet (has port forwarding configured).
	// Set to 0 to skip this check (stay registered regardless).
	// Default: 10m
	RegistrationTimeout Duration `json:"registration_timeout,omitempty"`

	// MaxTransports is the maximum transport count before deregistering from
	// service discovery. Once reached, the visor has served its purpose of
	// bootstrapping other visors and deregisters to make room for others.
	// Set to 0 to never deregister based on transport count.
	// Default: 1000
	MaxTransports int `json:"max_transports,omitempty"`
}

// Launcher configures the app
type Launcher struct {
	ServiceDisc       string                `json:"service_discovery"`
	ServiceDiscDmsg   string                `json:"service_discovery_dmsg,omitempty"` // DMSG-HTTP URL for service discovery
	Apps              []appserver.AppConfig `json:"apps"`
	ServerAddr        string                `json:"server_addr"`
	BinPath           string                `json:"bin_path"`
	DisplayNodeIP     bool                  `json:"display_node_ip"`
	HeartbeatInterval Duration              `json:"heartbeat_interval,omitempty"`
}

// Flush flushes the config to file (if specified).
func (v1 *V1) Flush() error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	return v1.Common.flush(v1)
}

// UpdateAppAutostart modifies a single app's autostart value within the config and also the given
//
// The updated config gets flushed to file if there are any changes.
// func (v1 *V1) UpdateAppAutostart(appName string, autoStart bool) error {
func (v1 *V1) UpdateAppAutostart(launch *launcher.AppLauncher, appName string, autoStart bool) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()
	conf := v1.Launcher
	changed := false
	for i := range conf.Apps {
		if conf.Apps[i].Name == appName {
			conf.Apps[i].AutoStart = autoStart
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// UpdateAppArg updates the cli flag of the specified app config and also within the
// The updated config gets flushed to file if there are any changes.
// func (v1 *V1) UpdateAppArg(appName, argName string, value interface{}) error {
func (v1 *V1) UpdateAppArg(launch *launcher.AppLauncher, appName, argName string, value interface{}) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher

	var configChanged bool
	switch val := value.(type) {
	case string:
		configChanged = updateStringArg(conf, appName, argName, val)
	case bool:
		configChanged = updateBoolArg(conf, appName, argName, val)
	default:
		return fmt.Errorf("invalid arg type %T", value)
	}

	if !configChanged {
		return nil
	}
	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// UpdateAppArgBatch updates the cli flag of the specified app config and also within the
// The updated config gets flushed to file if there are any changes.
// func (v1 *V1) UpdateAppArg(appName, argName string, value interface{}) error {
func (v1 *V1) UpdateAppArgBatch(launch *launcher.AppLauncher, appName string, args map[string]any) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher

	var configChanged bool

	for arg, value := range args {
		switch val := value.(type) {
		case string:
			configChanged = updateStringArg(conf, appName, arg, val)
		case bool:
			configChanged = updateBoolArg(conf, appName, arg, val)
		default:
			return fmt.Errorf("invalid arg type %T", value)
		}
	}

	if !configChanged {
		return nil
	}
	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// UpdateAppPort update app port for communicat with visor
func (v1 *V1) UpdateAppPort(launch *launcher.AppLauncher, appName string, port uint16) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()
	requestPort := routing.Port(port)
	conf := v1.Launcher
	busyPorts := map[routing.Port]bool{}
	appExist := false
	for ind, app := range conf.Apps {
		busyPorts[app.Port] = true
		if app.Name == appName {
			appExist = true
			if requestPort == app.Port {
				break
			}
			if _, ok := busyPorts[requestPort]; ok && requestPort != app.Port {
				return fmt.Errorf("requested port is busy")
			}
			conf.Apps[ind].Port = requestPort
		}
	}
	if !appExist {
		return fmt.Errorf("app not available")
	}

	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// DeleteAppArg Delete entire of args of a custom app
func (v1 *V1) DeleteAppArg(launch *launcher.AppLauncher, appName string) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher

	configChanged := deleteAppArg(conf, appName)

	if !configChanged {
		return nil
	}
	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// UpdateMinHops updates min_hops config
func (v1 *V1) UpdateMinHops(hops uint16) error {
	v1.mu.Lock()
	v1.Routing.MinHops = hops
	v1.mu.Unlock()

	return v1.flush(v1)
}

// UpdatePersistentTransports updates persistent_transports in config
func (v1 *V1) UpdatePersistentTransports(pTps []transport.PersistentTransports) error {
	v1.mu.Lock()
	v1.PersistentTransports = pTps
	v1.mu.Unlock()

	return v1.flush(v1)
}

// GetPersistentTransports gets persistent_transports from config
func (v1 *V1) GetPersistentTransports() ([]transport.PersistentTransports, error) {
	v1.mu.Lock()
	defer v1.mu.Unlock()
	return v1.PersistentTransports, nil
}

// UpdateLogRotationInterval updates log_rotation_interval in config
func (v1 *V1) UpdateLogRotationInterval(d Duration) error {
	v1.mu.Lock()
	v1.Transport.LogStore.RotationInterval = d
	v1.mu.Unlock()

	return v1.flush(v1)
}

// GetLogRotationInterval gets log_rotation_interval from config
func (v1 *V1) GetLogRotationInterval() (Duration, error) {
	v1.mu.Lock()
	defer v1.mu.Unlock()
	return v1.Transport.LogStore.RotationInterval, nil
}

// UpdatePublicAutoconnect updates public_autoconnect in config
func (v1 *V1) UpdatePublicAutoconnect(pAc bool) error {
	v1.mu.Lock()
	v1.Transport.PublicAutoconnect = pAc
	v1.mu.Unlock()

	return v1.flush(v1)
}

// UpdateCalculateRoutes updates calculate_routes in routing config
func (v1 *V1) UpdateCalculateRoutes(enabled bool) error {
	v1.mu.Lock()
	v1.Routing.CalculateRoutes = enabled
	v1.mu.Unlock()

	return v1.flush(v1)
}

// GetCalculateRoutes gets calculate_routes from routing config
func (v1 *V1) GetCalculateRoutes() bool {
	v1.mu.RLock()
	defer v1.mu.RUnlock()
	return v1.Routing.CalculateRoutes
}

// UpdateSyncTPDData updates sync_tpd_data in transport config
func (v1 *V1) UpdateSyncTPDData(enabled bool) error {
	v1.mu.Lock()
	v1.Transport.SyncTPDData = enabled
	v1.mu.Unlock()

	return v1.flush(v1)
}

// GetSyncTPDData gets sync_tpd_data from transport config
func (v1 *V1) GetSyncTPDData() bool {
	v1.mu.RLock()
	defer v1.mu.RUnlock()
	return v1.Transport.SyncTPDData
}

// AddAppConfig add new config to apps if name was not same
func (v1 *V1) AddAppConfig(launch *launcher.AppLauncher, appName, binaryName string) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher
	busyPorts := map[routing.Port]bool{}
	for _, app := range conf.Apps {
		busyPorts[app.Port] = true
		if app.Name == appName {
			return fmt.Errorf("the app exist")
		}
	}
	var randomNumber int
	for {
		minn := 10
		maxx := 99
		randomNumber = rand.Intn(maxx-minn+1) + minn             //nolint: gosec
		if _, ok := busyPorts[routing.Port(randomNumber)]; !ok { //nolint: gosec
			break
		}
	}

	conf.Apps = append(conf.Apps, appserver.AppConfig{Name: appName, Binary: binaryName, Port: routing.Port(randomNumber)}) //nolint: gosec

	launch.ResetConfig(launcher.AppLauncherConfig{
		VisorPK:       v1.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		DisplayNodeIP: conf.DisplayNodeIP,
	})
	return v1.flush(v1)
}

// updateStringArg updates the cli non-boolean flag of the specified app config and also within the
// It removes argName from app args if value is an empty string.
// The updated config gets flushed to file if there are any changes.
func updateStringArg(conf *Launcher, appName, argName, value string) bool {
	configChanged := false

	for i := range conf.Apps {
		if conf.Apps[i].Name != appName {
			continue
		}

		configChanged = true

		argChanged := false
		l := len(conf.Apps[i].Args)
		for j := 0; j < l; j++ {
			equalArgName := conf.Apps[i].Args[j] == argName && j+1 < len(conf.Apps[i].Args)
			if !equalArgName {
				continue
			}

			if value == "" {
				conf.Apps[i].Args = append(conf.Apps[i].Args[:j], conf.Apps[i].Args[j+2:]...)
				j-- //nolint:ineffassign
			} else {
				conf.Apps[i].Args[j+1] = value
			}

			argChanged = true
			break
		}

		if !argChanged && value != "" {
			conf.Apps[i].Args = append(conf.Apps[i].Args, argName, value)
		}

		break
	}

	return configChanged
}

// updateBoolArg updates the cli boolean flag of the specified app config and also within the
// All flag names and values are formatted as "-name=value" to allow arbitrary values with respect to different
// possible default values.
// The updated config gets flushed to file if there are any changes.
func updateBoolArg(conf *Launcher, appName, argName string, value bool) bool {
	const argFmt = "%s=%v"

	configChanged := false

	for i := range conf.Apps {
		if conf.Apps[i].Name != appName {
			continue
		}

		// we format it to have a single dash, just to unify representation
		fmtedArgName := argName
		if argName[1] == '-' {
			fmtedArgName = fmtedArgName[1:]
		}

		arg := fmt.Sprintf(argFmt, fmtedArgName, value)

		configChanged = true

		argChanged := false
		for j := 0; j < len(conf.Apps[i].Args); j++ {
			// there shouldn't be such values if config is modified automatically,
			// but might happen if done manually, so we avoid further panic with this check
			if len(conf.Apps[i].Args[j]) < 2 {
				continue
			}

			equalArgName := conf.Apps[i].Args[j][1] != '-' && strings.HasPrefix(conf.Apps[i].Args[j], fmtedArgName)
			if conf.Apps[i].Args[j][1] == '-' {
				equalArgName = strings.HasPrefix(conf.Apps[i].Args[j], "-"+fmtedArgName)
			}

			if !equalArgName {
				continue
			}

			// check next value. currently we store value along with the flag name in a single string,
			// but there're may be some broken configs because of the previous functionality, so we
			// make our best effort to fix this on the go
			if (j + 1) < len(conf.Apps[i].Args) {
				// bool value shouldn't be present there, so we remove it, if it is
				if conf.Apps[i].Args[j+1] == "true" || conf.Apps[i].Args[j+1] == "false" {
					if (j + 2) < len(conf.Apps[i].Args) {
						conf.Apps[i].Args = append(conf.Apps[i].Args[:j+1], conf.Apps[i].Args[j+2:]...)
					} else {
						conf.Apps[i].Args = conf.Apps[i].Args[:j+1]
					}
				}
			}

			conf.Apps[i].Args[j] = arg
			argChanged = true

			break
		}

		if !argChanged {
			conf.Apps[i].Args = append(conf.Apps[i].Args, arg)
		}

		break
	}

	return configChanged
}

// deleteAppArg delete all args of an app by its name
func deleteAppArg(conf *Launcher, appName string) bool {
	var configChanged bool
	for i := range conf.Apps {
		if conf.Apps[i].Name != appName {
			continue
		}

		conf.Apps[i].Args = []string{}
		configChanged = true
		break
	}
	return configChanged
}

// mergePubKeys returns the union of the given public key slices, preserving
// the order of the first slice and appending any unique keys from the second.
// nil slices are handled transparently.
func mergePubKeys(primary, extra []cipher.PubKey) []cipher.PubKey {
	if len(extra) == 0 {
		return primary
	}
	if len(primary) == 0 {
		return extra
	}
	seen := make(map[cipher.PubKey]struct{}, len(primary)+len(extra))
	merged := make([]cipher.PubKey, 0, len(primary)+len(extra))
	for _, k := range primary {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		merged = append(merged, k)
	}
	for _, k := range extra {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		merged = append(merged, k)
	}
	return merged
}

// EffectiveRouteSetupNodes returns the union of deployment route setup nodes
// (managed by config refresh) and user-added route setup nodes.
func (v1 *V1) EffectiveRouteSetupNodes() []cipher.PubKey {
	if v1 == nil || v1.Routing == nil {
		return nil
	}
	return mergePubKeys(v1.Routing.RouteSetupNodes, v1.Routing.UserRouteSetupNodes)
}

// EffectiveTransportSetupPKs returns the union of deployment transport setup
// public keys (managed by config refresh) and user-added keys.
func (v1 *V1) EffectiveTransportSetupPKs() []cipher.PubKey {
	if v1 == nil || v1.Transport == nil {
		return nil
	}
	return mergePubKeys(v1.Transport.TransportSetupPKs, v1.Transport.UserTransportSetupPKs)
}

// EffectiveSurveyWhitelist returns the union of deployment survey whitelist
// keys (managed by config refresh) and user-added keys.
func (v1 *V1) EffectiveSurveyWhitelist() []cipher.PubKey {
	if v1 == nil {
		return nil
	}
	return mergePubKeys(v1.SurveyWhitelist, v1.UserSurveyWhitelist)
}

/*
// V100Name is the semantic version string for v1.0.0.
const V100Name = "v1.0.0"

// V101Name is the semantic version string for v1.0.1.
const V101Name = "v1.0.1"

// V110Name is the semantic version string for v1.1.0.
// Added MinHops field to V1Routing section of config
// Removed public_trusted_visor field from root section
// Removed trusted_visors field from transport section
// Added is_public field to root section
// Added public_autoconnect field to transport section
// Added transport_setup_nodes field to transport section
// Removed authorization_file field from dmsgpty section
// Default urls are changed to newer shortened ones
// Added stun_servers field to the config
// Added persistent_transports field to the config
// Changed proxy_discovery_addr field to service_discovery
// Changed V1AppDisc struct to V1ServiceDisc
// Changed stcp field to skywire-tcp
// Changed local_address field to listening_address
// Changed port field in dmsgpty to dmsg_port
// Added dmsghttp_path field to the config
const V110Name = "v1.1.0"

// V111Name is the semantic version string for v1.1.1.
// Added support for dmsghttp
// Added servers field in dmsg for dmsghttp
const V111Name = "v1.1.1"

// V1Name is the semantic version string for the most recent version of V1.
const V1Name = V111Name

//(0pcom)
//Version the config using the version of the program.
//Remove previous version parsing compatibility - visor no longer updates it's own config
// Config will be updated on new version via script provided with the installation
*/
