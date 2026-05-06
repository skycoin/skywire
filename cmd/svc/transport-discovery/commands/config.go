// Package commands cmd/svc/transport-discovery/commands/config.go
package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// Config is the JSON configuration for transport-discovery. When
// --config is set, every runtime knob comes from this file; otherwise
// the service falls back to the legacy per-flag CLI behavior. The
// `dmsg` block matches dmsg-server's shape so operators see a
// consistent schema across visor / dmsg-server / every deployment
// service.
type Config struct {
	Path string `json:"-"`

	PubKey cipher.PubKey `json:"public_key,omitempty"`
	SecKey cipher.SecKey `json:"secret_key,omitempty"`

	Addr            string          `json:"addr,omitempty"`
	MetricsAddr     string          `json:"metrics_addr,omitempty"`
	PprofAddr       string          `json:"pprof_addr,omitempty"`
	Redis           string          `json:"redis,omitempty"`
	RedisPoolSize   int             `json:"redis_pool_size,omitempty"`
	EntryTimeout    time.Duration   `json:"entry_timeout,omitempty"`
	LogLevel        string          `json:"log_level,omitempty"`
	Tag             string          `json:"tag,omitempty"`
	Testing         bool            `json:"testing,omitempty"`
	Mode            string          `json:"mode,omitempty"`
	Whitelist       []string        `json:"whitelist_keys,omitempty"`
	SurveyWhitelist []cipher.PubKey `json:"survey_whitelist,omitempty"`
	TestEnvironment bool            `json:"test_environment,omitempty"`

	// StoreDataPath is the on-disk path for bandwidth backup files.
	StoreDataPath string `json:"store_data_path,omitempty"`
	// EnableCXO turns on the CXO aggregator + metrics publisher.
	EnableCXO bool `json:"enable_cxo,omitempty"`
	// UptimeDB is the local self-uptime bbolt store path. Empty
	// disables service-self uptime recording.
	UptimeDB string `json:"uptime_db,omitempty"`

	// Dmsg is the dmsg-related config block — same shape across
	// every deployment service that uses it.
	Dmsg cmdutil.DmsgConfig `json:"dmsg,omitempty"`
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

// LoadConfig reads, parses, and returns a Config from path.
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

// applyConfig copies values from c into the package-level cobra-flag
// vars that the rest of Run consumes. Empty / zero fields preserve
// the existing CLI-flag value, so a partial config is fine.
// Returns the embedded dmsg-server transit set (if any) and the
// survey whitelist override the config supplies, both of which Run
// uses directly rather than going through package-level vars.
func applyConfig(c *Config) (servers []*disc.Entry, surveyWL []cipher.PubKey) {
	if c.SecKey != (cipher.SecKey{}) {
		sk = c.SecKey
	}
	if c.Addr != "" {
		addr = c.Addr
	}
	if c.MetricsAddr != "" {
		metricsAddr = c.MetricsAddr
	}
	if c.PprofAddr != "" {
		pprofAddr = c.PprofAddr
	}
	if c.Redis != "" {
		redisURL = c.Redis
	}
	if c.RedisPoolSize > 0 {
		redisPoolSize = c.RedisPoolSize
	}
	if c.EntryTimeout != 0 {
		entryTimeout = c.EntryTimeout
	}
	if c.LogLevel != "" {
		logLvl = c.LogLevel
	}
	if c.Tag != "" {
		tag = c.Tag
	}
	if c.Testing {
		testing = true
	}
	if c.Mode != "" {
		mode = c.Mode
	}
	if c.TestEnvironment {
		testEnvironment = true
	}
	if len(c.Whitelist) > 0 {
		whitelistKeys = strings.Join(c.Whitelist, ",")
	}
	if c.StoreDataPath != "" {
		storeDataPath = c.StoreDataPath
	}
	if c.EnableCXO {
		enableCXO = true
	}
	if c.UptimeDB != "" {
		uptimeDB = c.UptimeDB
	}
	if c.Dmsg.Discovery != "" {
		dmsgDisc = c.Dmsg.Discovery
	}
	if c.Dmsg.ServerType != "" {
		dmsgServerType = c.Dmsg.ServerType
	}
	return c.Dmsg.Servers, c.SurveyWhitelist
}
