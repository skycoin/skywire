// Package commands cmd/dmsg-discovery/commands/config.go
package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// Config is the JSON configuration for dmsg-discovery. When --config
// is set, every runtime knob comes from this file; otherwise the
// service falls back to the legacy per-flag CLI behavior so existing
// deployments don't break. The schema mirrors the visor / dmsg-server
// pattern: per-service knobs at the top, dmsg-server transit set in
// `dmsg_servers` (replaces the runtime read of
// deployment.Prod.DmsgServers).
type Config struct {
	Path string `json:"-"`

	PubKey cipher.PubKey `json:"public_key,omitempty"`
	SecKey cipher.SecKey `json:"secret_key,omitempty"`

	// Addr is the HTTP listen address for the discovery API
	// (`:9090` by default).
	Addr string `json:"addr,omitempty"`
	// Redis is the redis connection URL the discovery uses for the
	// entry store.
	Redis string `json:"redis,omitempty"`
	// DmsgPort is the dmsghttp listener port (default 80).
	DmsgPort uint16 `json:"dmsg_port,omitempty"`
	// EntryTimeout is how long client discovery entries live in
	// redis between refreshes. Zero disables expiry (legacy
	// behavior; stale entries accumulate forever).
	EntryTimeout time.Duration `json:"entry_timeout,omitempty"`
	// Mode is the listener mode: "http" or "dual" (dmsg-only is
	// rejected because dmsg-servers register over plain HTTP).
	Mode string `json:"mode,omitempty"`
	// AuthPassphrase, when non-empty, gates official-server
	// registration with a shared secret.
	AuthPassphrase string `json:"auth_passphrase,omitempty"`
	// OfficialServers is a list of PKs treated as "official" — they
	// can register without the auth passphrase. Hex-encoded PKs.
	OfficialServers []string `json:"official_servers,omitempty"`
	// DmsgServerType filters dmsg-servers by their declared type.
	DmsgServerType string `json:"dmsg_server_type,omitempty"`
	// TestMode disables some runtime checks for use in tests.
	TestMode bool `json:"test_mode,omitempty"`
	// EnableLoadTesting allows sending fake load to the discovery.
	EnableLoadTesting bool `json:"enable_load_testing,omitempty"`
	// TestEnvironment selects deployment.Test over deployment.Prod
	// for any embedded-keyring fallback at gen time.
	TestEnvironment bool `json:"test_environment,omitempty"`
	// Whitelist is the network-monitor PKs allowed to deregister
	// stale entries. Hex-encoded PKs.
	Whitelist []string `json:"whitelist_keys,omitempty"`
	// LogLevel is the minimum log level (debug/info/warn/error).
	LogLevel string `json:"log_level,omitempty"`

	// DmsgServers is the static dmsg-server transit set the
	// discovery preloads at startup. Replaces the runtime read of
	// deployment.Prod.DmsgServers — operators ship a config file
	// generated from the keyring and edit it as servers rotate, no
	// rebuild required. Empty list falls back to the embedded
	// keyring at runtime as a last-resort safety net.
	DmsgServers []*disc.Entry `json:"dmsg_servers,omitempty"`
}

// applyConfig copies values from a parsed Config into the
// package-level cobra-flag variables that the rest of Run consumes,
// turning --config into a single source of truth. Empty / zero
// fields in Config preserve the existing CLI-flag value, so a
// partial config still leaves the rest at their defaults.
func applyConfig(c *Config) {
	if c.SecKey != (cipher.SecKey{}) {
		sk = c.SecKey
	}
	if c.Addr != "" {
		addr = c.Addr
	}
	if c.Redis != "" {
		redisURL = c.Redis
	}
	if c.DmsgPort != 0 {
		dmsgPort = c.DmsgPort
	}
	if c.EntryTimeout != 0 {
		entryTimeout = c.EntryTimeout
	}
	if c.Mode != "" {
		mode = c.Mode
	}
	if c.AuthPassphrase != "" {
		authPassphrase = c.AuthPassphrase
	}
	if len(c.OfficialServers) > 0 {
		officialServers = strings.Join(c.OfficialServers, ",")
	}
	if c.DmsgServerType != "" {
		dmsgServerType = c.DmsgServerType
	}
	if c.TestMode {
		testMode = true
	}
	if c.EnableLoadTesting {
		enableLoadTesting = true
	}
	if c.TestEnvironment {
		testEnvironment = true
	}
	if len(c.Whitelist) > 0 {
		whitelistKeys = strings.Join(c.Whitelist, ",")
	}
}

// LoadConfig reads, parses, and returns a Config from the given path.
// Returns an error if the file is missing, malformed, or has fields
// the JSON decoder doesn't recognize (strict decoding catches typos).
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	c.Path = path
	return &c, nil
}
