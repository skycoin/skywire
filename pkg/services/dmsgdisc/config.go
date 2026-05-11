// Package dmsgdisc pkg/services/dmsgdisc/config.go
//
// JSON schema for dmsg-discovery as both a standalone-service config
// file (cmd/dmsg/dmsg-discovery -c path) AND a service block in a
// multi-service services.json (skywire svc run --config services.json).
// Both consumers parse the same struct so there's a single source of
// truth for the field set.
package dmsgdisc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/services"
)

// Config is the JSON configuration for dmsg-discovery. Mirrors the
// cobra-flag set on cmd/dmsg/dmsg-discovery so a config file or a
// services.json block can drive the same Run() with no flag-only
// surprises.
//
// In services.json the block also carries `type: "dmsg-discovery"`
// and an optional `name`; those fields are consumed by the
// supervisor and are accepted-but-ignored by this struct (json
// "type" / "name" tags omitted — DisallowUnknownFields is OFF for
// the block path so the supervisor's framing fields pass through).
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
	EntryTimeout services.Duration `json:"entry_timeout,omitempty"`
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
	// MetricsAddr is the address to expose Prometheus metrics on
	// (empty disables metrics).
	MetricsAddr string `json:"metrics_addr,omitempty"`
	// PProfMode / PProfAddr — pass-through to dmsgcmdutil.InitPProf.
	PProfMode string `json:"pprof_mode,omitempty"`
	PProfAddr string `json:"pprof_addr,omitempty"`

	// DmsgServers is the static dmsg-server transit set the
	// discovery preloads at startup. Replaces the runtime read of
	// deployment.Prod.DmsgServers — operators ship a config file
	// generated from the keyring and edit it as servers rotate, no
	// rebuild required. Empty list falls back to the embedded
	// keyring at runtime as a last-resort safety net.
	DmsgServers []*disc.Entry `json:"dmsg_servers,omitempty"`
}

// LoadFile reads, parses, and returns a Config from the given path.
// Returns an error if the file is missing, malformed, or has fields
// the JSON decoder doesn't recognize (strict decoding catches typos).
//
// Used by the standalone cobra command (`skywire dmsg disc -c path`).
// The multi-service supervisor goes through ParseBlock instead since
// it has the bytes already.
func LoadFile(path string) (*Config, error) {
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

// ParseBlock decodes a services.json block (the inline JSON object
// the supervisor passes through Block.Raw) into a Config. Unlike
// LoadFile, this path tolerates unknown fields — the supervisor's
// framing keys ("type", "name") arrive in the same object, and any
// future inline-block extension shouldn't break older binaries.
func ParseBlock(raw []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("dmsg-discovery: parse block: %w", err)
	}
	return &c, nil
}
