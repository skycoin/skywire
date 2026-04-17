// Package deployment deploy_config.go
//
// DeployConfig defines the unified JSON configuration format for a skywire
// deployment. It extends the existing services-config.json (which defines
// service URLs and PKs visible to visors) with per-service operational
// config (ports, redis, TTLs, keyfiles, pprof).
//
// Each service reads its own section from the file. Secret keys are stored
// in separate keyfiles (referenced by path) rather than inline, so the
// config can be shared without exposing secrets.
//
// Usage:
//
//	skywire svc tpd --deploy-config /etc/skywire/deploy.json --env prod
//
// The --deploy-config flag is optional — services still accept all existing
// flags. When both are specified, flags override config file values.
//
// Generate a template with:
//
//	skywire svc deploy-template [--env prod|test]
package deployment

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// ServiceConfig holds operational config for a single deployment service.
// Each field maps to an existing CLI flag.
type ServiceConfig struct {
	// Addr is the HTTP listen address (e.g., ":9094").
	Addr string `json:"addr,omitempty"`

	// Keyfile is the path to a file containing the service's secret key.
	// Preferred over inline SK for security.
	Keyfile string `json:"keyfile,omitempty"`

	// Redis is the Redis connection URL (e.g., "redis://redis:6379").
	Redis string `json:"redis,omitempty"`

	// RedisPoolSize is the Redis connection pool size.
	RedisPoolSize int `json:"redis_pool_size,omitempty"`

	// DmsgDisc is the DMSG discovery URL used by this service.
	DmsgDisc string `json:"dmsg_disc,omitempty"`

	// PprofAddr is the pprof HTTP listen address (e.g., ":6094").
	PprofAddr string `json:"pprof,omitempty"`

	// EntryTimeout is the TTL for entries managed by this service.
	EntryTimeout string `json:"entry_timeout,omitempty"`

	// WhitelistKeys are network monitor PKs authorized to deregister entries.
	WhitelistKeys []string `json:"whitelist_keys,omitempty"`

	// Mode is the listener mode: "http", "dmsg", or "dual".
	Mode string `json:"mode,omitempty"`

	// ConfigFile is a path to an additional service-specific config file
	// (e.g., dmsg-server config.json, setup-node config.json).
	ConfigFile string `json:"config_file,omitempty"`

	// PostgresHost is the PostgreSQL host (for UT).
	PostgresHost string `json:"pg_host,omitempty"`

	// GodebugEnv is the GODEBUG environment variable value.
	GodebugEnv string `json:"godebug,omitempty"`
}

// DmsgServerConfig holds config for a single dmsg server instance.
type DmsgServerConfig struct {
	// Name is the container/instance name (e.g., "dmsg-server-1").
	Name string `json:"name"`

	// ConfigFile is the path to the dmsg server's JSON config.
	ConfigFile string `json:"config_file"`

	// PublicPort is the host port mapped to the server's listen port.
	PublicPort int `json:"public_port"`

	// PprofAddr is the pprof listen address.
	PprofAddr string `json:"pprof,omitempty"`
}

// DeployEnvConfig holds the full deployment config for one environment.
type DeployEnvConfig struct {
	// Embed the existing services-config fields (URLs, PKs, servers).
	EnvServices `json:",inline"`

	// Per-service operational config.
	Services struct {
		DmsgDiscovery      *ServiceConfig `json:"dmsg_discovery,omitempty"`
		TransportDiscovery *ServiceConfig `json:"transport_discovery,omitempty"`
		AddressResolver    *ServiceConfig `json:"address_resolver,omitempty"`
		RouteFinder        *ServiceConfig `json:"route_finder,omitempty"`
		ServiceDiscovery   *ServiceConfig `json:"service_discovery,omitempty"`
		UptimeTracker      *ServiceConfig `json:"uptime_tracker,omitempty"`
		SetupNode          *ServiceConfig `json:"setup_node,omitempty"`
		NetworkMonitor     *ServiceConfig `json:"network_monitor,omitempty"`
		ConfService        *ServiceConfig `json:"conf_service,omitempty"`
		GeoIP              *ServiceConfig `json:"geoip,omitempty"`
		Stun               *ServiceConfig `json:"stun,omitempty"`
		TransportSetup     *ServiceConfig `json:"transport_setup,omitempty"`
	} `json:"services,omitempty"`

	// DmsgServers lists dmsg server instances for this environment.
	DmsgServerInstances []DmsgServerConfig `json:"dmsg_server_instances,omitempty"`

	// Infrastructure.
	Redis struct {
		URL      string `json:"url,omitempty"`
		Password string `json:"password,omitempty"`
	} `json:"redis,omitempty"`

	Postgres struct {
		Host     string `json:"host,omitempty"`
		User     string `json:"user,omitempty"`
		Database string `json:"database,omitempty"`
	} `json:"postgres,omitempty"`
}

// DeployConfig is the top-level config with prod/test environments.
type DeployConfig struct {
	Prod *DeployEnvConfig `json:"prod,omitempty"`
	Test *DeployEnvConfig `json:"test,omitempty"`
}

// DefaultDeployConfig returns a template DeployConfig with sensible defaults
// that can be customized for a specific deployment.
func DefaultDeployConfig() *DeployConfig {
	defaults := func() *DeployEnvConfig {
		return &DeployEnvConfig{
			Services: struct {
				DmsgDiscovery      *ServiceConfig `json:"dmsg_discovery,omitempty"`
				TransportDiscovery *ServiceConfig `json:"transport_discovery,omitempty"`
				AddressResolver    *ServiceConfig `json:"address_resolver,omitempty"`
				RouteFinder        *ServiceConfig `json:"route_finder,omitempty"`
				ServiceDiscovery   *ServiceConfig `json:"service_discovery,omitempty"`
				UptimeTracker      *ServiceConfig `json:"uptime_tracker,omitempty"`
				SetupNode          *ServiceConfig `json:"setup_node,omitempty"`
				NetworkMonitor     *ServiceConfig `json:"network_monitor,omitempty"`
				ConfService        *ServiceConfig `json:"conf_service,omitempty"`
				GeoIP              *ServiceConfig `json:"geoip,omitempty"`
				Stun               *ServiceConfig `json:"stun,omitempty"`
				TransportSetup     *ServiceConfig `json:"transport_setup,omitempty"`
			}{
				DmsgDiscovery: &ServiceConfig{
					Addr:         ":9090",
					Keyfile:      "/etc/skywire/keys/dmsgdisc.key",
					Redis:        "redis://redis:6379",
					PprofAddr:    ":6071",
					EntryTimeout: "60m",
					GodebugEnv:   "madvdontneed=1",
				},
				TransportDiscovery: &ServiceConfig{
					Addr:         ":9094",
					Keyfile:      "/etc/skywire/keys/tpd.key",
					Redis:        "redis://redis:6379",
					PprofAddr:    ":6094",
					EntryTimeout: "5m",
					GodebugEnv:   "madvdontneed=1",
				},
				AddressResolver: &ServiceConfig{
					Addr:          ":9093",
					Keyfile:       "/etc/skywire/keys/ar.key",
					Redis:         "redis://redis:6379",
					RedisPoolSize: 150,
					PprofAddr:     ":6093",
					EntryTimeout:  "2m",
					GodebugEnv:    "madvdontneed=1",
				},
				RouteFinder: &ServiceConfig{
					Addr:       ":9092",
					Keyfile:    "/etc/skywire/keys/rf.key",
					Redis:      "redis://redis:6379",
					PprofAddr:  ":6092",
					GodebugEnv: "madvdontneed=1",
				},
				ServiceDiscovery: &ServiceConfig{
					Addr:         ":9091",
					Keyfile:      "/etc/skywire/keys/sd.key",
					Redis:        "redis://redis:6379",
					PprofAddr:    ":6091",
					EntryTimeout: "2m",
					GodebugEnv:   "madvdontneed=1",
				},
				UptimeTracker: &ServiceConfig{
					Addr:         ":9095",
					Keyfile:      "/etc/skywire/keys/ut.key",
					Redis:        "redis://redis:6379",
					PprofAddr:    ":6095",
					PostgresHost: "postgres",
					GodebugEnv:   "madvdontneed=1",
				},
				SetupNode: &ServiceConfig{
					ConfigFile: "/etc/skywire/setup-node/config.json",
					PprofAddr:  ":6070",
					GodebugEnv: "madvdontneed=1",
				},
				NetworkMonitor: &ServiceConfig{
					Addr:      ":9080",
					PprofAddr: ":6080",
				},
				ConfService: &ServiceConfig{
					ConfigFile: "/etc/skywire/config-bootstrapper/config.json",
					Keyfile:    "/etc/skywire/keys/conf.key",
				},
				GeoIP: &ServiceConfig{
					Addr:      ":9099",
					PprofAddr: ":6081",
				},
				TransportSetup: &ServiceConfig{
					ConfigFile: "/etc/skywire/transport-setup/tps.json",
				},
			},
			Redis: struct {
				URL      string `json:"url,omitempty"`
				Password string `json:"password,omitempty"`
			}{
				URL: "redis://redis:6379",
			},
			Postgres: struct {
				Host     string `json:"host,omitempty"`
				User     string `json:"user,omitempty"`
				Database string `json:"database,omitempty"`
			}{
				Host: "postgres",
				User: "postgres",
			},
		}
	}

	return &DeployConfig{
		Prod: defaults(),
		Test: defaults(),
	}
}

// PrintTemplate prints the default DeployConfig as formatted JSON.
func PrintTemplate(env string) error {
	cfg := DefaultDeployConfig()

	var out interface{}
	switch env {
	case "prod":
		out = cfg.Prod
	case "test":
		out = cfg.Test
	default:
		out = cfg
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("failed to encode template: %w", err)
	}
	return nil
}

// LoadDeployConfig reads a DeployConfig from a JSON file.
func LoadDeployConfig(path string) (*DeployConfig, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read deploy config: %w", err)
	}
	var cfg DeployConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse deploy config: %w", err)
	}
	return &cfg, nil
}

// ParseDuration is a helper that parses a duration string, returning 0 on empty.
func ParseDuration(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, _ := time.ParseDuration(s) //nolint:errcheck
	return d
}
