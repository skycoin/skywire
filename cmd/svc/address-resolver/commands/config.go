// Package commands cmd/svc/address-resolver/commands/config.go
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

// Config is the JSON configuration for address-resolver.
type Config struct {
	Path string `json:"-"`

	PubKey cipher.PubKey `json:"public_key,omitempty"`
	SecKey cipher.SecKey `json:"secret_key,omitempty"`

	Addr            string          `json:"addr,omitempty"`
	UDPAddr         string          `json:"udp_addr,omitempty"`
	PublicUDPAddr   string          `json:"public_udp_addr,omitempty"`
	MetricsAddr     string          `json:"metrics_addr,omitempty"`
	PprofAddr       string          `json:"pprof_addr,omitempty"`
	Redis           string          `json:"redis,omitempty"`
	RedisPoolSize   int             `json:"redis_pool_size,omitempty"`
	EntryTimeout    time.Duration   `json:"entry_timeout,omitempty"`
	Tag             string          `json:"tag,omitempty"`
	LogLevel        string          `json:"log_level,omitempty"`
	Testing         bool            `json:"testing,omitempty"`
	Mode            string          `json:"mode,omitempty"`
	Whitelist       []string        `json:"whitelist_keys,omitempty"`
	SurveyWhitelist []cipher.PubKey `json:"survey_whitelist,omitempty"`
	TestEnvironment bool            `json:"test_environment,omitempty"`

	// Dmsg is the dmsg-related config block — same shape across
	// every deployment service that uses it.
	Dmsg cmdutil.DmsgConfig `json:"dmsg,omitempty"`
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
// vars that the rest of Run consumes.
func applyConfig(c *Config) (servers []*disc.Entry, surveyWL []cipher.PubKey) {
	if c.SecKey != (cipher.SecKey{}) {
		sk = c.SecKey
	}
	if c.Addr != "" {
		addr = c.Addr
	}
	if c.UDPAddr != "" {
		udpAddr = c.UDPAddr
	}
	if c.PublicUDPAddr != "" {
		publicUDPAddr = c.PublicUDPAddr
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
	if c.Tag != "" {
		tag = c.Tag
	}
	if c.LogLevel != "" {
		logLvl = c.LogLevel
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
	if c.Dmsg.Discovery != "" {
		dmsgDisc = c.Dmsg.Discovery
	}
	if c.Dmsg.ServerType != "" {
		dmsgServerType = c.Dmsg.ServerType
	}
	return c.Dmsg.Servers, c.SurveyWhitelist
}

// dmsgDiscEntries returns []disc.Entry for svcmode.Start, falling
// back to the embedded keyring when the config has no servers.
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
