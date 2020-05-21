package visor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"runtime"
	"sync"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/launcher"
	"github.com/SkycoinProject/skywire-mainnet/pkg/restart"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
)

//go:generate readmegen -n Config -o ./README.md ./config.go

const (
	// DefaultTimeout is used for default config generation and if it is not set in config.
	// TODO: Put this in skyenv.
	DefaultTimeout = Duration(10 * time.Second)

	// ConfigVersion of the visor config.
	// TODO: Put this in skyenv?
	ConfigVersion = "v1.0.0"

	// StdinName is the path name used to identify STDIN.
	StdinName = "STDIN"
)

var (
	// ErrNoConfigPath is returned on attempt to read/write config when visor contains no config path.
	ErrNoConfigPath = errors.New("no config path")
)

// Config defines configuration parameters for Visor.
type Config struct {
	path string
	log  *logging.MasterLogger
	mu   sync.RWMutex

	Version       string               `json:"version"`
	KeyPair       *KeyPair             `json:"key_pair"`
	Dmsg          *snet.DmsgConfig     `json:"dmsg"`
	Dmsgpty       *DmsgptyConfig       `json:"dmsgpty,omitempty"`
	STCP          *snet.STCPConfig     `json:"stcp,omitempty"`
	Transport     *TransportConfig     `json:"transport"`
	Routing       *RoutingConfig       `json:"routing"`
	UptimeTracker *UptimeTrackerConfig `json:"uptime_tracker,omitempty"`
	Launcher      *LauncherConfig      `json:"launcher"`

	Hypervisors []HypervisorConfig `json:"hypervisors"`
	CLIAddr     string             `json:"cli_addr"`

	LogLevel          string   `json:"log_level"`
	ShutdownTimeout   Duration `json:"shutdown_timeout,omitempty"` // time value, examples: 10s, 1m, etc
	RestartCheckDelay string   `json:"restart_check_delay,omitempty"`
}

// Flush flushes config to file.
func (c *Config) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.flush()
}

func (c *Config) flush() error {
	switch c.path {
	case "":
		return ErrNoConfigPath
	case StdinName:
		return nil
	}

	j, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	c.log.Debugf("Updating visor config to: %s", string(j))

	bytes, err := json.MarshalIndent(c, "", "\t")
	if err != nil {
		return err
	}
	const filePerm = 0644
	return ioutil.WriteFile(c.path, bytes, filePerm)
}

// BaseConfig returns a visor config with 'enforced' fields only.
// This is used as default values if no config is given, or for missing *required* fields.
func BaseConfig(log *logging.MasterLogger, configPath string) *Config {
	if log == nil {
		log = logging.NewMasterLogger()
	}
	conf := &Config{
		path:    configPath,
		log:     log,
		Version: ConfigVersion,
		Dmsg: &snet.DmsgConfig{
			Discovery:     skyenv.DefaultDmsgDiscAddr,
			SessionsCount: 1,
		},
		Transport: &TransportConfig{
			Discovery: skyenv.DefaultTpDiscAddr,
			LogStore: &LogStoreConfig{
				Type: LogStoreMemory,
			},
		},
		Routing: &RoutingConfig{
			SetupNodes:         []cipher.PubKey{skyenv.MustPK(skyenv.DefaultSetupPK)},
			RouteFinder:        skyenv.DefaultRouteFinderAddr,
			RouteFinderTimeout: DefaultTimeout,
		},
		Launcher: &LauncherConfig{
			Discovery:  nil,
			Apps:       nil,
			ServerAddr: skyenv.DefaultAppSrvAddr,
			BinPath:    skyenv.DefaultAppBinPath,
			LocalPath:  skyenv.DefaultAppLocalPath,
		},
		CLIAddr:           skyenv.DefaultRPCAddr,
		LogLevel:          skyenv.DefaultLogLevel,
		ShutdownTimeout:   DefaultTimeout,
		RestartCheckDelay: restart.DefaultCheckDelay.String(), // TODO: Use Duration type.
	}
	return conf
}

// DefaultConfig returns the default visor config from a given key pair (if specified).
// The config's key_pair field will be nil if not specified.
// Generated config will be saved to 'configPath'
func DefaultConfig(log *logging.MasterLogger, configPath string, keys *KeyPair) (*Config, error) {
	conf := BaseConfig(log, configPath)
	// disallow dmsgpty for Windows systems
	if runtime.GOOS != "windows" {
		conf.Dmsgpty = &DmsgptyConfig{
			Port:     skyenv.DmsgPtyPort,
			AuthFile: skyenv.DefaultDmsgPtyWhitelist,
			CLINet:   skyenv.DefaultDmsgPtyCLINet,
			CLIAddr:  skyenv.DefaultDmsgPtyCLIAddr,
		}
	}
	conf.STCP = &snet.STCPConfig{
		LocalAddr: skyenv.DefaultSTCPAddr,
		PKTable:   nil,
	}
	conf.Transport.LogStore = &LogStoreConfig{
		Type:     LogStoreFile,
		Location: skyenv.DefaultTpLogStore,
	}
	conf.UptimeTracker = &UptimeTrackerConfig{
		Addr: skyenv.DefaultUptimeTrackerAddr,
	}
	conf.Launcher.Discovery = &AppDiscConfig{
		UpdateInterval: Duration(skyenv.AppDiscUpdateInterval),
		ProxyDisc:      skyenv.DefaultProxyDiscAddr,
	}
	conf.Launcher.Apps = []launcher.AppConfig{
		{
			Name:      skyenv.SkychatName,
			AutoStart: true,
			Port:      routing.Port(skyenv.SkychatPort),
			Args:      []string{"-addr", skyenv.SkychatAddr},
		},
		{
			Name:      skyenv.SkysocksName,
			AutoStart: true,
			Port:      routing.Port(skyenv.SkysocksPort),
		},
		{
			Name:      skyenv.SkysocksClientName,
			AutoStart: false,
			Port:      routing.Port(skyenv.SkysocksClientPort),
		},
		{
			Name:      skyenv.VPNServerName,
			AutoStart: true,
			Port:      routing.Port(skyenv.VPNServerPort),
		},
		{
			Name:      skyenv.VPNClientName,
			AutoStart: false,
			Port:      routing.Port(skyenv.VPNClientPort),
		},
	}
	if keys != nil {
		conf.KeyPair = keys
		conf.ensureKeys()
	}
	return conf, conf.Flush()
}

// UpdateAppAutostart modifies a single app's autostart value within the config and also the given launcher.
// The updated config gets flushed to file if there are any changes.
func (c *Config) UpdateAppAutostart(launch *launcher.Launcher, appName string, autoStart bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	conf := c.Launcher

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
		VisorPK:    c.KeyPair.PubKey,
		Apps:       conf.Apps,
		ServerAddr: conf.ServerAddr,
		BinPath:    conf.BinPath,
		LocalPath:  conf.LocalPath,
	})
	return c.flush()
}

// UpdateAppArg updates the cli flag of the specified app config and also within the launcher.
// The updated config gets flushed to file if there are any changes.
func (c *Config) UpdateAppArg(launch *launcher.Launcher, appName, argName, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	conf := c.Launcher

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
		VisorPK:    c.KeyPair.PubKey,
		Apps:       conf.Apps,
		ServerAddr: conf.ServerAddr,
		BinPath:    conf.BinPath,
		LocalPath:  conf.LocalPath,
	})
	return c.flush()
}

// Keys returns visor public and secret keys extracted from config.
// If they are not found, new keys are generated.
func (c *Config) Keys() *KeyPair {
	c.mu.Lock()
	defer c.mu.Unlock()

	if changed := c.ensureKeys(); !changed {
		return c.KeyPair
	}
	if err := c.flush(); err != nil && c.log != nil {
		c.log.WithError(err).Errorf("Failed to Flush config to disk")
	}
	return c.KeyPair
}

func (c *Config) ensureKeys() (changed bool) {
	// If both keys are set, no additional action is needed.
	if c.KeyPair != nil && !c.KeyPair.SecKey.Null() && !c.KeyPair.PubKey.Null() {
		return false
	}

	// If either no keys are set or SecKey is not set, a new key pair is generated.
	if c.KeyPair == nil || c.KeyPair.SecKey.Null() {
		c.KeyPair = NewKeyPair()
	}

	// If SecKey is set and PubKey is not set, PubKey can be generated from SecKey.
	if !c.KeyPair.SecKey.Null() && c.KeyPair.PubKey.Null() {
		pk, err := c.KeyPair.SecKey.PubKey()
		if err != nil {
			// If generation of PubKey from SecKey fails, a new key pair is generated.
			c.KeyPair = NewKeyPair()
		} else {
			c.KeyPair.PubKey = pk
		}
	}

	return true
}

// KeyPair defines Visor public and secret key pair.
type KeyPair struct {
	PubKey cipher.PubKey `json:"public_key"`
	SecKey cipher.SecKey `json:"secret_key"`
}

// NewKeyPair returns a new public and secret key pair.
func NewKeyPair() *KeyPair {
	pk, sk := cipher.GenerateKeyPair()

	return &KeyPair{
		PubKey: pk,
		SecKey: sk,
	}
}

// RestoreKeyPair generates a key pair using just the secret key.
func RestoreKeyPair(sk cipher.SecKey) *KeyPair {
	pk, err := sk.PubKey()
	if err != nil {
		panic(fmt.Errorf("failed to restore key pair: %v", err))
	}
	return &KeyPair{PubKey: pk, SecKey: sk}
}

// DmsgptyConfig configures the dmsgpty-host.
type DmsgptyConfig struct {
	Port     uint16 `json:"port"`
	AuthFile string `json:"authorization_file"`
	CLINet   string `json:"cli_network"`
	CLIAddr  string `json:"cli_address"`
}

// TransportConfig defines a transport config.
type TransportConfig struct {
	Discovery     string          `json:"discovery"`
	LogStore      *LogStoreConfig `json:"log_store"`
	TrustedVisors []cipher.PubKey `json:"trusted_visors"`
}

// LogStoreType defines a type for LogStore. It may be either file or memory.
type LogStoreType string

const (
	// LogStoreFile tells LogStore to use a file for storage.
	LogStoreFile = "file"
	// LogStoreMemory tells LogStore to use memory for storage.
	LogStoreMemory = "memory"
)

// LogStoreConfig configures a LogStore.
type LogStoreConfig struct {
	Type     LogStoreType `json:"type"`
	Location string       `json:"location"`
}

// RoutingConfig configures routing.
type RoutingConfig struct {
	SetupNodes         []cipher.PubKey `json:"setup_nodes,omitempty"`
	RouteFinder        string          `json:"route_finder"`
	RouteFinderTimeout Duration        `json:"route_finder_timeout,omitempty"`
}

// UptimeTrackerConfig configures uptime tracker.
type UptimeTrackerConfig struct {
	Addr string `json:"addr"`
}

// AppDiscConfig configures Skywire App Discovery Clients.
type AppDiscConfig struct {
	UpdateInterval Duration `json:"update_interval,omitempty"`
	ProxyDisc      string   `json:"proxy_discovery_addr"`
}

// LauncherConfig configures the app launcher.
type LauncherConfig struct {
	Discovery  *AppDiscConfig       `json:"discovery"`
	Apps       []launcher.AppConfig `json:"apps"`
	ServerAddr string               `json:"server_addr"`
	BinPath    string               `json:"bin_path"`
	LocalPath  string               `json:"local_path"`
}

// HypervisorConfig represents hypervisor configuration.
type HypervisorConfig struct {
	PubKey cipher.PubKey `json:"public_key"`
}

// InterfaceConfig defines listening interfaces for skywire visor.
type InterfaceConfig struct {
	RPCAddress string `json:"rpc"` // RPC address and port for command-line interface (leave blank to disable RPC interface).
}
