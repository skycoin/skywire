// Package visorconfig defines the visor's config
package visorconfig

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	// StdinName is the path name used to identify STDIN.
	StdinName = "STDIN"
	// StdoutName is the path name used to identify STDOUT.
	StdoutName = "STDOUT"
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
	c.Version = skyenv.Version()
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

func (c *Common) flush(v interface{}) (err error) {
	switch c.path {
	case "":
		return ErrNoConfigPath
	case StdinName:
		return nil
	}

	log := c.log.
		PackageLogger("visor:config").
		WithField("filepath", c.path).
		WithField("config_version", c.Version)
	log.Info("Flushing config to file.")
	defer func() {
		if err != nil {
			log.WithError(err).Error("Failed to flush config to file.")
		}
	}()

	raw, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		return err
	}
	const filePerm = 0644
	return os.WriteFile(c.path, raw, filePerm)
}
