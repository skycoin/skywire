package visor

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/dmsgpty"

	"github.com/SkycoinProject/skywire-mainnet/internal/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
	trClient "github.com/SkycoinProject/skywire-mainnet/pkg/transport-discovery/client"
)

const (
	// DefaultTimeout is used for default config generation and if it is not set in config.
	DefaultTimeout = Duration(10 * time.Second)
	// DefaultLocalPath is used for default config generation and if it is not set in config.
	DefaultLocalPath = "./local"
	// DefaultAppsPath is used for default config generation and if it is not set in config.
	DefaultAppsPath = "./apps"
	// DefaultLogLevel is used for default config generation and if it is not set in config.
	DefaultLogLevel = "info"
	// DefaultSTCPPort ...
	// TODO: Define above or remove below.
	DefaultSTCPPort = 7777
)

// Config defines configuration parameters for Visor.
type Config struct {
	Visor         *KeyPair             `json:"visor"`
	STCP          *snet.STCPConfig     `json:"stcp"`
	Dmsg          *snet.DmsgConfig     `json:"dmsg"`
	DmsgPty       *DmsgPtyConfig       `json:"dmsg_pty,omitempty"`
	Transport     *TransportConfig     `json:"transport"`
	Routing       *RoutingConfig       `json:"routing"`
	UptimeTracker *UptimeTrackerConfig `json:"uptime"` // TODO: Rename it in JSON at some point.

	Apps []AppConfig `json:"apps"`

	TrustedVisors []cipher.PubKey    `json:"trusted_visors"`
	Hypervisors   []HypervisorConfig `json:"hypervisors"`

	AppsPath  string `json:"apps_path"`
	LocalPath string `json:"local_path"`

	LogLevel        string   `json:"log_level"`
	ShutdownTimeout Duration `json:"shutdown_timeout,omitempty"` // time value, examples: 10s, 1m, etc

	Interfaces *InterfaceConfig `json:"interfaces"`

	AppServerSockFile string `json:"app_server_sock_file"`
	RestartCheckDelay string `json:"restart_check_delay,omitempty"`
}

// Keys returns visor public and secret keys extracted from config.
// If they are not found, new keys are generated.
func (c *Config) Keys() *KeyPair {
	if c.Visor == nil || c.Visor.StaticPubKey.Null() || c.Visor.StaticSecKey.Null() {
		c.Visor = NewKeyPair()
	}

	return c.Visor
}

// DmsgPtyHost extracts DmsgPtyConfig and returns *dmsgpty.Host based on the config.
// If DmsgPtyConfig is not found, DefaultDmsgPtyConfig is used.
func (c *Config) DmsgPtyHost(dmsgC *dmsg.Client) (*dmsgpty.Host, error) {
	if c.DmsgPty == nil {
		c.DmsgPty = DefaultDmsgPtyConfig()
	}

	var wl dmsgpty.Whitelist
	if c.DmsgPty.AuthFile == "" {
		wl = dmsgpty.NewMemoryWhitelist()
	} else {
		var err error
		if wl, err = dmsgpty.NewJSONFileWhiteList(c.DmsgPty.AuthFile); err != nil {
			return nil, err
		}
	}

	// Whitelist hypervisor PKs.
	hypervisorWL := dmsgpty.NewMemoryWhitelist()
	for _, hv := range c.Hypervisors {
		if err := hypervisorWL.Add(hv.PubKey); err != nil {
			return nil, fmt.Errorf("failed to add hypervisor PK to whitelist: %v", err)
		}
	}

	host := dmsgpty.NewHost(dmsgC, dmsgpty.NewCombinedWhitelist(0, wl, hypervisorWL))
	return host, nil
}

// TransportDiscovery extracts TransportConfig and returns transport.DiscoveryClient based on the config.
// If TransportConfig is not found, DefaultTransportConfig is used.
func (c *Config) TransportDiscovery() (transport.DiscoveryClient, error) {
	if c.Transport == nil {
		c.Transport = DefaultTransportConfig()
	}

	return trClient.NewHTTP(c.Transport.Discovery, c.Keys().StaticPubKey, c.Keys().StaticSecKey)
}

// TransportLogStore extracts LogStoreConfig and returns transport.LogStore based on the config.
// If LogStoreConfig is not found, DefaultLogStoreConfig is used.
func (c *Config) TransportLogStore() (transport.LogStore, error) {
	if c.Transport == nil {
		c.Transport = DefaultTransportConfig()
	} else if c.Transport.LogStore == nil {
		c.Transport.LogStore = DefaultLogStoreConfig()
	}

	if c.Transport.LogStore.Type == LogStoreFile {
		return transport.FileTransportLogStore(c.Transport.LogStore.Location)
	}

	return transport.InMemoryTransportLogStore(), nil
}

// RoutingConfig extracts and returns RoutingConfig from Visor Config.
// If it is not found, it sets DefaultRoutingConfig as RoutingConfig and returns it.
func (c *Config) RoutingConfig() *RoutingConfig {
	if c.Routing == nil {
		c.Routing = DefaultRoutingConfig()
	}

	return c.Routing
}

// AppsConfig decodes AppsConfig from a local json config file.
func (c *Config) AppsConfig() (map[string]AppConfig, error) {
	apps := make(map[string]AppConfig)
	for _, app := range c.Apps {
		apps[app.App] = app
	}

	return apps, nil
}

// AppsDir returns absolute path for directory with application binaries.
// Directory will be created if necessary.
// If it is not set in config, DefaultAppsPath is used.
func (c *Config) AppsDir() (string, error) {
	if c.AppsPath == "" {
		c.AppsPath = DefaultAppsPath
	}

	return ensureDir(c.AppsPath)
}

// LocalDir returns absolute path for app work directory.
// Directory will be created if necessary.
// If it is not set in config, DefaultLocalPath is used.
func (c *Config) LocalDir() (string, error) {
	if c.LocalPath == "" {
		c.LocalPath = DefaultLocalPath
	}

	return ensureDir(c.LocalPath)
}

func ensureDir(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("failed to expand path: %s", err)
	}

	if _, err := os.Stat(absPath); !os.IsNotExist(err) {
		return absPath, nil
	}

	if err := os.MkdirAll(absPath, 0750); err != nil {
		return "", fmt.Errorf("failed to create dir: %s", err)
	}

	return absPath, nil
}

// KeyPair defines Visor public and secret key pair.
type KeyPair struct {
	StaticPubKey cipher.PubKey `json:"static_public_key"`
	StaticSecKey cipher.SecKey `json:"static_secret_key"`
}

// NewKeyPair returns a new public and secret key pair.
func NewKeyPair() *KeyPair {
	pk, sk := cipher.GenerateKeyPair()

	return &KeyPair{
		StaticPubKey: pk,
		StaticSecKey: sk,
	}
}

// DefaultSTCPConfig returns default STCP config.
func DefaultSTCPConfig() (*snet.STCPConfig, error) {
	lIPaddr, err := getLocalIPAddress()
	if err != nil {
		return nil, err
	}

	c := &snet.STCPConfig{
		LocalAddr: lIPaddr,
	}

	return c, nil
}

// DefaultDmsgConfig returns default Dmsg config.
func DefaultDmsgConfig() *snet.DmsgConfig {
	return &snet.DmsgConfig{
		Discovery:     skyenv.DefaultDmsgDiscAddr,
		SessionsCount: 1,
	}
}

// DmsgPtyConfig configures the dmsgpty-host.
type DmsgPtyConfig struct {
	Port     uint16 `json:"port"`
	AuthFile string `json:"authorization_file"`
	CLINet   string `json:"cli_network"`
	CLIAddr  string `json:"cli_address"`
}

// DefaultDmsgPtyConfig returns default DmsgPty config.
func DefaultDmsgPtyConfig() *DmsgPtyConfig {
	return &DmsgPtyConfig{
		Port:     skyenv.DmsgPtyPort,
		AuthFile: "./skywire/dmsgpty/whitelist.json",
		CLINet:   skyenv.DefaultDmsgPtyCLINet,
		CLIAddr:  skyenv.DefaultDmsgPtyCLIAddr,
	}
}

// TransportConfig defines a transport config.
type TransportConfig struct {
	Discovery string          `json:"discovery"`
	LogStore  *LogStoreConfig `json:"log_store"`
}

// DefaultTransportConfig returns default transport config.
func DefaultTransportConfig() *TransportConfig {
	return &TransportConfig{
		Discovery: skyenv.DefaultTpDiscAddr,
		LogStore:  DefaultLogStoreConfig(),
	}
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

// DefaultLogStoreConfig returns default LogStore config.
func DefaultLogStoreConfig() *LogStoreConfig {
	return &LogStoreConfig{
		Type:     LogStoreFile,
		Location: "./skywire/transport_logs",
	}
}

// RoutingConfig configures routing.
type RoutingConfig struct {
	SetupNodes         []cipher.PubKey `json:"setup_nodes"`
	RouteFinder        string          `json:"route_finder"`
	RouteFinderTimeout Duration        `json:"route_finder_timeout,omitempty"`
}

// DefaultRoutingConfig returns default routing config.
func DefaultRoutingConfig() *RoutingConfig {
	return &RoutingConfig{
		SetupNodes:         []cipher.PubKey{skyenv.MustDefaultSetupPK()},
		RouteFinder:        skyenv.DefaultRouteFinderAddr,
		RouteFinderTimeout: DefaultTimeout,
	}
}

// UptimeTrackerConfig configures uptime tracker.
type UptimeTrackerConfig struct {
	Addr string `json:"tracker"` // TODO: Rename it in JSON at some point.
}

// DefaultUptimeTrackerConfig returns default uptime tracker config.
func DefaultUptimeTrackerConfig() *UptimeTrackerConfig {
	return &UptimeTrackerConfig{
		Addr: skyenv.DefaultUptimeTrackerAddr,
	}
}

// HypervisorConfig represents hypervisor configuration.
type HypervisorConfig struct {
	PubKey cipher.PubKey `json:"public_key"`
	Addr   string        `json:"address"`
}

// AppConfig defines app startup parameters.
type AppConfig struct {
	App       string       `json:"app"`
	AutoStart bool         `json:"auto_start"`
	Port      routing.Port `json:"port"`
	Args      []string     `json:"args"`
}

// InterfaceConfig defines listening interfaces for skywire visor.
type InterfaceConfig struct {
	RPCAddress string `json:"rpc"` // RPC address and port for command-line interface (leave blank to disable RPC interface).
}

// DefaultInterfaceConfig returns default server interface config.
func DefaultInterfaceConfig() *InterfaceConfig {
	return &InterfaceConfig{
		RPCAddress: "localhost:3435",
	}
}

func getLocalIPAddress() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}

	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return fmt.Sprintf("%s:%d", ipnet.IP.String(), DefaultSTCPPort), nil
			}
		}
	}
	return "", errors.New("could not find local IP address")
}
