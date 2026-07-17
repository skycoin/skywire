// Package config pkg/transport-setup/config/config.go c2-net-transport
package config

import (
	"encoding/json"
	"io"
	"os"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/logging"
)

// Config defines configuration for transport setup
type Config struct {
	PK   cipher.PubKey    `json:"public_key"`
	SK   cipher.SecKey    `json:"secret_key"`
	Port uint16           `json:"port"`
	Dmsg dmsgc.DmsgConfig `json:"dmsg"`
}

// MustReadConfig reads and decodes config from file. If there is an error,
// it reports the error and exits application
func MustReadConfig(filename string, log *logging.Logger) Config {
	rdr, err := os.Open(filename) //nolint:gosec
	if err != nil {
		log.Fatalf("Failed to open config: %v", err)
	}
	// Close the handle before returning. On Windows an open handle keeps the
	// file locked, which (in tests, where Fatalf is neutralized) breaks
	// t.TempDir cleanup with "being used by another process". Calling Close on
	// a nil *os.File (open failed + neutralized Fatalf) is a safe no-op.
	defer func() { _ = rdr.Close() }() //nolint:errcheck,gosec
	conf := Config{}
	raw, err := io.ReadAll(rdr)
	if err != nil {
		log.Fatalf("Failed to read config: %v", err)
	}
	if err := json.Unmarshal(raw, &conf); err != nil {
		log.WithField("raw", string(raw)).Fatalf("Failed to decode config: %s", err)
	}
	return conf
}
