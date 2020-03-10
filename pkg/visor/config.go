package visor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/SkycoinProject/dmsg/dmsgpty"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
	trClient "github.com/SkycoinProject/skywire-mainnet/pkg/transport-discovery/client"
)

// Config defines configuration parameters for Visor.
// TODO(evanlinjin): Instead of having nested structs, make separate types for each field.
// TODO(evanlinjin): Use pointers to allow nil-configs for non-crucial fields.
type Config struct {
	Version string `json:"version"`

	Visor struct {
		StaticPubKey cipher.PubKey `json:"static_public_key"`
		StaticSecKey cipher.SecKey `json:"static_secret_key"`
	} `json:"visor"`

	STCP struct {
		PubKeyTable map[cipher.PubKey]string `json:"pk_table"`
		LocalAddr   string                   `json:"local_address"`
	} `json:"stcp"`

	Dmsg struct {
		Discovery     string `json:"discovery"`
		SessionsCount int    `json:"sessions_count"`
	} `json:"dmsg"`

	DmsgPty *DmsgPtyConfig `json:"dmsg_pty,omitempty"`

	Transport struct {
		Discovery string `json:"discovery"`
		LogStore  struct {
			Type     string `json:"type"`
			Location string `json:"location"`
		} `json:"log_store"`
	} `json:"transport"`

	Routing struct {
		SetupNodes         []cipher.PubKey `json:"setup_nodes"`
		RouteFinder        string          `json:"route_finder"`
		RouteFinderTimeout Duration        `json:"route_finder_timeout,omitempty"`
	} `json:"routing"`

	Uptime struct {
		Tracker string `json:"tracker"`
	} `json:"uptime"`

	Apps []AppConfig `json:"apps"`

	TrustedVisors []cipher.PubKey    `json:"trusted_visors"`
	Hypervisors   []HypervisorConfig `json:"hypervisors"`

	AppsPath  string `json:"apps_path"`
	LocalPath string `json:"local_path"`

	LogLevel        string   `json:"log_level"`
	ShutdownTimeout Duration `json:"shutdown_timeout,omitempty"` // time value, examples: 10s, 1m, etc

	Interfaces InterfaceConfig `json:"interfaces"`

	AppServerSockFile string `json:"app_server_sock_file"`

	RestartCheckDelay string `json:"restart_check_delay,omitempty"`
}

// DmsgConfig returns config for dmsg client.
func (c *Config) DmsgConfig() (*DmsgConfig, error) {
	dmsgConfig := c.Dmsg

	if dmsgConfig.Discovery == "" {
		return nil, errors.New("empty discovery")
	}

	return &DmsgConfig{
		PubKey:     c.Visor.StaticPubKey,
		SecKey:     c.Visor.StaticSecKey,
		Discovery:  disc.NewHTTP(dmsgConfig.Discovery),
		Retries:    5,
		RetryDelay: time.Second,
	}, nil
}

// DmsgPtyHost instantiates a host from the dmsgpty config.
func (c *Config) DmsgPtyHost(dmsgC *dmsg.Client) (*dmsgpty.Host, error) {
	if c.DmsgPty == nil {
		return nil, errors.New("'dmsg_pty' config field not defined")
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

// TransportDiscovery returns transport discovery client.
func (c *Config) TransportDiscovery() (transport.DiscoveryClient, error) {
	if c.Transport.Discovery == "" {
		return nil, errors.New("empty transport_discovery")
	}

	return trClient.NewHTTP(c.Transport.Discovery, c.Visor.StaticPubKey, c.Visor.StaticSecKey)
}

// TransportLogStore returns configure transport.LogStore.
func (c *Config) TransportLogStore() (transport.LogStore, error) {
	if c.Transport.LogStore.Type == "file" {
		return transport.FileTransportLogStore(c.Transport.LogStore.Location)
	}

	return transport.InMemoryTransportLogStore(), nil
}

// AppsConfig decodes AppsConfig from a local json config file.
func (c *Config) AppsConfig() (map[string]AppConfig, error) {
	apps := make(map[string]AppConfig)
	for _, app := range c.Apps {
		apps[app.App] = app
	}

	return apps, nil
}

// AppsDir returns absolute path for directory with application
// binaries. Directory will be created if necessary.
func (c *Config) AppsDir() (string, error) {
	if c.AppsPath == "" {
		return "", errors.New("empty AppsPath")
	}

	return ensureDir(c.AppsPath)
}

// LocalDir returns absolute path for app work directory. Directory
// will be created if necessary.
func (c *Config) LocalDir() (string, error) {
	if c.LocalPath == "" {
		return "", errors.New("empty LocalPath")
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

// HypervisorConfig represents hypervisor configuration.
type HypervisorConfig struct {
	PubKey cipher.PubKey `json:"public_key"`
	Addr   string        `json:"address"`
}

// DmsgConfig represents dmsg configuration.
type DmsgConfig struct {
	PubKey     cipher.PubKey
	SecKey     cipher.SecKey
	Discovery  disc.APIClient
	Retries    int
	RetryDelay time.Duration
}

// DmsgPtyConfig configures the dmsgpty-host.
type DmsgPtyConfig struct {
	Port     uint16 `json:"port"`
	AuthFile string `json:"authorization_file"`
	CLINet   string `json:"cli_network"`
	CLIAddr  string `json:"cli_address"`
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

// Duration wraps around time.Duration to allow parsing from and to JSON
type Duration time.Duration

// MarshalJSON implements json marshaling
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements unmarshal from json
func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		*d = Duration(time.Duration(value))
		return nil
	case string:
		tmp, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		*d = Duration(tmp)
		return nil
	default:
		return errors.New("invalid duration")
	}
}
