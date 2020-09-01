package visorconfig

import (
	"sync"

	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/snet"
	"github.com/skycoin/skywire/pkg/visor/hypervisorconfig"
)

//go:generate readmegen -n V1 -o ./README.md ./v1.go

// V1Name is the semantic version string for V1.
const V1Name = "v1.0.0"

// V1 is visor config v1.0.0
type V1 struct {
	*Common
	mu sync.RWMutex

	Dmsg          *snet.DmsgConfig `json:"dmsg"`
	Dmsgpty       *V1Dmsgpty       `json:"dmsgpty,omitempty"`
	STCP          *snet.STCPConfig `json:"stcp,omitempty"`
	Transport     *V1Transport     `json:"transport"`
	Routing       *V1Routing       `json:"routing"`
	UptimeTracker *V1UptimeTracker `json:"uptime_tracker,omitempty"`
	Launcher      *V1Launcher      `json:"launcher"`

	Hypervisors []cipher.PubKey `json:"hypervisors"`
	CLIAddr     string          `json:"cli_addr"`

	LogLevel          string   `json:"log_level"`
	ShutdownTimeout   Duration `json:"shutdown_timeout,omitempty"` // time value, examples: 10s, 1m, etc
	RestartCheckDelay string   `json:"restart_check_delay,omitempty"`

	PublicTrustedVisor bool `json:"public_trusted_visor,omitempty"`

	Hypervisor *hypervisorconfig.Config `json:"hypervisor,omitempty"`
}

// V1Dmsgpty configures the dmsgpty-host.
type V1Dmsgpty struct {
	Port     uint16 `json:"port"`
	AuthFile string `json:"authorization_file"`
	CLINet   string `json:"cli_network"`
	CLIAddr  string `json:"cli_address"`
}

// V1Transport defines a transport config.
type V1Transport struct {
	Discovery       string          `json:"discovery"`
	AddressResolver string          `json:"address_resolver"`
	LogStore        *V1LogStore     `json:"log_store"`
	TrustedVisors   []cipher.PubKey `json:"trusted_visors"`
}

// V1LogStore configures a LogStore.
type V1LogStore struct {
	// Type defines the log store type. Valid values: file, memory.
	Type     string `json:"type"`
	Location string `json:"location"`
}

// V1Routing configures routing.
type V1Routing struct {
	SetupNodes         []cipher.PubKey `json:"setup_nodes,omitempty"`
	RouteFinder        string          `json:"route_finder"`
	RouteFinderTimeout Duration        `json:"route_finder_timeout,omitempty"`
}

// V1UptimeTracker configures uptime tracker.
type V1UptimeTracker struct {
	Addr string `json:"addr"`
}

// V1AppDisc configures Skywire App Discovery Clients.
type V1AppDisc struct {
	UpdateInterval Duration `json:"update_interval,omitempty"`
	ServiceDisc    string   `json:"proxy_discovery_addr"` // TODO: change JSON name
}

// V1Launcher configures the app launcher.
type V1Launcher struct {
	Discovery  *V1AppDisc           `json:"discovery"`
	Apps       []launcher.AppConfig `json:"apps"`
	ServerAddr string               `json:"server_addr"`
	BinPath    string               `json:"bin_path"`
	LocalPath  string               `json:"local_path"`
}

// Flush flushes the config to file (if specified).
func (v1 *V1) Flush() error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	return v1.Common.flush(v1)
}

// UpdateAppAutostart modifies a single app's autostart value within the config and also the given launcher.
// The updated config gets flushed to file if there are any changes.
func (v1 *V1) UpdateAppAutostart(launch *launcher.Launcher, appName string, autoStart bool) error {
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

	launch.ResetConfig(launcher.Config{
		VisorPK:    v1.PK,
		Apps:       conf.Apps,
		ServerAddr: conf.ServerAddr,
	})
	return v1.flush(v1)
}

// UpdateAppArg updates the cli flag of the specified app config and also within the launcher.
// The updated config gets flushed to file if there are any changes.
func (v1 *V1) UpdateAppArg(launch *launcher.Launcher, appName, argName, value string) error {
	v1.mu.Lock()
	defer v1.mu.Unlock()

	conf := v1.Launcher

	configChanged := true
	for i := range conf.Apps {
		if conf.Apps[i].Name == appName {
			configChanged = true

			argChanged := false
			for j := range conf.Apps[i].Args {
				if conf.Apps[i].Args[j] == argName && j+1 < len(conf.Apps[i].Args) {
					conf.Apps[i].Args[j+1] = value
					argChanged = true
					break
				}
			}
			if !argChanged {
				conf.Apps[i].Args = append(conf.Apps[i].Args, argName, value)
			}
		}
	}

	if !configChanged {
		return nil
	}

	launch.ResetConfig(launcher.Config{
		VisorPK:    v1.PK,
		Apps:       conf.Apps,
		ServerAddr: conf.ServerAddr,
	})

	return v1.flush(v1)
}
