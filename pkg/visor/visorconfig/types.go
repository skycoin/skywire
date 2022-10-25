// Package visorconfig pkg/visor/visorconfig/types.go
package visorconfig

import (
	"encoding/json"
	"errors"
	"time"
)

// LogStore types.
const (
	FileLogStore   = "file"
	MemoryLogStore = "memory"
)

const (
	// DefaultTimeout is used for default config generation and if it is not set in config.
	DefaultTimeout = Duration(10 * time.Second)
	// DefaultLogRotationInterval is used as default for RotationInterval and if it is not set in Log.
	DefaultLogRotationInterval = Duration(time.Hour * 24 * 7)
)

// Duration wraps around time.Duration to allow parsing from and to JSON
type Duration time.Duration

// MarshalJSON implements json marshaling
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements unmarshal from json
func (d *Duration) UnmarshalJSON(b []byte) error {
	if len(b) == 0 {
		*d = 0
		return nil
	}

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

// VisorConfig is every field of the config
type VisorConfig struct {
	Version string `json:"version"`
	Sk      string `json:"sk"`
	Pk      string `json:"pk"`
	Dmsg    struct {
		Discovery     string `json:"discovery"`
		SessionsCount int    `json:"sessions_count"`
		Servers       []struct {
			Version   string `json:"version"`
			Sequence  int    `json:"sequence"`
			Timestamp int    `json:"timestamp"`
			Static    string `json:"static"`
			Server    struct {
				Address           string `json:"address"`
				AvailableSessions int    `json:"availableSessions"`
			} `json:"server"`
		} `json:"servers"`
	} `json:"dmsg"`
	Dmsgpty struct {
		DmsgPort   int    `json:"dmsg_port"`
		CliNetwork string `json:"cli_network"`
		CliAddress string `json:"cli_address"`
	} `json:"dmsgpty"`
	SkywireTCP struct {
		PkTable          interface{} `json:"pk_table"`
		ListeningAddress string      `json:"listening_address"`
	} `json:"skywire-tcp"`
	Transport struct {
		Discovery           string      `json:"discovery"`
		AddressResolver     string      `json:"address_resolver"`
		PublicAutoconnect   bool        `json:"public_autoconnect"`
		TransportSetupNodes interface{} `json:"transport_setup_nodes"`
	} `json:"transport"`
	Routing struct {
		SetupNodes         []string `json:"setup_nodes"`
		RouteFinder        string   `json:"route_finder"`
		RouteFinderTimeout string   `json:"route_finder_timeout"`
		MinHops            int      `json:"min_hops"`
	} `json:"routing"`
	UptimeTracker struct {
		Addr string `json:"addr"`
	} `json:"uptime_tracker"`
	Launcher struct {
		ServiceDiscovery string `json:"service_discovery"`
		Apps             []struct {
			Name      string   `json:"name"`
			AutoStart bool     `json:"auto_start"`
			Port      int      `json:"port"`
			Args      []string `json:"args,omitempty"`
		} `json:"apps"`
		ServerAddr string `json:"server_addr"`
		BinPath    string `json:"bin_path"`
	} `json:"launcher"`
	Hypervisors          []interface{} `json:"hypervisors"`
	CliAddr              string        `json:"cli_addr"`
	LogLevel             string        `json:"log_level"`
	LocalPath            string        `json:"local_path"`
	StunServers          []string      `json:"stun_servers"`
	ShutdownTimeout      string        `json:"shutdown_timeout"`
	RestartCheckDelay    string        `json:"restart_check_delay"`
	IsPublic             bool          `json:"is_public"`
	PersistentTransports interface{}   `json:"persistent_transports"`
	Hypervisor           struct {
		DbPath     string `json:"db_path"`
		EnableAuth bool   `json:"enable_auth"`
		Cookies    struct {
			HashKey         string `json:"hash_key"`
			BlockKey        string `json:"block_key"`
			ExpiresDuration int64  `json:"expires_duration"`
			Path            string `json:"path"`
			Domain          string `json:"domain"`
		} `json:"cookies"`
		DmsgPort    int    `json:"dmsg_port"`
		HTTPAddr    string `json:"http_addr"`
		EnableTLS   bool   `json:"enable_tls"`
		TLSCertFile string `json:"tls_cert_file"`
		TLSKeyFile  string `json:"tls_key_file"`
	} `json:"hypervisor"`
}
