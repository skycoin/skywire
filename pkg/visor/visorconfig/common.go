// Package visorconfig defines the visor's config
package visorconfig

import (
	"errors"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

const (
	// Stdin read config from STDIN.
	Stdin = "STDIN"
	// Stdout write config to STDOUT.
	Stdout = "STDOUT"
)

var (
	// ErrNoConfigPath is returned on attempt to read/write config when visor contains no config path.
	ErrNoConfigPath = errors.New("no config path")
)

// Common represents the common fields that are shared across all config versions,
// alongside logging and flushing fields.
type Common struct {
	path string
	log  *logging.MasterLogger

	Version string        `json:"version"`
	SK      cipher.SecKey `json:"sk,omitempty"`
	PK      cipher.PubKey `json:"pk,omitempty"`
}

// NewCommon returns a new Common.
func NewCommon(log *logging.MasterLogger, confPath string, sk *cipher.SecKey) (*Common, error) {
	if log == nil {
		log = logging.NewMasterLogger()
	}
	c := new(Common)
	c.log = log
	c.path = confPath
	c.Version = Version()
	if sk != nil {
		c.SK = *sk
		if err := c.ensureKeys(); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// MasterLogger returns the underlying master logger.
func (c *Common) MasterLogger() *logging.MasterLogger {
	return c.log
}

// Path returns the on-disk path the config was loaded from. Empty
// when the config came from STDIN or was synthesized in-memory.
func (c *Common) Path() string {
	return c.path
}

// SetLogger sets logger.
func (c *Common) SetLogger(log *logging.MasterLogger) {
	c.log = log
}

func (c *Common) ensureKeys() error {
	if !c.PK.Null() {
		return nil
	}
	if c.SK.Null() {
		c.PK, c.SK = cipher.GenerateKeyPair()
		return nil
	}
	var err error
	if c.PK, err = c.SK.PubKey(); err != nil {
		return err
	}
	return nil
}

// flush serializes v as JSON and writes it to the Common's
// on-disk path. Implementation lives in common_native.go under
// //go:build !js because it pulls encoding/json (which drags the
// reflect runtime helpers TinyGo's stdlib lacks) and os.WriteFile
// (no-op in a browser anyway). Callers under js/wasm can't reach
// this method; any flush attempt panics with a clear error.
