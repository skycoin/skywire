package visor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/dmsgpty"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/snet"
	"github.com/skycoin/skywire/pkg/transport"
	trClient "github.com/skycoin/skywire/pkg/transport-discovery/client"
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
	// DefaultSTCPPort ???
	// TODO: Define above or remove below.
	DefaultSTCPPort = 7777
)

var (
	// ErrNoConfigPath is returned on attempt to read/write config when visor contains no config path.
	ErrNoConfigPath = errors.New("no config path")
)

// Config defines configuration parameters for Visor.
type Config struct {
	Path    *string `json:"-"`
	log     *logging.Logger
	flushMu sync.Mutex

	Version       string               `json:"version"`
	KeyPair       *KeyPair             `json:"key_pair"`
	Dmsg          *snet.DmsgConfig     `json:"dmsg"`
	DmsgPty       *DmsgPtyConfig       `json:"dmsg_pty,omitempty"`
	STCP          *snet.STCPConfig     `json:"stcp,omitempty"`
	Transport     *TransportConfig     `json:"transport"`
	Routing       *RoutingConfig       `json:"routing"`
	UptimeTracker *UptimeTrackerConfig `json:"uptime_tracker,omitempty"`

	Apps []AppConfig `json:"apps"`

	TrustedVisors []cipher.PubKey    `json:"trusted_visors"`
	Hypervisors   []HypervisorConfig `json:"hypervisors"`

	AppsPath  string `json:"apps_path"`
	LocalPath string `json:"local_path"`

	LogLevel        string   `json:"log_level"`
	ShutdownTimeout Duration `json:"shutdown_timeout,omitempty"` // time value, examples: 10s, 1m, etc

	Interfaces *InterfaceConfig `json:"interfaces"`

	AppServerAddr string `json:"app_server_addr"`

	RestartCheckDelay string `json:"restart_check_delay,omitempty"`
}

// Flush flushes config to file.
func (c *Config) flush() error {
	c.flushMu.Lock()
	defer c.flushMu.Unlock()

	if c.Path == nil {
		return ErrNoConfigPath
	}

	c.log.Infof("Updating visor config to %#v", c)

	bytes, err := json.MarshalIndent(c, "", "\t")
	if err != nil {
		return err
	}

	const filePerm = 0644
	return ioutil.WriteFile(*c.Path, bytes, filePerm)
}

// Keys returns visor public and secret keys extracted from config.
// If they are not found, new keys are generated.
func (c *Config) Keys() *KeyPair {
	// If both keys are set, no additional action is needed.
	if c.KeyPair != nil && !c.KeyPair.SecKey.Null() && !c.KeyPair.PubKey.Null() {
		return c.KeyPair
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

	if err := c.flush(); err != nil && c.log != nil {
		c.log.WithError(err).Errorf("Failed to flush config to disk")
	}

	return c.KeyPair
}

// DmsgConfig extracts and returns DmsgConfig from Visor Config.
// If it is not found, it sets DefaultDmsgConfig() as RoutingConfig and returns it.
func (c *Config) DmsgConfig() *snet.DmsgConfig {
	if c.Dmsg == nil {
		c.Dmsg = DefaultDmsgConfig()
		if err := c.flush(); err != nil && c.log != nil {
			c.log.WithError(err).Errorf("Failed to flush config to disk")
		}
	}

	return c.Dmsg
}

// DmsgPtyHost extracts DmsgPtyConfig and returns *dmsgpty.Host based on the config.
// If DmsgPtyConfig is not found, DefaultDmsgPtyConfig() is used.
func (c *Config) DmsgPtyHost(dmsgC *dmsg.Client) (*dmsgpty.Host, error) {
	if c.DmsgPty == nil {
		c.DmsgPty = DefaultDmsgPtyConfig()
		if err := c.flush(); err != nil && c.log != nil {
			c.log.WithError(err).Errorf("Failed to flush config to disk")
		}
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
// If TransportConfig is not found, DefaultTransportConfig() is used.
func (c *Config) TransportDiscovery() (transport.DiscoveryClient, error) {
	if c.Transport == nil {
		c.Transport = DefaultTransportConfig()
		if err := c.flush(); err != nil && c.log != nil {
			c.log.WithError(err).Errorf("Failed to flush config to disk")
		}
	}

	return trClient.NewHTTP(c.Transport.Discovery, c.Keys().PubKey, c.Keys().SecKey)
}

// TransportLogStore extracts LogStoreConfig and returns transport.LogStore based on the config.
// If LogStoreConfig is not found, DefaultLogStoreConfig() is used.
func (c *Config) TransportLogStore() (transport.LogStore, error) {
	if c.Transport == nil {
		c.Transport = DefaultTransportConfig()
		if err := c.flush(); err != nil && c.log != nil {
			c.log.WithError(err).Errorf("Failed to flush config to disk")
		}
	} else if c.Transport.LogStore == nil {
		c.Transport.LogStore = DefaultLogStoreConfig()
		if err := c.flush(); err != nil && c.log != nil {
			c.log.WithError(err).Errorf("Failed to flush config to disk")
		}
	}

	if c.Transport.LogStore.Type == LogStoreFile {
		return transport.FileTransportLogStore(c.Transport.LogStore.Location)
	}

	return transport.InMemoryTransportLogStore(), nil
}

// RoutingConfig extracts and returns RoutingConfig from Visor Config.
// If it is not found, it sets DefaultRoutingConfig() as RoutingConfig and returns it.
func (c *Config) RoutingConfig() *RoutingConfig {
	if c.Routing == nil {
		c.Routing = DefaultRoutingConfig()
		if err := c.flush(); err != nil && c.log != nil {
			c.log.WithError(err).Errorf("Failed to flush config to disk")
		}
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
		if err := c.flush(); err != nil && c.log != nil {
			c.log.WithError(err).Errorf("Failed to flush config to disk")
		}
	}

	return ensureDir(c.AppsPath)
}

// LocalDir returns absolute path for app work directory.
// Directory will be created if necessary.
// If it is not set in config, DefaultLocalPath is used.
func (c *Config) LocalDir() (string, error) {
	if c.LocalPath == "" {
		c.LocalPath = DefaultLocalPath
		if err := c.flush(); err != nil && c.log != nil {
			c.log.WithError(err).Errorf("Failed to flush config to disk")
		}
	}

	return ensureDir(c.LocalPath)
}

// AppServerAddress extracts and returns AppServerAddr from Visor Config.
// If it is not found, it sets appcommon.DefaultServerAddr as AppServerAddr and returns it.
func (c *Config) AppServerAddress() string {
	if c.AppServerAddr == "" {
		c.AppServerAddr = appcommon.DefaultServerAddr
		if err := c.flush(); err != nil && c.log != nil {
			c.log.WithError(err).Errorf("Failed to flush config to disk")
		}
	}

	return c.AppServerAddr
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
	SetupNodes         []cipher.PubKey `json:"setup_nodes,omitempty"`
	RouteFinder        string          `json:"route_finder"`
	RouteFinderTimeout Duration        `json:"route_finder_timeout,omitempty"`
}

// DefaultRoutingConfig returns default routing config.
func DefaultRoutingConfig() *RoutingConfig {
	return &RoutingConfig{
		SetupNodes:         []cipher.PubKey{skyenv.MustPK(skyenv.DefaultSetupPK)},
		RouteFinder:        skyenv.DefaultRouteFinderAddr,
		RouteFinderTimeout: DefaultTimeout,
	}
}

// UptimeTrackerConfig configures uptime tracker.
type UptimeTrackerConfig struct {
	Addr string `json:"addr"`
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
	Args      []string     `json:"args,omitempty"`
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
