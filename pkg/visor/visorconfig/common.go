package visorconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
)

const (
	// StdinName is the path name used to identify STDIN.
	StdinName = "STDIN"
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
	pk      cipher.PubKey
}

// NewCommon returns a new Common.
func NewCommon(log *logging.MasterLogger, confPath string, version string, sk *cipher.SecKey) (*Common, error) {
	if log == nil {
		log = logging.NewMasterLogger()
	}

	c := new(Common)
	c.log = log
	c.path = confPath
	c.Version = version
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

// PK returns the visor's public key.
func (c *Common) PK() cipher.PubKey {
	if err := c.ensureKeys(); err != nil {
		panic(fmt.Errorf("invalid 'sk' defined in visor config: %w", err))
	}
	return c.pk
}

func (c *Common) ensureKeys() error {
	if !c.pk.Null() {
		return nil
	}
	if c.SK.Null() {
		c.pk, c.SK = cipher.GenerateKeyPair()
		return nil
	}
	var err error
	if c.pk, err = c.SK.PubKey(); err != nil {
		return err
	}
	return nil
}

func (c *Common) flush(v interface{}) error {
	switch c.path {
	case "":
		return ErrNoConfigPath
	case StdinName:
		return nil
	}

	j, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	c.log.Debugf("Updating visor config to: %s", string(j))

	raw, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		return err
	}
	const filePerm = 0644
	return ioutil.WriteFile(c.path, raw, filePerm)
}
