// Package tpd pkg/services/tpd/config.go
//
// JSON schema for transport-discovery as both a standalone config
// file (cmd/svc/transport-discovery -c path) AND a service block in
// services.json (skywire svc run --config services.json).
package tpd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/services"
)

// Config is the JSON configuration for transport-discovery.
type Config struct {
	Path string `json:"-"`

	PubKey cipher.PubKey `json:"public_key,omitempty"`
	SecKey cipher.SecKey `json:"secret_key,omitempty"`

	Addr            string            `json:"addr,omitempty"`
	MetricsAddr     string            `json:"metrics_addr,omitempty"`
	PprofAddr       string            `json:"pprof_addr,omitempty"`
	Redis           string            `json:"redis,omitempty"`
	RedisPoolSize   int               `json:"redis_pool_size,omitempty"`
	EntryTimeout    services.Duration `json:"entry_timeout,omitempty"`
	LogLevel        string            `json:"log_level,omitempty"`
	Tag             string            `json:"tag,omitempty"`
	Testing         bool              `json:"testing,omitempty"`
	Mode            string            `json:"mode,omitempty"`
	Whitelist       []string          `json:"whitelist_keys,omitempty"`
	SurveyWhitelist []cipher.PubKey   `json:"survey_whitelist,omitempty"`
	TestEnvironment bool              `json:"test_environment,omitempty"`

	// StoreDataPath is the on-disk path for bandwidth backup files.
	StoreDataPath string `json:"store_data_path,omitempty"`
	// EnableCXO turns on the CXO aggregator + metrics publisher.
	EnableCXO bool `json:"enable_cxo,omitempty"`
	// UptimeDB is the local self-uptime bbolt store path. Empty
	// disables service-self uptime recording.
	UptimeDB string `json:"uptime_db,omitempty"`

	// DmsgPort is the dmsghttp listener port (default 80).
	DmsgPort uint16 `json:"dmsg_port,omitempty"`

	// Dmsg is the dmsg-related config block — same shape across
	// every deployment service that uses it.
	Dmsg cmdutil.DmsgConfig `json:"dmsg,omitempty"`
}

// LoadFile reads and strict-parses a Config from path. Used by the
// standalone cobra command (--config).
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

// ParseBlock decodes a services.json block (tolerant of unknown
// fields — the supervisor's framing keys "type"/"name" arrive in
// the same object).
func ParseBlock(raw []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("transport-discovery: parse block: %w", err)
	}
	return &c, nil
}

// dmsgDiscEntries returns the dmsg-server transit set svcmode.Start
// expects ([]disc.Entry, value type) from the config's []*disc.Entry
// when non-empty, otherwise falls back to the embedded
// dmsg.Prod.DmsgServers keyring.
func dmsgDiscEntries(configServers []*disc.Entry) []disc.Entry {
	if len(configServers) == 0 {
		return dmsg.Prod.DmsgServers
	}
	out := make([]disc.Entry, 0, len(configServers))
	for _, e := range configServers {
		if e != nil {
			out = append(out, *e)
		}
	}
	return out
}
