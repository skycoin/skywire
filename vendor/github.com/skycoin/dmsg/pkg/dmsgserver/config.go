// Package dmsgserver pkg/dmsgserver/config.go
package dmsgserver

import (
	"encoding/json"
	"os"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire-utilities/pkg/cipher"

	"github.com/skycoin/dmsg/pkg/dmsg"
)

const (
	defaultPublicAddress = "127.0.0.1:8081"
	defaultLocalAddress  = ":8081"
	defaultHTTPAddress   = ":8082"

	// DefaultConfigPath default path of config file
	DefaultConfigPath = "config.json"
)

var defaultDiscoveryURL = dmsg.DiscAddr(false)

// DefaultDiscoverURLTest default URL for discovery in test env
var DefaultDiscoverURLTest = dmsg.DiscAddr(true)

// Config is structure of config file
type Config struct {
	Path string `json:"-"`

	PubKey         cipher.PubKey `json:"public_key"`
	SecKey         cipher.SecKey `json:"secret_key"`
	Discovery      string        `json:"discovery"`
	PublicAddress  string        `json:"public_address"`
	LocalAddress   string        `json:"local_address"`
	HTTPAddress    string        `json:"health_endpoint_address"`
	LogLevel       string        `json:"log_level"`
	UpdateInterval time.Duration `json:"update_interval"`
	MaxSessions    int           `json:"max_sessions"`
}

// GenerateDefaultConfig generate default config for dmsg-server
func GenerateDefaultConfig(c *Config) {
	pk, sk := cipher.GenerateKeyPair()

	c.Path = DefaultConfigPath
	c.PubKey = pk
	c.SecKey = sk
	c.Discovery = defaultDiscoveryURL
	c.PublicAddress = defaultPublicAddress
	c.LocalAddress = defaultLocalAddress
	c.HTTPAddress = defaultHTTPAddress
	c.LogLevel = "info"
	c.MaxSessions = 2048
}

// Flush trying to save config file
func (c Config) Flush(log *logging.Logger) (err error) {
	defer func() {
		if err != nil {
			log.WithError(err).Error("Failed to flush config to file.")
		}
	}()

	raw, err := json.MarshalIndent(c, "", "\t")
	if err != nil {
		return err
	}

	return os.WriteFile(c.Path, raw, 0600)
}
